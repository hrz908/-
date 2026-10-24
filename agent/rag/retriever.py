"""混合检索器：pgvector 向量召回 + PostgreSQL 全文检索，RRF(k=60) 融合，可选重排。"""
import logging
import re
import time
from typing import List, Optional

from langchain_core.callbacks import CallbackManagerForRetrieverRun, AsyncCallbackManagerForRetrieverRun
from langchain_core.documents import Document
from langchain_core.retrievers import BaseRetriever
from langchain_openai import OpenAIEmbeddings

import psycopg

from config import (
    DATABASE_URL,
    OPENAI_API_BASE,
    OPENAI_API_KEY,
    ONLINE_EMBED_MODEL,
    EMBED_DIM,
    RERANK_ENABLED,
    RERANK_CANDIDATES,
    RERANK_THRESHOLD,
    RAG_RERANK_FILTER,
)
from rag.reranker import rerank_scores

logger = logging.getLogger("stocka.agent")

# 混合检索 SQL（RRF k=60）——保留 $n 占位形式作为规范文档，
# 执行时经 _to_psycopg 转成 psycopg 的 %s 位置参数。
# 注意：$1（查询向量）在 vec CTE 里出现 3 次（dist / 窗口排序 / 外层排序），
# $2 为全文检索 query，$3 为 limit；params 必须按出现顺序给足 5 个。
# similarity = 1 - 余弦距离，与 RAG_THRESHOLD 同尺度，供相关性判定（RRF 分只用于排序融合）。
SEARCH_SQL = """
WITH vec AS (
  SELECT id, (embedding <=> $1::vector) AS dist,
         ROW_NUMBER() OVER (ORDER BY embedding <=> $1::vector) AS rk
  FROM chunks ORDER BY embedding <=> $1::vector LIMIT 50
),
lex AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY ts_rank_cd(tsv, q) DESC) AS rk
  FROM chunks, websearch_to_tsquery('simple', $2) q
  WHERE tsv @@ q ORDER BY ts_rank_cd(tsv, q) DESC LIMIT 50
),
fused AS (
  SELECT id, SUM(1.0/(60+rk)) AS score
  FROM (SELECT id,rk FROM vec UNION ALL SELECT id,rk FROM lex) u GROUP BY id
)
SELECT c.id, c.content, d.title, d.source_path, f.score, (1 - v.dist) AS similarity
FROM fused f
JOIN chunks c USING(id)
JOIN documents d ON d.id=c.document_id
LEFT JOIN vec v ON v.id = c.id
ORDER BY f.score DESC LIMIT $3;
"""


def _to_psycopg(sql: str) -> str:
    """把 $1/$2/$3 占位符按出现顺序替换为 psycopg 的 %s。"""
    for n in ("1", "2", "3"):
        sql = sql.replace(f"${n}", "%s")
    return sql


def _vec_literal(embedding: List[float]) -> str:
    """list[float] -> pgvector 文本字面量 '[v0,v1,...]'。"""
    return "[" + ",".join(f"{x:.8f}" for x in embedding) + "]"


class HybridRetriever(BaseRetriever):
    """向量 + 全文 RRF 融合检索器。

    k: 进上下文的片段数上限。candidates: 送重排的候选池大小（小于 k 无意义）。
    embedder 惰性构造，复用同一 OpenAI 兼容 embeddings 客户端。
    """

    k: int = 6
    candidates: int = RERANK_CANDIDATES
    embedder: OpenAIEmbeddings = None  # type: ignore[assignment]

    class Config:
        arbitrary_types_allowed = True

    def _get_embedder(self) -> OpenAIEmbeddings:
        if self.embedder is None:
            self.embedder = OpenAIEmbeddings(
                model=ONLINE_EMBED_MODEL,
                openai_api_base=OPENAI_API_BASE,
                openai_api_key=OPENAI_API_KEY,
                # 必须显式指定：Qwen3-4B 默认输出 2560 维，与库里的 1536 不匹配
                dimensions=EMBED_DIM,
            )
        return self.embedder

    async def _run_candidates(self, query: str, timings: Optional[dict] = None) -> List[Document]:
        """召回 + 重排打分，返回未截断的候选池。

        保持 RRF 序而非重排分序：线上 limit=N 拿到的是 RRF 前 N 条，
        候选池扫描靠这个顺序切片重放，顺序混了重放结果就和真实执行对不上。
        timings 传入时回填 embed_ms / sql_ms / rerank_ms。
        """
        t0 = time.monotonic()
        emb = await self._get_embedder().aembed_query(query)
        if timings is not None:
            timings["embed_ms"] = int((time.monotonic() - t0) * 1000)
        vl = _vec_literal(emb)
        # $1 出现 3 次、$2 一次、$3 一次，按出现顺序绑定 5 个参数
        limit = max(self.k, self.candidates) if RERANK_ENABLED else self.k
        params = (vl, vl, vl, query, limit)
        sql = _to_psycopg(SEARCH_SQL)
        t1 = time.monotonic()
        async with await psycopg.AsyncConnection.connect(DATABASE_URL) as conn:
            async with conn.cursor() as cur:
                await cur.execute(sql, params)
                rows = await cur.fetchall()
        if timings is not None:
            timings["sql_ms"] = int((time.monotonic() - t1) * 1000)
        docs: List[Document] = []
        for cid, content, title, source_path, score, similarity in rows:
            docs.append(Document(
                page_content=content,
                metadata={
                    "id": cid, "title": title, "source_path": source_path,
                    "score": float(score),
                    # 余弦相似度（1 - 余弦距离），与 RAG_THRESHOLD 同尺度，用于相关性判定
                    "similarity": float(similarity) if similarity is not None else 0.0,
                },
            ))
        # 重排：RRF 只保证"两路都命中"，不保证真相关；交叉编码器直接判 query↔正文
        if RERANK_ENABLED and docs:
            t2 = time.monotonic()
            try:
                scores = await rerank_scores(query, [d.page_content for d in docs])
                for d, s in zip(docs, scores):
                    d.metadata["rerank_score"] = s
            except Exception as e:  # noqa: BLE001 - 重排是增强项，挂了不能拖垮检索
                logger.warning("重排失败，回退 RRF 排序: %s", e)
            if timings is not None:
                timings["rerank_ms"] = int((time.monotonic() - t2) * 1000)
        return docs

    async def _run_search(self, query: str) -> List[Document]:
        docs = await self._run_candidates(query)
        # 重排关闭时全为 0 分，stable sort 保持 RRF 序，与旧行为一致
        docs.sort(key=lambda d: d.metadata.get("rerank_score", 0.0), reverse=True)
        docs = _apply_rerank_filter(docs)
        # 融合结果后处理：去噪、去重、控多样性（详见 _clean_docs）
        return _clean_docs(docs)[: self.k]

    def _get_relevant_documents(
        self, query: str, *, run_manager: CallbackManagerForRetrieverRun
    ) -> List[Document]:
        # 检索本质是异步 IO，同步入口用 asyncio 桥接
        import asyncio
        return asyncio.run(self._run_search(query))

    async def _aget_relevant_documents(
        self, query: str, *, run_manager: AsyncCallbackManagerForRetrieverRun
    ) -> List[Document]:
        return await self._run_search(query)


_WS_RE = re.compile(r"\s+")


def _content_key(text: str) -> str:
    """去重用的正文键：空白折叠后取全文。

    不能退回前缀比对——ingest 给每个 chunk 头部注入了「[标题路径]」，同标题下的
    兄弟片段前 40 字往往一模一样，按前缀去重会把不同段落整条误删（等于 per_title=1）。
    只折叠空白不删除空白：宁可漏合并，也不要把两段不同正文判成同一段。
    """
    return _WS_RE.sub(" ", text or "").strip()


def _clean_docs(docs: List[Document], sim_floor: float = 0.05, per_title: int = 2) -> List[Document]:
    """对融合后的候选片段做轻量后处理，提升送入生成的质量。

    - 丢弃余弦相似度过低（< sim_floor）的噪声片段，避免无关资料污染回答；
    - 按「标题 + 归一化全文」去重：SQL 的 fused 已按 id 分组，这里只兜住
      不同 id 承载同一段正文的情况，正常片段一条都不该被删；
    - 同一标题最多保留 per_title 条，防止单一文档刷屏、保证来源多样性。
    不改变原有按 RRF 分数的排序，sufficiency_check 仍按最高相似度与数量判定。
    """
    seen_ids: set = set()
    seen_text: set = set()
    per_title_count: dict = {}
    out: List[Document] = []
    for d in docs:
        sim = d.metadata.get("similarity", 0.0) or 0.0
        if sim < sim_floor:
            continue
        cid = d.metadata.get("id")
        if cid is not None:
            if cid in seen_ids:
                continue
            seen_ids.add(cid)
        title = d.metadata.get("title", "")
        key = (title, _content_key(d.page_content))
        if key in seen_text:
            continue
        seen_text.add(key)
        if per_title_count.get(title, 0) >= per_title:
            continue
        per_title_count[title] = per_title_count.get(title, 0) + 1
        out.append(d)
    return out


def _apply_rerank_filter(docs: List[Document], threshold: float = None,
                         enabled: bool = None) -> List[Document]:
    """按重排分再裁一次上下文（RAG_RERANK_FILTER 开关）。

    命中闸只看最高分，过线后低分片段照样进 prompt 和 sources；开启后
    只有 >= 阈值的片段进上下文。重排分缺失（重排关闭/失败）时原样返回，
    否则会把整批候选裁空。enabled 显式传入时覆盖开关，供离线重放用。
    """
    on = RAG_RERANK_FILTER if enabled is None else enabled
    if not on:
        return docs
    if not any(d.metadata.get("rerank_score") is not None for d in docs):
        return docs
    th = RERANK_THRESHOLD if threshold is None else threshold
    return [d for d in docs if (d.metadata.get("rerank_score") or 0.0) >= th]


def build_retriever(k: int = 6, candidates: int = None) -> HybridRetriever:
    if candidates is None:
        return HybridRetriever(k=k)
    return HybridRetriever(k=k, candidates=candidates)
