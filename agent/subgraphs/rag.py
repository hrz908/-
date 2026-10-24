r"""RAG 混合检索子图（RAG 漏斗）。

流程: hybrid_retrieve -> sufficiency_check --(命中)--> generate_with_citations -> END
                                         \--(不足)--> escalate(设 intent=stock_agent) -> END

sufficiency_check 两级判定：阈值闸（最高相似度 < RAG_THRESHOLD 或有效片段 < 2 即未命中）；
过阈值后再交小模型判读「能否回答」（prompts/rag_judge.xml），避免主题相似但答非所问
（问实时股价却检索到投教文章）。未命中时置 intent=stock_agent，由主图重路由到炒股 agent 子图。
generate_with_citations: 用 prompts/rag.xml 生成带 [n] 引用的回答，并发 sources 事件
（{"docs":[{title, chunk_id, score}]}，与前端和 Go 事件协议一致）。
"""
import logging
from datetime import datetime
from typing import Literal

from langchain_core.messages import SystemMessage, HumanMessage, AIMessage
from langgraph.graph import StateGraph, START, END
from pydantic import BaseModel, Field

from core.llm import get_llm, get_small_llm
from core.state import AgentState, recent_turns
from config import RAG_THRESHOLD
from rag.retriever import build_retriever
from core.template import render
from subgraphs import emit

logger = logging.getLogger("stocka.agent")

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
        "chunk_id": str(d.metadata.get("id", "")),
        "score": d.metadata.get("score", 0.0),
        "similarity": d.metadata.get("similarity", 0.0),
    } for d in docs]
    return {"retrieved": retrieved}


class _Judge(BaseModel):
    """RAG 命中判读的结构化输出契约（与 prompts/rag_judge.xml 规则对应）。"""

    can_answer: bool = Field(description="检索资料能否直接回答用户问题")
    reason: str = Field(default="", description="一句话理由")


# 判读上下文上限（字符）：只取片段开头，够判主题与信息量即可
_JUDGE_MAX_CHARS = 4000


async def _judge_can_answer(query: str, retrieved: list) -> bool:
    """LLM 判读检索片段能否回答用户问题。

    判官缺席（模型异常 / 结构化输出失败）时按「能」处理，退回纯阈值口径：
    宁可沿用原行为出引用回答，也不因判读故障把所有问题都甩给取数 agent。
    """
    sep = chr(10) * 2
    passages = sep.join(
        "[" + str(i) + "] 《" + str(r.get("title", "")) + "》"
        + str(r.get("content", ""))[:300]
        for i, r in enumerate(retrieved, 1)
    )[:_JUDGE_MAX_CHARS]
    try:
        small = get_small_llm(streaming=False, temperature=0.0)
        verdict = await small.with_structured_output(_Judge).ainvoke([
            HumanMessage(content=render("rag_judge.xml", query=query, passages=passages))
        ])
        return bool(verdict.can_answer)
    except Exception as e:  # noqa: BLE001 - 判读失败不阻断，退回纯阈值口径
        logger.warning(
            "rag judge failed (%s: %s), fallback to threshold-only",
            type(e).__name__, e,
        )
        return True


async def sufficiency_check(state: dict) -> dict:
    """判断检索是否充分，不足则置 intent=stock_agent 让主图重路由。

    两级判定：阈值闸（便宜）——最高相似度 < RAG_THRESHOLD 或有效片段 < 2 即未命中；
    LLM 判读（准）——过阈值的片段交小模型判「能否回答」，判读异常按命中处理、不阻断链路。
    用 similarity 而非 RRF 融合分 score 判定：score 是排名函数（量级 ~1/(60+rk)，最大约 0.033），
    无法衡量相关性；similarity 才与 RAG_THRESHOLD 同尺度（取值区间随 embedding 模型变化）。
    """
    retrieved = state.get("retrieved", []) or []
    top_sim = max((r.get("similarity", 0.0) for r in retrieved), default=0.0)
    if len(retrieved) < _MIN_CHUNKS or top_sim < RAG_THRESHOLD:
        return {"intent": "stock_agent"}
    query = state.get("rewritten") or state.get("query", "") or ""
    if await _judge_can_answer(query, retrieved):
        return {}
    return {"intent": "stock_agent"}


def route_after_check(state: dict) -> Literal["escalate", "generate"]:
    # 两级判定未命中后 intent 被改成 stock_agent
    return "escalate" if state.get("intent") == "stock_agent" else "generate"


async def escalate(state: dict) -> dict:
    """占位节点：intent 已在 sufficiency_check 改为 stock_agent，直接结束子图。"""
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
            "chunk_id": r.get("chunk_id", ""),
            "score": r.get("score", 0.0),
        })
    contexts = "\n\n".join(ctx_lines) if ctx_lines else "（无检索资料）"

    prompt = render("rag.xml", now=now, query=query, contexts=contexts)
    # 引用来源走独立 custom 事件（sources），不混入 token 流
    emit("sources", {"docs": [{
        "title": c.get("title", ""),
        "chunk_id": c.get("chunk_id", ""),
        "score": c.get("score", 0.0),
    } for c in citations]})

    # 带上最近历史让回答衔接上下文（复用 recent_turns 统一口径，跳过当前问题避免重复）
    messages = [SystemMessage(content=prompt)]
    messages.extend(recent_turns(state, 6, query=query))
    messages.append(HumanMessage(content=state.get("query", "") or query))

    llm = get_llm(streaming=True, temperature=0.3)
    # 流式生成：逐块 emit token，同时累加成完整 AIMessage 写回 state
    parts = []
    async for chunk in llm.astream(messages):
        if isinstance(chunk.content, str) and chunk.content:
            emit("token", {"delta": chunk.content})
        parts.append(chunk)
    ai = parts[0] if parts else None
    for p in parts[1:]:
        ai = ai + p
    return {"messages": [ai if ai is not None else AIMessage(content="")], "citations": citations}


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
