"""数据库操作层：使用 psycopg async 操作 PostgreSQL。"""
import json
import logging
from typing import Optional

import psycopg
import psycopg.errors
from psycopg.rows import dict_row

logger = logging.getLogger(__name__)


def _vec_literal(embedding: list) -> str:
    """list[float] -> pgvector 文本字面量 '[v0,v1,...]'（配合 ::vector 强转使用）。"""
    return "[" + ",".join(f"{float(x):.8f}" for x in embedding) + "]"


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
        """
        校验 documents 和 chunks 表已存在。

        表结构由 server/migrations/005_agent.sql 管理，ingest 不自建表，
        这里只做启动前置检查，缺表时快速失败并给出提示。
        """
        try:
            async with self._pool.connection() as conn:
                await conn.execute("SELECT 1 FROM documents LIMIT 1")
                await conn.execute("SELECT 1 FROM chunks LIMIT 1")
        except psycopg.errors.UndefinedTable as e:
            raise RuntimeError(
                "数据库表不存在，请先执行 server/migrations/005_agent.sql 迁移"
            ) from e
        logger.info("数据库表校验通过（schema 由 migration 005 管理）")

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
        
        幂等逻辑：如果 content_hash + embed_model + status('ok') 三者全同，
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

            if row and (
                row["content_hash"] == content_hash
                and row["embed_model"] == embed_model
                and row["status"] == "ok"
            ):
                # 幂等判断：三者全同则跳过
                logger.debug("文档无变化，跳过: %s", source_path)
                return None

            # ON CONFLICT upsert，RETURNING id
            cur = await conn.execute(
                """
                INSERT INTO documents
                    (source_path, title, mime_type, content_hash,
                     embed_model, status, chunk_count, error, updated_at)
                VALUES (%s, %s, %s, %s, %s, 'ok', %s, NULL, now())
                ON CONFLICT (source_path) DO UPDATE SET
                    content_hash = EXCLUDED.content_hash,
                    embed_model  = EXCLUDED.embed_model,
                    title        = EXCLUDED.title,
                    mime_type    = EXCLUDED.mime_type,
                    chunk_count  = EXCLUDED.chunk_count,
                    status       = 'ok',
                    error        = NULL,
                    updated_at   = now()
                RETURNING id
                """,
                (source_path, title, mime_type, content_hash, embed_model, chunk_count),
            )
            row = await cur.fetchone()
            await conn.commit()
            return str(row["id"])

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

        对齐 005 schema：chunks.id 为 BIGSERIAL 自增，不手动指定；
        tsv 为 GENERATED 列，不写入；heading_path 存入 metadata JSONB。
        """
        if not chunks:
            return

        async with self._pool.connection() as conn:
            # 使用 executemany 批量插入
            await conn.executemany(
                """
                INSERT INTO chunks (document_id, seq, content, token_count, metadata, embedding)
                VALUES (%s, %s, %s, %s, %s::jsonb, %s::vector)
                """,
                [
                    (
                        document_id,
                        chunk["seq"],
                        chunk["content"],
                        chunk.get("token_count"),
                        json.dumps(chunk.get("metadata", {}), ensure_ascii=False),
                        _vec_literal(chunk["embedding"]),
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
                SELECT id, source_path, title, mime_type, content_hash,
                       embed_model, status, chunk_count, error, updated_at
                FROM documents
                WHERE embed_model != %s AND status = 'ok'
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
                        and row["status"] == "ok"
                    ):
                        logger.debug("文档无变化，跳过事务写入: %s", source_path)
                        return None

                # upsert（幂等跳过已在上方处理），RETURNING id
                cur = await conn.execute(
                    """
                    INSERT INTO documents
                        (source_path, title, mime_type, content_hash,
                         embed_model, status, chunk_count, error, updated_at)
                    VALUES (%s, %s, %s, %s, %s, 'ok', %s, NULL, now())
                    ON CONFLICT (source_path) DO UPDATE SET
                        content_hash = EXCLUDED.content_hash,
                        embed_model  = EXCLUDED.embed_model,
                        title        = EXCLUDED.title,
                        mime_type    = EXCLUDED.mime_type,
                        chunk_count  = EXCLUDED.chunk_count,
                        status       = 'ok',
                        error        = NULL,
                        updated_at   = now()
                    RETURNING id
                    """,
                    (source_path, title, mime_type, content_hash, embed_model, len(chunks)),
                )
                doc_id = str((await cur.fetchone())["id"])

                # 2. 删除旧 chunks
                await conn.execute(
                    "DELETE FROM chunks WHERE document_id = %s",
                    (doc_id,),
                )

                # 3. 批量插入新 chunks（id 自增、tsv 生成列均不写入）
                if chunks:
                    await conn.executemany(
                        """
                        INSERT INTO chunks (document_id, seq, content, token_count, metadata, embedding)
                        VALUES (%s, %s, %s, %s, %s::jsonb, %s::vector)
                        """,
                        [
                            (
                                doc_id,
                                chunk["seq"],
                                chunk["content"],
                                chunk.get("token_count"),
                                json.dumps(chunk.get("metadata", {}), ensure_ascii=False),
                                _vec_literal(chunk["embedding"]),
                            )
                            for chunk in chunks
                        ],
                    )

        logger.info("事务写入完成: %s -> %d chunks", source_path, len(chunks))
        return doc_id

