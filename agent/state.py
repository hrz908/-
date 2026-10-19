"""图状态定义。

AgentState 贯穿整张主图与子图。使用 add_messages reducer 让 messages 自动累加，
其余字段为覆盖式写入。所有节点只写自己负责的字段，避免相互踩踏。
"""
from typing import TypedDict, Annotated, Optional

from langgraph.graph.message import add_messages


class AgentState(TypedDict, total=False):
    # 对话消息（含 human / ai / tool），由 add_messages 累加
    messages: Annotated[list, add_messages]
    user_id: str
    thread_id: str
    # 当前轮用户原始输入
    query: str
    # 改写后的独立问题（指代消解结果）
    rewritten: str
    # 意图路由：mcp / rag / chat / clarify
    intent: str
    confidence: float
    # 工具调用结果（MCP 子图写入）
    tool_results: list
    # RAG 检索片段
    retrieved: list
    # 结构化卡片事件（quote_card 等）
    cards: list
    # 引用来源
    citations: list
    # 错误信息：非空时条件边路由到降级节点
    error: Optional[str]
    # token 用量统计
    usage: dict


def initial_state(user_id: str, thread_id: str, query: str) -> AgentState:
    """构造一轮请求的初始状态，给所有可变字段填安全默认值。"""
    return AgentState(
        messages=[],
        user_id=user_id,
        thread_id=thread_id,
        query=query,
        rewritten="",
        intent="",
        confidence=0.0,
        tool_results=[],
        retrieved=[],
        cards=[],
        citations=[],
        error=None,
        usage={},
    )
