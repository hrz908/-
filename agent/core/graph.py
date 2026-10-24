"""主图组装。

START -> guardrail -> [error? clarify : rewrite -> classify -> route_by_intent]
route_by_intent -> {stock, chat, off_topic, clarify}
  - stock     -> rag 漏斗子图
                 命中 -> post
                 未命中 -> (intent 改写 stock_agent) -> stock_agent 子图 -> post
  - chat      -> post
  - off_topic -> refuse -> post
  - clarify   -> post
post -> END

route_by_intent 是纯函数，仅读取 state["intent"]，异常/未知一律回退 clarify。

意图口径：
- stock      : 一切股票问题，统一先进 RAG 漏斗，命中带引用直接作答；
- stock_agent: 内部意图，rag 两级判定（阈值 + LLM 判读）未命中时写入，
               由 route_after_rag 重路由到炒股 agent 子图（MCP 工具取数）；
- chat       : 闲聊，直接陪聊，不挂工具；
- off_topic  : 与股票无关的正经话题，固定话术婉拒（nodes/refuse.py）；
- clarify    : 含糊 / 低置信度 / 护栏拦截，反问或安全降级。
"""
from typing import Literal

from langgraph.graph import StateGraph, START, END

from core.state import AgentState
from nodes import guardrail, rewrite, classify, clarify, refuse, post
from subgraphs.stock import build_stock_subgraph
from subgraphs.rag import build_rag_subgraph
from subgraphs.chat import build_chat_subgraph

# classify 可产出的意图值（stock_agent 由 rag 两级判定写入，不来自 classify）
_CLASSIFY_INTENTS = {'stock', 'chat', 'off_topic', 'clarify'}


def route_after_guardrail(state: dict) -> Literal['clarify', 'rewrite']:
    """护栏命中 -> 降级澄清；否则进入改写。"""
    return 'clarify' if state.get('error') else 'rewrite'


def route_by_intent(state: dict) -> Literal['stock', 'chat', 'off_topic', 'clarify']:
    """纯函数路由：按 intent 分派；未知一律回退 clarify。"""
    intent = state.get('intent', '')
    return intent if intent in _CLASSIFY_INTENTS else 'clarify'


def route_after_rag(state: dict) -> Literal['stock_agent', 'post']:
    """RAG 两级判定未命中会把 intent 改成 stock_agent，此时重路由到炒股 agent 子图。"""
    return 'stock_agent' if state.get('intent') == 'stock_agent' else 'post'


def build_graph(checkpointer=None):
    g = StateGraph(AgentState)

    # 节点
    g.add_node('guardrail', guardrail)
    g.add_node('rewrite', rewrite)
    g.add_node('classify', classify)
    g.add_node('clarify', clarify)
    g.add_node('refuse', refuse)
    g.add_node('post', post)
    g.add_node('stock_agent', build_stock_subgraph())
    g.add_node('rag', build_rag_subgraph())
    g.add_node('chat', build_chat_subgraph())

    # 边
    g.add_edge(START, 'guardrail')
    g.add_conditional_edges('guardrail', route_after_guardrail, {
        'clarify': 'clarify',
        'rewrite': 'rewrite',
    })
    g.add_edge('rewrite', 'classify')
    g.add_conditional_edges('classify', route_by_intent, {
        'stock': 'rag',
        'chat': 'chat',
        'off_topic': 'refuse',
        'clarify': 'clarify',
    })
    g.add_edge('stock_agent', 'post')
    g.add_conditional_edges('rag', route_after_rag, {
        'stock_agent': 'stock_agent',
        'post': 'post',
    })
    g.add_edge('chat', 'post')
    g.add_edge('refuse', 'post')
    g.add_edge('clarify', 'post')
    g.add_edge('post', END)

    return g.compile(checkpointer=checkpointer)


_graph = None


async def get_graph():
    """惰性构建带 checkpointer 的全局图单例。"""
    global _graph
    if _graph is None:
        from core.checkpointer import get_checkpointer
        cp = await get_checkpointer()
        _graph = build_graph(cp)
    return _graph
