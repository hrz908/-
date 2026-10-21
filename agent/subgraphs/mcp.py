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
import time
from datetime import datetime

from langchain_core.messages import SystemMessage, HumanMessage, AIMessage, ToolMessage
from langgraph.graph import StateGraph, START, END

from llm import get_llm
from state import AgentState
from tools import get_tools
from prompts.parse import render
from subgraphs import emit, emit_card, emit_table

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
    """取最近 limit 条对话历史（排除末尾的当前 query）。

    裁剪开头的 ToolMessage 与带 tool_calls 的 AIMessage，
    避免孤儿 ToolMessage / 残缺 tool_call 对导致 LLM 接口报错。
    """
    history = list(state.get("messages", []))[-(limit + 1):-1]
    while history and (
        isinstance(history[0], ToolMessage)
        or (isinstance(history[0], AIMessage) and getattr(history[0], "tool_calls", None))
    ):
        history.pop(0)
    return history


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


def _pending_nudge(state: dict) -> str:
    """已搜到代码但还没取到任何行情数据时，追加一条强制继续取数的指令。

    「一轮只调了 symbol_search 就作答」是实测中最常见的失效姿势：模型靠历史消息
    里的旧数字拼出一份看似完整的答案，实际是过期数据。这里明确点名代码并要求
    继续调工具，比单纯在 system 里写规则有效得多。
    """
    results = state.get("tool_results") or []
    codes = []
    got_data = False
    for item in results:
        name = item.get("tool")
        res = item.get("result") or {}
        if not res.get("ok"):
            continue
        if name in _DATA_TOOLS:
            got_data = True
        if name == "symbol_search":
            for row in (res.get("data") or [])[:3]:
                if isinstance(row, dict) and row.get("code"):
                    codes.append(str(row["market"] or "") + str(row["code"]))
    if got_data or not codes:
        return ""
    return (
        "已检索到代码：" + "、".join(codes) + "。"
        "本轮还没有取到任何行情/K线数据，必须继续调用 get_kline 或 get_quote 取真实数据；"
        "禁止直接作答，也禁止引用历史消息里的数字（那是上一轮的过期数据）。"
    )


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
    ai = await llm.ainvoke(msgs)
    return {"messages": [ai]}


def _tool_rounds(state: dict) -> int:
    """已发生的工具执行轮数 = 消息里带 tool_calls 的 AIMessage 数量。"""
    return sum(
        1 for m in state.get("messages", [])
        if isinstance(m, AIMessage) and getattr(m, "tool_calls", None)
    )


def route_after_call(state: dict) -> str:
    """有 tool_calls 且未超轮次上限 -> 执行；否则（无工具 / 已达上限）-> 合成。"""
    msgs = state.get("messages", [])
    if msgs and isinstance(msgs[-1], AIMessage) and getattr(msgs[-1], "tool_calls", None):
        if _tool_rounds(state) <= _MAX_TOOL_ROUNDS:
            return "tool_execute"
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

_NO_DATA_REPLY = (
    "抱歉，这一轮没有取到行情数据（工具没返回有效结果），我无法给出你要的数字。"
    "请稍后再试一次；也可以带上代码再问，例如「000568 最近10天日K」。"
)


def _no_data_fallback(state: dict, tool_results: list) -> str:
    """本轮没有取到任何行情数据、且用户就是要数据时，返回固定兜底文案。

    实测里最常见的坏答案：模型只搜到股票代码就作答，数字全部来自上一轮历史
    （行情是实时的，照抄即过期数据），甚至凭空编出别的标的。提示词规则拦不住，
    这里改成确定性兜底——宁可说「没取到」，也不给过期/编造的数字。
    """
    has_data = any(
        item.get("tool") in _DATA_TOOLS and (item.get("result") or {}).get("ok")
        for item in tool_results
    )
    if has_data:
        return ""
    query = (state.get("rewritten") or state.get("query") or "").upper()
    if not any(k.upper() in query for k in _DATA_KEYWORDS):
        return ""
    return _NO_DATA_REPLY


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
    query = state.get("rewritten") or state.get("query", "") or ""
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    prompt = render("mcp.xml", now=now, query=query, tool_results=tr_text)

    # 结构化数据走独立事件
    _emit_structured(tool_results)

    # 带上最近对话历史，让回答能衔接上下文
    messages = [SystemMessage(content=prompt)]
    messages.extend(_recent_history(state))
    messages.append(HumanMessage(content=state.get("query", "") or query))

    llm = get_llm(streaming=True, temperature=0.4)
    ai = await llm.ainvoke(messages)
    return {"messages": [ai]}


def build_mcp_subgraph():
    g = StateGraph(AgentState)
    g.add_node("tool_call", tool_call)
    g.add_node("tool_execute", tool_execute)
    g.add_node("synthesize", synthesize)

    g.add_edge(START, "tool_call")
    g.add_conditional_edges("tool_call", route_after_call, {
        "tool_execute": "tool_execute",
        "synthesize": "synthesize",
    })
    # 执行完工具回到 tool_call：让模型基于工具结果决定「继续调」还是「收尾作答」（多跳 ReAct）
    g.add_edge("tool_execute", "tool_call")
    g.add_edge("synthesize", END)
    return g.compile()
