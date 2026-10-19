"""分类节点：意图路由。

用 small_llm + prompts/router.xml 输出 XML，parse_route 解析出 intent/confidence。
confidence < 0.6 强制降级为 clarify（宁可反问，不可乱答）。
解析失败(parse_route 已兜底)一律 clarify。
"""
from datetime import datetime

from langchain_core.messages import HumanMessage

from llm import get_small_llm
from prompts.parse import render, parse_route
from nodes.rewrite import _format_history

_CONF_MIN = 0.6


async def classify(state: dict) -> dict:
    # 护栏已拦截时不再分类，直接透传 clarify
    if state.get("error"):
        return {"intent": "clarify", "confidence": 0.0}

    query = state.get("rewritten") or state.get("query", "") or ""
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    history = _format_history(state.get("messages", []))

    prompt = render("router.xml", now=now, history=history, query=query)
    try:
        llm = get_small_llm(streaming=False, temperature=0.0)
        resp = await llm.ainvoke([HumanMessage(content=prompt)])
        route = parse_route(resp.content or "")
    except Exception:  # noqa: BLE001 - 分类失败降级 clarify
        route = {"intent": "clarify", "confidence": 0.0, "reason": "classify_error"}

    intent = route.get("intent", "clarify")
    confidence = float(route.get("confidence", 0.0) or 0.0)
    if intent not in {"mcp", "rag", "chat", "clarify"}:
        intent, confidence = "clarify", 0.0
    # 低置信度强制澄清
    if confidence < _CONF_MIN:
        intent = "clarify"

    return {"intent": intent, "confidence": confidence}
