"""混合检索器：pgvector 向量召回 + PostgreSQL 全文检索，RRF(k=60) 融合。"""
from typing import List

from langchain_core.callbacks import CallbackManagerForRetrieverRun, AsyncCallbackManagerForRetrieverRun
from langchain_core.documents import Document
from langchain_core.retrievers import BaseRetriever
from langchain_openai import OpenAIEmbeddings

import psycopg

from config import DATABASE_URL, OPENAI_API_BASE, OPENAI_API_KEY, EMBED_MODEL

# 混合检索 SQL（RRF k=60）——保留 $n 占位形式作为规范文档，
# 执行时经 _to_psycopg 转成 psycopg 的 %s 位置参数。
SEARCH_SQL = """
WITH vec AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY embedding <=> $1::vector) AS rk
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
SELECT c.id, c.content, d.title, d.source_path, f.score
FROM fused f JOIN chunks c USING(id) JOIN documents d ON d.id=c.document_id
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

    k: 返回片段数上限。embedder 惰性构造，复用同一 OpenAI 兼容 embeddings 客户端。
    """

    k: int = 6
    embedder: OpenAIEmbeddings = None  # type: ignore[assignment]

    class Config:
        arbitrary_types_allowed = True

    def _get_embedder(self) -> OpenAIEmbeddings:
        if self.embedder is None:
            self.embedder = OpenAIEmbeddings(
                model=EMBED_MODEL,
                openai_api_base=OPENAI_API_BASE,
                openai_api_key=OPENAI_API_KEY,
            )
        return self.embedder

    async def _run_search(self, query: str) -> List[Document]:
        emb = await self._get_embedder().aembed_query(query)
        params = (_vec_literal(emb), _vec_literal(emb), query, self.k)
        sql = _to_psycopg(SEARCH_SQL)
        async with await psycopg.AsyncConnection.connect(DATABASE_URL) as conn:
            async with conn.cursor() as cur:
                await cur.execute(sql, params)
                rows = await cur.fetchall()
        docs: List[Document] = []
        for cid, content, title, source_path, score in rows:
            docs.append(Document(
                page_content=content,
                metadata={"id": cid, "title": title, "source_path": source_path, "score": float(score)},
            ))
        return docs

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


def build_retriever(k: int = 6) -> HybridRetriever:
    return HybridRetriever(k=k)
