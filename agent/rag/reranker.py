"""重排：调 OpenAI 兼容的 /v1/rerank 给 (query, chunk) 打相关性分。

为什么不用余弦相似度做命中闸：向量空间存在很高的相似度地板值，实测无关 query
与语料的平均相似度也有 0.56，相关/无关的 top 相似度完全重叠，绝对阈值切不开；
而交叉编码器直接看 query 与正文的交互，实测相关 ~0.99、无关 0，可以绝对阈值判定。
"""
import logging

import httpx

from config import OPENAI_API_BASE, OPENAI_API_KEY, RERANK_MODEL, RERANK_TIMEOUT

logger = logging.getLogger("stocka.agent")


async def rerank_scores(query: str, texts: list[str]) -> list[float]:
    """返回与 texts 等长的相关性分数（0~1，越大越相关）。

    失败直接抛异常，由调用方决定降级（保留原排序继续走），不在这里吞掉。
    """
    if not texts:
        return []

    url = OPENAI_API_BASE.rstrip("/") + "/rerank"
    payload = {
        "model": RERANK_MODEL,
        "query": query,
        "documents": texts,
        # 要回全部候选的分，top_n 才不会是子集
        "top_n": len(texts),
        "return_documents": False,
    }
    headers = {"Authorization": f"Bearer {OPENAI_API_KEY}"}

    async with httpx.AsyncClient(timeout=RERANK_TIMEOUT) as client:
        resp = await client.post(url, json=payload, headers=headers)
        resp.raise_for_status()
        data = resp.json()

    # 接口只回 results 数组，按 index 回填，避免依赖其返回顺序
    scores = [0.0] * len(texts)
    for item in data.get("results", []):
        idx = item.get("index")
        if isinstance(idx, int) and 0 <= idx < len(texts):
            scores[idx] = float(item.get("relevance_score", 0.0))
    return scores
