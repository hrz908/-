"""完整处理流水线：加载 -> 分块 -> 嵌入 -> 入库。"""
import logging
from pathlib import Path

import redis

from chunker import chunk_markdown
from config import CHUNK_OVERLAP, CHUNK_SIZE, EMBED_MODEL, LOCK_TTL
from embedder import Embedder
from loaders import content_hash, load_document
from store import Store

logger = logging.getLogger(__name__)


async def process_file(
    path: str,
    store: Store,
    embedder: Embedder,
    redis_client: redis.Redis,
) -> bool:
    """
    处理单个文件，返回是否实际执行了入库（False 表示跳过）。

    流程:
    1. Redis SETNX lock:ingest:{path} (TTL 300s) 去重锁
    2. 加载文档内容
    3. 计算 content_hash
    4. 检查幂等（store.transactional_upsert 返回 None 表示无变化）
    5. 分块
    6. 嵌入
    7. 单事务写入：upsert doc -> delete old chunks -> insert new chunks
    8. 释放锁
    """
    p = Path(path)
    if not p.exists():
        logger.warning("文件不存在，跳过: %s", path)
        return False

    if not p.is_file():
        logger.debug("非文件路径，跳过: %s", path)
        return False

    # 1. 去重锁
    lock_key = f"lock:ingest:{path}"
    try:
        acquired = redis_client.set(lock_key, "1", nx=True, ex=LOCK_TTL)
        if not acquired:
            logger.info("文件正在被其他进程处理，跳过: %s", path)
            return False
    except Exception as e:
        logger.warning("Redis 锁获取异常 (%s)，继续处理: %s", e, path)

    try:
        # 2. 加载文档内容
        logger.info("开始处理文件: %s", path)
        try:
            content, mime_type = load_document(str(p))
        except Exception as e:
            logger.error("文档加载失败: %s - %s", path, e)
            return False

        if not content or not content.strip():
            logger.warning("文档内容为空，跳过: %s", path)
            return False

        # 3. 计算 content_hash
        doc_hash = content_hash(content)
        title = p.stem

        # 4 & 5. 分块
        chunks = chunk_markdown(content, chunk_size=CHUNK_SIZE, overlap=CHUNK_OVERLAP)
        if not chunks:
            logger.warning("分块结果为空，跳过: %s", path)
            return False

        # 6. 嵌入
        texts = [c["content"] for c in chunks]
        try:
            embeddings = embedder.embed(texts)
        except Exception as e:
            logger.error("嵌入失败: %s - %s", path, e)
            return False

        # 将 embedding 附加到 chunks
        for chunk, emb in zip(chunks, embeddings):
            chunk["embedding"] = emb

        # 7. 单事务写入
        doc_id = await store.transactional_upsert(
            source_path=str(p),
            title=title,
            mime_type=mime_type,
            content_hash=doc_hash,
            embed_model=EMBED_MODEL,
            chunks=chunks,
        )

        if doc_id is None:
            logger.info("文档无变化，已跳过: %s", path)
            return False

        logger.info("文件入库成功: %s -> doc_id=%s, chunks=%d", path, doc_id, len(chunks))
        return True

    except Exception as e:
        logger.error("处理文件异常: %s - %s", path, e, exc_info=True)
        return False

    finally:
        # 8. 释放锁
        try:
            redis_client.delete(lock_key)
        except Exception as e:
            logger.debug("释放锁失败: %s", e)


async def delete_file(path: str, store: Store) -> bool:
    """文件被删除时，级联删除数据库记录。"""
    try:
        await store.delete_document_by_path(path)
        logger.info("已清理删除文件的数据库记录: %s", path)
        return True
    except Exception as e:
        logger.error("删除文件记录失败: %s - %s", path, e)
        return False
