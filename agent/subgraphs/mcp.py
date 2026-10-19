r"""MCP 行情工具子图。

流程: tool_call --(有 tool_calls?)--> tool_execute --> synthesize
                 \--(无)----------------------------> synthesize

- tool_call: LLM 绑定工具，决定是否/如何调用工具。
- tool_execute: 并发执行工具（每个工具已内置 8s 超时 + 重试 + 失败降级），
  每个工具调用前后发射 tool_start / tool_end 事件，
  产出 ToolMessage 与结构化 tool_results。
- synthesize: 用 prompts/mcp.xml + tool_results 流式生成最终回答，
  并把行情/K线结构化数据以 card/table 事件独立发射。
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
    "2. 用户只给了名称/拼音时，先用 symbol_search 拿到代码，再查行情或K线。\n"
    "3. 一次性把需要的工具都调用齐（可并行多个 tool_call）。\n"
    "4. 拿到足够数据后停止调用工具，直接回答。"
)

# 工具名 -> 中文标签（tool_start 事件用，前端展示“正在获取xx”）
_TOOL_LABELS = {
    "get_quote": "实时行情",
    "get_kline": "K线数据",
    "symbol_search": "股票搜索",
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


async def tool_call(state: dict) -> dict:
    llm = get_llm(streaming=False, temperature=0.2).bind_tools(_tools())
    msgs = [SystemMessage(content=_TOOL_SYSTEM)] + list(state.get("messages", []))
    ai = await llm.ainvoke(msgs)
    return {"messages": [ai]}


def route_after_call(state: dict) -> str:
    """有 tool_calls 就去执行，否则直接合成。"""
    msgs = state.get("messages", [])
    if msgs and isinstance(msgs[-1], AIMessage) and getattr(msgs[-1], "tool_calls", None):
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

    return {"messages": tool_messages, "tool_results": tool_results}


def _emit_structured(tool_results: list) -> None:
    """把行情/K线/搜索结果以独立 card/table 事件发射（不进入 token 流）。"""
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


async def synthesize(state: dict) -> dict:
    tool_results = state.get("tool_results", []) or []
    # 无工具结果（模型直接回答）时也给一个空上下文
    tr_text = json.dumps(tool_results, ensure_ascii=False, default=str) if tool_results else "（无工具数据）"
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
    g.add_edge("tool_execute", "synthesize")
    g.add_edge("synthesize", END)
    return g.compile()
