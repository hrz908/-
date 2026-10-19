"""纯对话子图：单节点流式生成，无工具、无检索。

用 prompts/chat.xml 作为 system，携带最近对话历史，直接流式产出回答。
"""
from datetime import datetime

from langchain_core.messages import SystemMessage
from langgraph.graph import StateGraph, START, END

from llm import get_llm
from state import AgentState
from prompts.parse import render


async def llm_stream(state: dict) -> dict:
    query = state.get("rewritten") or state.get("query", "") or ""
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    system = render("chat.xml", now=now, query=query)

    # 保留最近历史以维持连贯，但当前问题已通过 query 明确，避免重复
    history = list(state.get("messages", []))[-10:]
    msgs = [SystemMessage(content=system)] + history

    llm = get_llm(streaming=True, temperature=0.7)
    ai = await llm.ainvoke(msgs)
    return {"messages": [ai]}


def build_chat_subgraph():
    g = StateGraph(AgentState)
    g.add_node("llm_stream", llm_stream)
    g.add_edge(START, "llm_stream")
    g.add_edge("llm_stream", END)
    return g.compile()
