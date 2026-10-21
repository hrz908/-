r"""MCP 行情工具子图（有界 ReAct 循环）。

流程:
    tool_call --(有 tool_calls 且未超轮次)--> tool_execute --┐
       ↑                                                    │
       └────────────────────────────────────────────────────┘
    tool_call --(无 tool_calls / 已达轮次上限)--> synthesize --> END

tool_execute 执行完工具后回到 tool_call，让模型看到工具结果再决定「继续调」还是
「收尾作答」。这样才能完成「先 symbol_search 拿代码、再用代码 get_quote/get_valuation」
的多跳链路。轮次由 _MAX_TOOL_ROUNDS 封顶，避免无限循环。

- tool_call: LLM 绑定工具，决定是否/如何调用工具（streaming=False，不外泄推理 token）。
- tool_execute: 并发执行本轮所有 tool_calls（每个工具已内置超时 + 重试 + 失败降级），
  每个工具调用前后发射 tool_start / tool_end 事件，产出 ToolMessage；
  tool_results 跨轮累加（读取已有 + 本轮），保证 synthesize 能看到全部工具数据。
- synthesize: 用 prompts/mcp.xml + 累计 tool_results 流式生成最终回答，
  并把行情/K线/估值等结构化数据以 card/table 事件独立发射。
"""
import asyncio
import json
import logging
import re
import time
import uuid
from datetime import datetime

from langchain_core.messages import SystemMessage, HumanMessage, AIMessage, ToolMessage
from langgraph.graph import StateGraph, START, END

from config import MAX_INPUT_TOKENS
from llm import get_llm, count_message_tokens, is_context_overflow
from state import AgentState
from tools import get_tools
from prompts.parse import render
from subgraphs import emit, emit_card, emit_table

logger = logging.getLogger("stocka.agent")

_TOOL_SYSTEM = (
    "你是股票行情助手，可调用工具获取实时数据。规则：\n"
    "1. 需要行情/K线/代码时，必须调用工具，不要凭空作答。\n"
    "2. 用户只给了名称/拼音时，先用 symbol_search 拿到代码，下一轮再用代码查行情/K线/估值。\n"
    "3. 可分多轮逐步取数（先搜代码再查数据），也可一轮内并行调用多个工具。\n"
    "4. 拿到足够数据后停止调用工具，直接回答。\n"
    "5. K 线默认取最近 10 根（count=10）；只有用户明确要更长区间（如近三个月、半年）\n"
    "   时才加大 count，上限 60。不要一次拉满：数据要进对话卡片，太多会挤爆前端。\n"
    "6. 每轮提问都要重新取数：不得因为历史消息里已有数据就跳过工具调用，\n"
    "   行情/ K 线是实时数据，沿用上一轮的数字等于给出过期答案。\n"
    "7. 工具全部失败时如实说明「暂无法获取」，禁止编造数字，\n"
    "   也禁止出现与用户提问无关的标的名称。"
)

# 多跳工具调用的轮次上限：tool_execute 每完成一轮就回到 tool_call，
# 最多允许 _MAX_TOOL_ROUNDS 轮工具执行，超过则强制进入 synthesize，防止无限循环。
# 3 轮足够覆盖「搜代码 -> 查行情 -> 查估值/资金流」这类常见链式取数。
_MAX_TOOL_ROUNDS = 3

# 单条消息允许进入 tool_call 上下文的最大字符数（见 _current_run_messages）
_MAX_MSG_CHARS = 2000
# synthesize 里塞进提示词的工具结果上限（字符）
_MAX_TOOL_CHARS = 6000
# 单个工具的卡片事件上限：一轮问多只股票时一标的一卡，封顶防刷屏
_MAX_CARDS_PER_TOOL = 3

# 工具名 -> 中文标签（tool_start 事件用，前端展示“正在获取xx”）。
# Tushare 增强工具也要给标签，否则前端只能显示原始工具名
_TOOL_LABELS = {
    "get_quote": "实时行情",
    "get_kline": "K线数据",
    "symbol_search": "股票搜索",
    "get_valuation": "估值指标",
    "get_financial_indicator": "财务指标",
    "get_financial_report": "财报数据",
    "get_money_flow": "资金流向",
    "get_top_list": "龙虎榜",
    "get_hsgt_flow": "北向资金",
    "get_stock_news": "财经资讯",
    "get_history_daily": "历史行情",
}

# 工具名 -> 结构化事件构造器
_TOOLS = None


def _tools():
    global _TOOLS
    if _TOOLS is None:
        _TOOLS = get_tools()
    return _TOOLS


def _tools_by_name():
    return {t.name: t for t in _tools()}


def _recent_history(state: dict, limit: int = 6) -> list:
    """取最近 limit 条「纯文本」对话历史。

    工具调用与工具结果一律不进 LLM 上下文，两个原因：
    1. ToolMessage 里是 json.dumps 的完整工具载荷，Tushare 类接口一次能返回几千行
       （估值接口整段历史实测约 30 万 token），直接进对话会触发上游 max_seq_len 报错，
       整条回答变成空；
    2. 带 tool_calls 的 AIMessage 一旦与对应 ToolMessage 配对被裁掉，就会变成残缺的
       tool_call，上游接口同样会报错。
    工具数据统一由 prompts/mcp.xml 的 <tool_results> 段承载（那里有字符上限）。
    """
    msgs = list(state.get("messages", []))[-(limit + 1):-1]
    current = (state.get("query") or "").strip()
    out = []
    for m in msgs:
        if isinstance(m, ToolMessage):
            continue
        if isinstance(m, AIMessage) and getattr(m, "tool_calls", None):
            continue
        if isinstance(m, HumanMessage) and current:
            content = m.content if isinstance(m.content, str) else str(m.content or "")
            if content.strip() == current:
                # 本轮问题由 synthesize 显式追加，历史里的同一句不重复带
                continue
        out.append(_shrink(m))
    return out


def _shrink(msg):
    """把单条消息的正文截到 _MAX_MSG_CHARS，字段（tool_call_id 等）原样保留。

    只截正文、不重建消息类型：工具调用协议依赖 tool_call_id 与消息类型的配对，
    重建不当会产出孤儿 ToolMessage / 残缺 tool_call，直接让 LLM 接口报错。
    """
    content = getattr(msg, "content", "")
    if isinstance(content, str) and len(content) > _MAX_MSG_CHARS:
        try:
            return msg.model_copy(update={"content": content[:_MAX_MSG_CHARS] + "\n…[已截断]"})
        except Exception:  # noqa: BLE001 - 拷贝失败就退回原消息，宁可长也不能破坏协议
            return msg
    return msg


def _current_run_messages(state: dict) -> list:
    """取「本轮」的消息：从最后一条 HumanMessage 起直到末尾。

    tool_call 必须看到本轮已取到的工具结果，才能决定「继续取数」还是「收尾作答」
    （ReAct 多跳）。但绝不能带上之前轮次的工具结果：实测第二轮会因为「历史里
    已经有 K 线数据」而不再发起调用，直接凭印象作答（答成「无法获取」）。
    只取本轮顺带把上下文锁在一轮之内，不会随会话轮数累积到几十万 token。
    """
    msgs = list(state.get("messages", []))
    for i in range(len(msgs) - 1, -1, -1):
        if isinstance(msgs[i], HumanMessage):
            return msgs[i:]
    return msgs


# 取数类工具：只有它们的结果才能支撑回答里的数字
_DATA_TOOLS = ("get_quote", "get_kline", "get_valuation", "get_financial_report", "get_money_flow")


def _got_data(state: dict) -> bool:
    """本轮是否已经取到取数类工具的数据。"""
    for item in (state.get("tool_results") or []):
        if item.get("tool") in _DATA_TOOLS and (item.get("result") or {}).get("ok"):
            return True
    return False


def _needs_data_fetch(state: dict) -> bool:
    """这一轮是否必须去取真实数据（用户要数据 + 不是概念问题）。

    概念问题（如「解释一下 PE 和 PB 的区别」）不需要真实数据，不要逼它去取数。
    """
    if _is_concept_question(state):
        return False
    return _asked_for_data(state)


def _pending_nudge(state: dict) -> str:
    """还没取到任何行情数据、且用户要的是数据时，追加一条强制取数的指令。

    「一轮只调了 symbol_search 就作答」是实测中最常见的失效姿势：模型靠历史消息
    里的旧数字拼出一份看似完整的答案，实际是过期数据；「一个工具都不调直接作答」
    同理（实测偶发：问行情却直接答「没取到数据」）。

    两个讲究：
    1. 只在第二轮起追加。第一轮系统提示已经写了「必须调用工具」，再补一遍
       等于给模型两条同义指令，实测会让它把提示里的词直接抄进函数名槽位
       （输出成 `function调用`），反而调不出工具；
    2. 措辞里避开「调用」二字，原因同上（模型会把它当成函数名）。
    """
    if _call_rounds(state) < 1:
        # 第一轮：不加提示，交给系统提示约束
        return ""
    results = state.get("tool_results") or []
    codes = []
    for item in results:
        name = item.get("tool")
        res = item.get("result") or {}
        if not res.get("ok"):
            continue
        if name == "symbol_search":
            for row in (res.get("data") or [])[:3]:
                if isinstance(row, dict) and row.get("code"):
                    codes.append(str(row["market"] or "") + str(row["code"]))
    if _got_data(state) or not _needs_data_fetch(state):
        return ""
    hint = "必须用工具取真实数据（行情用 get_quote，K 线用 get_kline，参数见工具列表）"
    if codes:
        hint = "已检索到代码：" + "、".join(codes) + "。" + hint
    return (
        hint + "；禁止直接作答，也禁止引用历史消息里的数字（那是上一轮的过期数据）。"
    )


# 有些模型（实测 DeepSeek-V3 经 SiliconFlow）有一定概率不填 tool_calls 字段，
# 而是把函数调用写进正文：
#     function
#     get_quote
#     ```json
#     {"code":"SH600519"}
#     ```
# 这种「文本形态的工具调用」在图里等于没调工具：取数不会发生，
# 回答最后变成「没取到数据」的兜底文案。这里把它还原成真正的 tool_call。
_TEXT_TOOL_RE = re.compile(r"function\s*\n?\s*([A-Za-z_][A-Za-z0-9_]*)")
_TEXT_TOOL_RE2 = re.compile(r"(?:^|\n)\s*([A-Za-z_][A-Za-z0-9_]*)\s*\n\s*\{")
# JSON 前缀里最后一个标识符（用来找工具名）
_TRAILING_IDENT_RE = re.compile(r"([A-Za-z_][A-Za-z0-9_]*)\s*$")


def _extract_json_object(text: str, start: int) -> str:
    """从 text 的 start 之后找出第一个配平的 JSON 对象（跳过字符串里的括号）。"""
    i = text.find("{", start)
    if i < 0:
        return ""
    depth = 0
    in_str = False
    esc = False
    for j in range(i, len(text)):
        c = text[j]
        if in_str:
            if esc:
                esc = False
            elif c == "\\":
                esc = True
            elif c == '"':
                in_str = False
            continue
        if c == '"':
            in_str = True
        elif c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
            if depth == 0:
                return text[i:j + 1]
    return ""


def _parse_text_tool_call(content: str):
    """从正文里解析出 (工具名, 参数)；解析不出返回 (None, None)。

    两种来源都认：
    1. 正文中写了工具名（如 `function\\nget_quote\\n{...}`）；
    2. 工具名缺失或被写坏（实测会写成 `function调用`），此时按参数签名反推
       （只带 code -> 行情，带 count/period -> K 线，带 query -> 搜索）。
    """
    if not content or "{" not in content:
        return None, None
    known = _tools_by_name()
    for raw, prefix in _iter_json_objects(content):
        try:
            args = json.loads(raw)
        except Exception:  # noqa: BLE001 - 解析失败就当没这回事
            continue
        if not isinstance(args, dict):
            continue
        name = _tool_name_in(prefix)
        if name in known:
            return name, args
        inferred = _infer_tool_by_args(args)
        if inferred:
            return inferred, args
    return None, None


def _iter_json_objects(content: str):
    """逐个产出内容里的 JSON 对象及其前面的文本。"""
    i = 0
    n = len(content)
    while i < n:
        if content[i] != "{":
            i += 1
            continue
        raw = _extract_json_object(content, i)
        if not raw:
            i += 1
            continue
        yield raw, content[:i]
        i += len(raw)


def _tool_name_in(prefix: str):
    """从 JSON 前面的文本里找工具名（找不到返回 None）。"""
    # 中间可能隔着 ```json 围栏，先去掉再找最后一个标识符
    stripped = re.sub(r"```(?:json)?", " ", prefix)
    m = _TRAILING_IDENT_RE.search(stripped)
    if m and m.group(1) in _tools_by_name():
        return m.group(1)
    return None


def _infer_tool_by_args(args: dict):
    """按参数签名反推工具名（拿不准就不猜，返回 None）。"""
    if "query" in args:
        return "symbol_search"
    if "code" in args and ("count" in args or "period" in args):
        return "get_kline"
    if "code" in args and len(args) == 1:
        return "get_quote"
    return None


def _normalize_tool_call(ai: AIMessage) -> AIMessage:
    """把「文本形态的工具调用」还原成结构化 tool_call。

    还原后原正文会被清空：正文里那段 function 文本对模型没有意义，
    留着反而可能让它继续用文本形态回答。
    """
    if getattr(ai, "tool_calls", None):
        return ai
    content = ai.content if isinstance(ai.content, str) else ""
    name, args = _parse_text_tool_call(content)
    if not name:
        return ai
    call_id = f"call_{name}_{uuid.uuid4().hex[:8]}"
    logger.info("restore text-form tool call: %s", name)
    return ai.model_copy(update={
        "content": "",
        "tool_calls": [{"name": name, "args": args, "id": call_id, "type": "tool_call"}],
    })


async def tool_call(state: dict) -> dict:
    # 只喂本轮消息（含本轮已取到的工具结果），逐条截断正文，防止上下文膨胀
    run_msgs = [_shrink(m) for m in _current_run_messages(state)]
    if not run_msgs:
        # 兜底：拿不到本轮消息时至少把问题本身交给模型
        run_msgs = [HumanMessage(content=state.get("query", "") or "")]
    msgs = [SystemMessage(content=_TOOL_SYSTEM)] + run_msgs
    nudge = _pending_nudge(state)
    if nudge:
        msgs.append(SystemMessage(content=nudge))
    llm = get_llm(streaming=False, temperature=0.2).bind_tools(_tools())
    try:
        ai = await llm.ainvoke(msgs)
    except Exception as e:  # noqa: BLE001
        if not is_context_overflow(e):
            raise
        # 上下文超长：这一轮不再调工具，直接进 synthesize（那里有降级文案），
        # 绝不让超长请求冒泡出去把整条对话变成空
        logger.warning("tool_call context overflow, skip tool round", exc_info=True)
        return {"messages": [AIMessage(content="")]}
    # 模型偶尔把工具调用写成正文而不是结构化字段，这里还原成真正的 tool_call
    return {"messages": [_normalize_tool_call(ai)]}


def _call_rounds(state: dict) -> int:
    """tool_call 节点已执行的轮数 = 本轮消息里 AIMessage 的条数。

    比「带 tool_calls 的 AIMessage 数」更准，两个原因：
    1. 模型偶尔一轮不调工具直接作答，那个 AIMessage 也要计入，
       否则「没调工具 -> 再来一轮」的重试会变成死循环；
    2. 只数本轮（从最后一个 HumanMessage 起），不把历史轮次算进来——
       否则多轮对话里轮次上限会被历史提前吃光。
    """
    return sum(1 for m in _current_run_messages(state) if isinstance(m, AIMessage))


def route_after_call(state: dict) -> str:
    """路由：该取数就去取，该取而没取就再给一次机会，最后才合成。

    - 有 tool_calls 且未超轮次上限 -> 执行工具
    - 模型一轮都没调工具，但用户要的是数据 -> 再回 tool_call 一次
      （实测偶发「问行情却直接答没取到数据」，靠重试逼它去取真实数据）
    - 其余（已达上限 / 概念问题 / 不需要数据）-> 合成
    """
    msgs = state.get("messages", [])
    last = msgs[-1] if msgs and isinstance(msgs[-1], AIMessage) else None
    if last is not None and getattr(last, "tool_calls", None):
        return "tool_execute" if _call_rounds(state) <= _MAX_TOOL_ROUNDS else "synthesize"
    if (
        _call_rounds(state) < _MAX_TOOL_ROUNDS
        and _needs_data_fetch(state)
        and not _got_data(state)
    ):
        return "tool_call"
    return "synthesize"


async def tool_execute(state: dict) -> dict:
    """并发执行 last AIMessage 的所有 tool_calls，收集结构化结果。"""
    msgs = state.get("messages", [])
    ai = msgs[-1] if msgs and isinstance(msgs[-1], AIMessage) else None
    if ai is None or not ai.tool_calls:
        return {}

    by_name = _tools_by_name()

    async def _run(tc):
        tool = by_name.get(tc["name"])
        args = tc.get("args", {}) or {}
        name = tc["name"]
        label = _TOOL_LABELS.get(name, name)
        if tool is None:
            return tc["id"], name, {"ok": False, "error": f"unknown tool {name}"}
        # 调用前：通知前端开始取数（emit 为同步函数，内部拿不到 writer 时静默降级）
        emit("tool_start", {"tool": name, "label": f"正在获取{label}"})
        t0 = time.monotonic()
        try:
            result = await tool.ainvoke(args)
        except Exception as e:  # noqa: BLE001 - 工具本身已降级，这里再兜一层
            result = {"ok": False, "error": f"{type(e).__name__}: {e}"}
        elapsed_ms = int((time.monotonic() - t0) * 1000)
        # 调用后：回传成功与否及耗时
        emit("tool_end", {"tool": name, "ok": bool(result.get("ok")), "ms": elapsed_ms})
        return tc["id"], name, result

    outcomes = await asyncio.gather(*[_run(tc) for tc in ai.tool_calls])

    tool_messages = []
    tool_results = []
    for tc_id, name, result in outcomes:
        tool_messages.append(ToolMessage(
            content=json.dumps(result, ensure_ascii=False, default=str),
            tool_call_id=tc_id,
            name=name,
        ))
        tool_results.append({"tool": name, "result": result})

    # 跨轮累加：tool_results 无 reducer（覆盖式写入），这里手动拼接已有结果，
    # 否则多跳循环里后一轮会覆盖前一轮，synthesize 就看不到早先轮次的工具数据
    prev = state.get("tool_results") or []
    return {"messages": tool_messages, "tool_results": prev + tool_results}


def _emit_structured(tool_results: list) -> None:
    """把行情/K线/估值/资金流/财报等结构化数据以独立 card/table 事件发射
    （不进入 token 流）。

    事件契约（与小程序 chat.js 的卡片工厂一一对应，改这里必须同步那边
    与 server/README.md 第三节）：
    - get_quote            -> card: quote_card（行情速览卡）
    - get_kline            -> table: kline_table（K 线数据）
    - symbol_search        -> table: symbol_list（股票搜索候选）
    - get_valuation        -> card: valuation_card（估值卡，daily_basic 行）
    - get_money_flow       -> card: fundflow_card（资金流向卡，moneyflow 行）
    - get_financial_report -> card: report_card（财报速览卡，三表合并行）

    行载荷按行发射：Tushare 返回的是数组，一轮问多只股票时一标的一卡，
    前端每张卡是一条独立消息。单工具最多发 _MAX_CARDS_PER_TOOL 张，防刷屏。
    """
    for item in tool_results:
        name = item.get("tool")
        result = item.get("result") or {}
        if not result.get("ok"):
            continue
        data = result.get("data")
        if name == "get_quote" and data:
            emit_card("quote_card", data)
        elif name == "get_kline" and data:
            emit_table("kline_table", data)
        elif name == "symbol_search" and data:
            emit_table("symbol_list", data)
        elif name == "get_valuation" and data:
            for row in _rows_of(data)[:_MAX_CARDS_PER_TOOL]:
                emit_card("valuation_card", row)
        elif name == "get_money_flow" and data:
            for row in _rows_of(data)[:_MAX_CARDS_PER_TOOL]:
                emit_card("fundflow_card", row)
        elif name == "get_financial_report" and data:
            emit_card("report_card", data)


def _rows_of(data):
    """把工具载荷归一成行列表：Tushare 给数组，组合工具给 dict。"""
    if isinstance(data, list):
        return [r for r in data if isinstance(r, dict)]
    return [data] if isinstance(data, dict) else []


# 命中这些词说明用户就是要数据；此时若一个数据都没取到，绝不能让模型自由发挥。
# 除行情价格类外，补上估值/财报/资金三类：它们对应各自的 Tushare 工具与卡片，
# 取不到数时同样必须走兜底文案，而不是让模型凭印象编数字
_DATA_KEYWORDS = (
    "k线", "K线", "行情", "价格", "涨跌", "走势", "收盘", "报价", "涨幅",
    "估值", "市盈率", "市净率", "市销率", "股息", "市值",
    "财报", "业绩", "营收", "净利润", "毛利", "ROE", "EPS",
    "资金", "主力", "北向", "南向", "龙虎榜", "流入", "流出", "融资融券",
)

def _no_data_fallback(state: dict, tool_results: list) -> str:
    """本轮没有取到任何行情数据、且用户就是要数据时，返回固定兜底文案。

    实测里最常见的坏答案：模型只搜到股票代码就作答，数字全部来自上一轮历史
    （行情是实时的，照抄即过期数据），甚至凭空编出别的标的。提示词规则拦不住，
    这里改成确定性兜底——宁可说「没取到」，也不给过期/编造的数字。
    """
    # 有任一取数类工具成功 -> 数据到手，交给模型作答
    if any(
        item.get("tool") in _DATA_TOOLS and (item.get("result") or {}).get("ok")
        for item in tool_results
    ):
        return ""
    # 模型调过取数类工具却全部失败 -> 用户问的就是数据，必然要兜底
    called_data_tool = any(item.get("tool") in _DATA_TOOLS for item in tool_results)
    if called_data_tool:
        reason = _failure_reason(tool_results)
        head = "抱歉，这一轮没有取到所需的数据，我无法给出你要的数字。"
        if reason:
            head += reason + "。"
        return head + "请稍后重试；也可以带上股票代码再问，例如「000568 最近10天日K」。"
    # 没调取数工具：可能是知识型问题（如「解释一下 PE 和 PB 的区别」），
    # 这类问题本来就不需要真实数据，兜底文案接管会把一条能答好的回答
    # 替换成无意义的取数失败提示（实测：PE/PB/PS 通俗解释被误伤）
    if _is_concept_question(state):
        return ""
    if _asked_for_data(state):
        return ("抱歉，这一轮没有取到所需的数据，我无法给出你要的数字。"
                "请稍后重试；也可以带上股票代码再问，例如「000568 最近10天日K」。")
    return ""


# 英文缩写单独匹配：改写步骤常把「估值水平」缩成「PE 和 PB」，
# 而中文关键词表里没有这些缩写，只看中文关键词会漏判（实测漏判后模型会自己
# 编一个失败原因，把 Tushare 的「频率超限(1次/分钟)」说成「每小时限1次」）。
_DATA_ABBR_RE = re.compile(r"(?<![A-Za-z])(PE|PB|PS|ROE|EPS|TTM)(?![A-Za-z])")


def _asked_for_data(state: dict) -> bool:
    """用户这一轮是不是在要数据（原始问题与改写后的问题都算）。"""
    text = ((state.get("query") or "") + " " + (state.get("rewritten") or "")).upper()
    if any(k.upper() in text for k in _DATA_KEYWORDS):
        return True
    return bool(_DATA_ABBR_RE.search(text))


# 概念性问题：问的是「是什么 / 怎么算 / 有什么区别」，本身不需要真实数据。
# 这类问题不能被「没取到数据」的兜底文案接管——否则会把一条本来能答好的
# 知识型回答（实测：PE/PB/PS 通俗解释）换成无意义的取数失败提示。
_CONCEPT_KEYWORDS = (
    "解释", "含义", "意思", "区别", "不同", "为什么", "怎么算", "如何算",
    "怎么用", "如何用", "是什么", "什么是", "通俗", "简单说", "怎么理解",
    "如何理解", "讲讲", "介绍", "科普", "定义", "对比", "有什么区别",
)


def _is_concept_question(state: dict) -> bool:
    """这一轮是不是纯概念/知识型问题（不需要真实行情数据）。"""
    text = (state.get("query") or "") + " " + (state.get("rewritten") or "")
    return any(k in text for k in _CONCEPT_KEYWORDS)


def _failure_reason(tool_results: list) -> str:
    """从失败的工具结果里提炼一句准确的失败原因。

    直接把 Tushare 原文丢给模型，它会把「频率超限(1次/分钟)」转述成
    「每小时限1次」这种假细节，所以这里归一化成一句准确的中文说明，
    由兜底文案与提示词共用。
    """
    for item in tool_results:
        res = item.get("result") or {}
        if res.get("ok"):
            continue
        err = str(res.get("error") or "")
        if not err:
            continue
        if "频率超限" in err or "访问频率" in err:
            return "数据接口访问频率超限（Tushare 每个接口每分钟 1 次）"
        if "访问权限" in err or "没有接口" in err or "没有访问该接口" in err:
            return "当前账号没有该数据接口的访问权限（Tushare 积分/权限不足）"
        if "timeout" in err.lower():
            return "数据接口响应超时"
        return err[:80]
    return ""


# 上下文超长时的最终兜底：宁可明确告知，也不要留一条空回答
_OVERFLOW_REPLY = (
    "抱歉，这次的数据量超出了模型能一次消化的上限，我没法给出完整回答。"
    "可以缩小范围再问一次，例如「贵州茅台最近 10 天日K」「宁德时代最新一期财报」。"
)


def _fit_context(messages: list) -> list:
    """按 token 预算裁剪上下文：先丢历史，再截提示词。

    上游模型上下文 163840，超了直接 400（整条回答为空）。这里留足余量提前裁，
    避免再出现「问一句、答一句空」的故障。
    """
    if count_message_tokens(messages) <= MAX_INPUT_TOKENS:
        return messages
    out = list(messages)
    # 1) 先丢最老的历史（index 1 起是历史，末尾是本轮问题，两者都保留）
    while len(out) > 2 and count_message_tokens(out) > MAX_INPUT_TOKENS:
        del out[1]
    # 2) 还超就截提示词正文（工具结果段在最前面，截尾保留取数结论）
    if count_message_tokens(out) > MAX_INPUT_TOKENS:
        sys_msg = out[0]
        text = sys_msg.content if isinstance(sys_msg.content, str) else str(sys_msg.content)
        out[0] = SystemMessage(content=text[:max(2000, len(text) // 2)] + "\n…[上下文已裁剪]")
    return out


async def synthesize(state: dict) -> dict:
    tool_results = state.get("tool_results", []) or []
    fallback = _no_data_fallback(state, tool_results)
    if fallback:
        # 不走 LLM：直接把兜底文案当成本轮回答。不另外 emit token——
        # 返回的 AIMessage 本身会被 messages 流投递成一次 token，再 emit 就重复一遍
        return {"messages": [AIMessage(content=fallback)]}
    # 无工具结果（模型直接回答）时也给一个空上下文
    tr_text = json.dumps(tool_results, ensure_ascii=False, default=str) if tool_results else "（无工具数据）"
    # 同样防超限：用户要长区间 K 线时这段 JSON 会很大，截尾保留取数结论足够作答
    if len(tr_text) > _MAX_TOOL_CHARS:
        tr_text = tr_text[:_MAX_TOOL_CHARS] + "\n…[工具结果已截断]"
    # 失败原因单独点名：把 Tushare 原文丢给模型，它会把「频率超限(1次/分钟)」
    # 转述成「每小时限1次」这种假细节，这里给一句准确的中文说明让它照着转述
    reason = _failure_reason(tool_results)
    if reason:
        tr_text = f"【数据获取失败说明】{reason}\n{tr_text}"
    query = state.get("rewritten") or state.get("query", "") or ""
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    prompt = render("mcp.xml", now=now, query=query, tool_results=tr_text)

    # 结构化数据走独立事件
    _emit_structured(tool_results)

    # 带上最近对话历史，让回答能衔接上下文
    messages = [SystemMessage(content=prompt)]
    messages.extend(_recent_history(state))
    messages.append(HumanMessage(content=state.get("query", "") or query))

    # 上下文预算：超了先裁剪再发，绝不把超长请求丢给上游
    messages = _fit_context(messages)

    llm = get_llm(streaming=True, temperature=0.4)
    try:
        ai = await llm.ainvoke(messages)
    except Exception as e:  # noqa: BLE001
        if not is_context_overflow(e):
            raise
        # 上游仍报超长：降级成一条能用的回答，绝不让整条对话只剩 error 事件
        logger.warning("synthesize context overflow, fallback to safe reply", exc_info=True)
        return {"messages": [AIMessage(content=_OVERFLOW_REPLY)]}
    return {"messages": [ai]}


def build_mcp_subgraph():
    g = StateGraph(AgentState)
    g.add_node("tool_call", tool_call)
    g.add_node("tool_execute", tool_execute)
    g.add_node("synthesize", synthesize)

    g.add_edge(START, "tool_call")
    g.add_conditional_edges("tool_call", route_after_call, {
        "tool_execute": "tool_execute",
        # 回到 tool_call：模型一轮没调工具但用户要的是数据时，再给一次机会
        # （少了这一条会直接 KeyError: 'tool_call'，整轮对话变成 error）
        "tool_call": "tool_call",
        "synthesize": "synthesize",
    })
    # 执行完工具回到 tool_call：让模型基于工具结果决定「继续调」还是「收尾作答」（多跳 ReAct）
    g.add_edge("tool_execute", "tool_call")
    g.add_edge("synthesize", END)
    return g.compile()
