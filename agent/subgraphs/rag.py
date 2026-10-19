r"""RAG 混合检索子图。

流程: hybrid_retrieve -> sufficiency_check --(充分)--> generate_with_citations -> END
                                          \--(不足)--> escalate(设 intent=mcp) -> END

sufficiency_check: 最高分 < RAG_THRESHOLD 或有效片段 < 2 -> escalate，
把 intent 改成 "mcp"，由主图条件边重路由到行情工具子图。
generate_with_citations: 用 prompts/rag.xml 生成带 [n] 引用的回答，并发射 citations 事件。
"""
from datetime import datetime

from langchain_core.messages import SystemMessage
from langgraph.graph import StateGraph, START, END

from llm import get_llm
from state import AgentState
from config import RAG_THRESHOLD
from retriever import build_retriever
from prompts.parse import render
from subgraphs import emit

_MIN_CHUNKS = 2
_retriever = None


def _get_retriever():
    global _retriever
    if _retriever is None:
        _retriever = build_retriever(k=6)
    return _retriever


async def hybrid_retrieve(state: dict) -> dict:
    query = state.get("rewritten") or state.get("query", "") or ""
    try:
        docs = await _get_retriever().ainvoke(query)
    except Exception as e:  # noqa: BLE001 - 检索失败按“不足”处理，交给 escalate
        return {"retrieved": [], "error": f"retrieve_failed: {e}"}
    retrieved = [{
        "content": d.page_content,
        "title": d.metadata.get("title", ""),
        "source_path": d.metadata.get("source_path", ""),
        "score": d.metadata.get("score", 0.0),
    } for d in docs]
    return {"retrieved": retrieved}


def sufficiency_check(state: dict) -> dict:
    """判断检索是否充分。不足则升级 intent=mcp 让主图重路由。"""
    retrieved = state.get("retrieved", []) or []
    top = max((r.get("score", 0.0) for r in retrieved), default=0.0)
    if len(retrieved) < _MIN_CHUNKS or top < RAG_THRESHOLD:
        return {"intent": "mcp"}
    return {}


def route_after_check(state: dict) -> str:
    # escalate 后 intent 被改成 mcp
    return "escalate" if state.get("intent") == "mcp" else "generate"


async def escalate(state: dict) -> dict:
    """占位节点：intent 已在 sufficiency_check 改为 mcp，直接结束子图。"""
    return {}


async def generate_with_citations(state: dict) -> dict:
    retrieved = state.get("retrieved", []) or []
    query = state.get("rewritten") or state.get("query", "") or ""
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")

    # 给每个片段编号，供 [n] 引用
    ctx_lines = []
    citations = []
    for i, r in enumerate(retrieved, 1):
        ctx_lines.append(f"[{i}] 《{r.get('title','')}》\n{r.get('content','')}")
        citations.append({
            "index": i,
            "title": r.get("title", ""),
            "source_path": r.get("source_path", ""),
            "score": r.get("score", 0.0),
        })
    contexts = "\n\n".join(ctx_lines) if ctx_lines else "（无检索资料）"

    prompt = render("rag.xml", now=now, query=query, contexts=contexts)
    # 引用列表走独立 custom 事件，不混入 token 流
    emit("citations", citations)

    llm = get_llm(streaming=True, temperature=0.3)
    ai = await llm.ainvoke([SystemMessage(content=prompt)])
    return {"messages": [ai], "citations": citations}


def build_rag_subgraph():
    g = StateGraph(AgentState)
    g.add_node("hybrid_retrieve", hybrid_retrieve)
    g.add_node("sufficiency_check", sufficiency_check)
    g.add_node("escalate", escalate)
    g.add_node("generate", generate_with_citations)

    g.add_edge(START, "hybrid_retrieve")
    g.add_edge("hybrid_retrieve", "sufficiency_check")
    g.add_conditional_edges("sufficiency_check", route_after_check, {
        "escalate": "escalate",
        "generate": "generate",
    })
    g.add_edge("escalate", END)
    g.add_edge("generate", END)
    return g.compile()
