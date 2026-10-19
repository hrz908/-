"""数据库操作层：使用 psycopg async 操作 PostgreSQL。"""
import json
import logging
import uuid
from typing import Optional

import psycopg
from psycopg.rows import dict_row

logger = logging.getLogger(__name__)


class Store:
    """文档和 chunks 的数据库操作封装。"""

    def __init__(self, database_url: str):
        self.database_url = database_url
        self._pool: Optional[psycopg.AsyncConnectionPool] = None

    async def connect(self):
        """初始化连接池"""
        self._pool = psycopg.AsyncConnectionPool(
            conninfo=self.database_url,
            min_size=2,
            max_size=10,
            open=False,
            kwargs={"row_factory": dict_row},
        )
        await self._pool.open()
        logger.info("数据库连接池已建立")

    async def close(self):
        """关闭连接池"""
        if self._pool:
            await self._pool.close()
            logger.info("数据库连接池已关闭")

    async def ensure_tables(self):
        """确保 documents 和 chunks 表存在（自动建表）"""
        async with self._pool.connection() as conn:
            await conn.execute("""
                CREATE TABLE IF NOT EXISTS documents (
                    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                    source_path TEXT NOT NULL UNIQUE,
                    title TEXT NOT NULL DEFAULT '',
                    mime_type TEXT NOT NULL DEFAULT '',
                    content_hash TEXT NOT NULL DEFAULT '',
                    embed_model TEXT NOT NULL DEFAULT '',
                    chunk_count INT NOT NULL DEFAULT 0,
                    status TEXT NOT NULL DEFAULT 'active',
                    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
                );
            """)
            await conn.execute("""
                CREATE TABLE IF NOT EXISTS chunks (
                    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
                    seq INT NOT NULL,
                    content TEXT NOT NULL,
                    token_count INT NOT NULL DEFAULT 0,
                    heading_path TEXT NOT NULL DEFAULT '',
                    embedding vector(1024),
                    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
                );
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_chunks_document_id ON chunks(document_id);
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_documents_status ON documents(status);
            """)
            await conn.commit()
            logger.info("数据库表已就绪")

    async def upsert_document(
        self,
        source_path: str,
        title: str,
        mime_type: str,
        content_hash: str,
        embed_model: str,
        chunk_count: int,
    ) -> Optional[str]:
        """
        UPSERT documents 表。
        
        幂等逻辑：如果 content_hash + embed_model + status('active') 三者全同，
        表示文档无变化，返回 None 跳过后续处理。
        否则更新记录并返回 document_id。
        """
        async with self._pool.connection() as conn:
            # 先查询现有记录判断是否跳过
            cur = await conn.execute(
                """
                SELECT id, content_hash, embed_model, status
                FROM documents
                WHERE source_path = %s
                """,
                (source_path,),
            )
            row = await cur.fetchone()

            if row:
                # 幂等判断：三者全同则跳过
                if (
                    row["content_hash"] == content_hash
                    and row["embed_model"] == embed_model
                    and row["status"] == "active"
                ):
                    logger.debug("文档无变化，跳过: %s", source_path)
                    return None

                # 有变化，更新
                doc_id = str(row["id"])
                await conn.execute(
                    """
                    UPDATE documents
                    SET title = %s, mime_type = %s, content_hash = %s,
                        embed_model = %s, chunk_count = %s, status = 'active',
                        updated_at = now()
                    WHERE id = %s
                    """,
                    (title, mime_type, content_hash, embed_model, chunk_count, doc_id),
                )
            else:
                # 新插入
                doc_id = str(uuid.uuid4())
                await conn.execute(
                    """
                    INSERT INTO documents (id, source_path, title, mime_type, content_hash, embed_model, chunk_count, status)
                    VALUES (%s, %s, %s, %s, %s, %s, %s, 'active')
                    """,
                    (doc_id, source_path, title, mime_type, content_hash, embed_model, chunk_count),
                )

            await conn.commit()
            return doc_id

    async def delete_chunks(self, document_id: str):
        """删除文档的所有旧 chunks"""
        async with self._pool.connection() as conn:
            await conn.execute(
                "DELETE FROM chunks WHERE document_id = %s",
                (document_id,),
            )
            await conn.commit()

    async def insert_chunks_batch(self, document_id: str, chunks: list[dict]):
        """
        批量插入 chunks。
        
        chunks 格式: [{"seq": 0, "content": "...", "token_count": N, 
                       "metadata": {"heading_path": "..."}, "embedding": [...]}]
        """
        if not chunks:
            return

        async with self._pool.connection() as conn:
            # 使用 executemany 批量插入
            await conn.executemany(
                """
                INSERT INTO chunks (document_id, seq, content, token_count, heading_path, embedding)
                VALUES (%s, %s, %s, %s, %s, %s)
                """,
                [
                    (
                        document_id,
                        chunk["seq"],
                        chunk["content"],
                        chunk.get("token_count", 0),
                        chunk.get("metadata", {}).get("heading_path", ""),
                        json.dumps(chunk.get("embedding", [])),
                    )
                    for chunk in chunks
                ],
            )
            await conn.commit()
            logger.info("已插入 %d chunks for document %s", len(chunks), document_id)

    async def delete_document_by_path(self, source_path: str):
        """
        文件被删除时，级联删除 document。
        chunks 通过 ON DELETE CASCADE 自动删除。
        """
        async with self._pool.connection() as conn:
            cur = await conn.execute(
                "DELETE FROM documents WHERE source_path = %s RETURNING id",
                (source_path,),
            )
            row = await cur.fetchone()
            await conn.commit()
            if row:
                logger.info("已删除文档及其 chunks: %s (id=%s)", source_path, row["id"])
            else:
                logger.debug("文档不存在，无需删除: %s", source_path)

    async def get_stale_documents(self, current_model: str) -> list[dict]:
        """
        获取 embed_model != current_model 的文档，用于 reembed。
        """
        async with self._pool.connection() as conn:
            cur = await conn.execute(
                """
                SELECT id, source_path, title, mime_type, embed_model, chunk_count
                FROM documents
                WHERE embed_model != %s AND status = 'active'
                ORDER BY updated_at ASC
                """,
                (current_model,),
            )
            rows = await cur.fetchall()
            return [dict(r) for r in rows]

    async def transactional_upsert(
        self,
        source_path: str,
        title: str,
        mime_type: str,
        content_hash: str,
        embed_model: str,
        chunks: list[dict],
    ) -> Optional[str]:
        """
        单事务写入：upsert document -> delete old chunks -> insert new chunks。
        
        返回 document_id，如果幂等跳过则返回 None。
        """
        async with self._pool.connection() as conn:
            async with conn.transaction():
                # 1. 查询现有记录判断幂等
                cur = await conn.execute(
                    """
                    SELECT id, content_hash, embed_model, status
                    FROM documents
                    WHERE source_path = %s
                    """,
                    (source_path,),
                )
                row = await cur.fetchone()

                if row:
                    if (
                        row["content_hash"] == content_hash
                        and row["embed_model"] == embed_model
                        and row["status"] == "active"
                    ):
                        logger.debug("文档无变化，跳过事务写入: %s", source_path)
                        return None
                    doc_id = str(row["id"])
                    await conn.execute(
                        """
                        UPDATE documents
                        SET title = %s, mime_type = %s, content_hash = %s,
                            embed_model = %s, chunk_count = %s, status = 'active',
                            updated_at = now()
                        WHERE id = %s
                        """,
                        (title, mime_type, content_hash, embed_model, len(chunks), doc_id),
                    )
                else:
                    doc_id = str(uuid.uuid4())
                    await conn.execute(
                        """
                        INSERT INTO documents (id, source_path, title, mime_type, content_hash, embed_model, chunk_count, status)
                        VALUES (%s, %s, %s, %s, %s, %s, %s, 'active')
                        """,
                        (doc_id, source_path, title, mime_type, content_hash, embed_model, len(chunks)),
                    )

                # 2. 删除旧 chunks
                await conn.execute(
                    "DELETE FROM chunks WHERE document_id = %s",
                    (doc_id,),
                )

                # 3. 批量插入新 chunks
                if chunks:
                    await conn.executemany(
                        """
                        INSERT INTO chunks (document_id, seq, content, token_count, heading_path, embedding)
                        VALUES (%s, %s, %s, %s, %s, %s)
                        """,
                        [
                            (
                                doc_id,
                                chunk["seq"],
                                chunk["content"],
                                chunk.get("token_count", 0),
                                chunk.get("metadata", {}).get("heading_path", ""),
                                json.dumps(chunk.get("embedding", [])),
                            )
                            for chunk in chunks
                        ],
                    )

        logger.info("事务写入完成: %s -> %d chunks", source_path, len(chunks))
        return doc_id

