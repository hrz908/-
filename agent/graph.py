"""主图组装。

START -> guardrail -> [error? clarify : rewrite -> classify -> route_by_intent]
route_by_intent -> {mcp, rag, chat, clarify}
  - mcp   -> post
  - rag   -> [escalate(intent=mcp)? mcp : post]   # 检索不足时重路由到行情子图
  - chat  -> post
  - clarify -> post
post -> END

route_by_intent 是纯函数，仅读取 state["intent"]，异常/未知一律回退 clarify。
"""
from langgraph.graph import StateGraph, START, END

from state import AgentState
from nodes import guardrail, rewrite, classify, clarify, post
from subgraphs.mcp import build_mcp_subgraph
from subgraphs.rag import build_rag_subgraph
from subgraphs.chat import build_chat_subgraph

_VALID_INTENTS = {"mcp", "rag", "chat", "clarify"}


def route_after_guardrail(state: dict) -> str:
    """护栏命中 -> 降级澄清；否则进入改写。"""
    return "clarify" if state.get("error") else "rewrite"


def route_by_intent(state: dict) -> str:
    """纯函数路由：按 intent 分派到对应子图/节点。"""
    intent = state.get("intent", "")
    return intent if intent in _VALID_INTENTS else "clarify"


def route_after_rag(state: dict) -> str:
    """RAG 检索不足会把 intent 改成 mcp，此时重路由到行情子图。"""
    return "mcp" if state.get("intent") == "mcp" else "post"


def build_graph(checkpointer=None):
    g = StateGraph(AgentState)

    # 节点
    g.add_node("guardrail", guardrail)
    g.add_node("rewrite", rewrite)
    g.add_node("classify", classify)
    g.add_node("clarify", clarify)
    g.add_node("post", post)
    g.add_node("mcp", build_mcp_subgraph())
    g.add_node("rag", build_rag_subgraph())
    g.add_node("chat", build_chat_subgraph())

    # 边
    g.add_edge(START, "guardrail")
    g.add_conditional_edges("guardrail", route_after_guardrail, {
        "clarify": "clarify",
        "rewrite": "rewrite",
    })
    g.add_edge("rewrite", "classify")
    g.add_conditional_edges("classify", route_by_intent, {
        "mcp": "mcp",
        "rag": "rag",
        "chat": "chat",
        "clarify": "clarify",
    })
    g.add_edge("mcp", "post")
    g.add_conditional_edges("rag", route_after_rag, {
        "mcp": "mcp",
        "post": "post",
    })
    g.add_edge("chat", "post")
    g.add_edge("clarify", "post")
    g.add_edge("post", END)

    return g.compile(checkpointer=checkpointer)


_graph = None


async def get_graph():
    """惰性构建带 checkpointer 的全局图单例。"""
    global _graph
    if _graph is None:
        from checkpointer import get_checkpointer
        cp = await get_checkpointer()
        _graph = build_graph(cp)
    return _graph
