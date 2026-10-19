"""Postgres checkpointer：为图提供跨请求的对话持久化。

用连接池而非单连接，避免并发请求争抢一条连接。首次使用时惰性建池并 setup 表结构。
"""
from typing import Optional

from psycopg_pool import AsyncConnectionPool
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver

from config import DATABASE_URL

_pool: Optional[AsyncConnectionPool] = None
_checkpointer: Optional[AsyncPostgresSaver] = None


async def get_checkpointer() -> AsyncPostgresSaver:
    """返回全局单例 checkpointer（惰性初始化连接池 + 建表）。"""
    global _pool, _checkpointer
    if _checkpointer is None:
        _pool = AsyncConnectionPool(
            conninfo=DATABASE_URL,
            max_size=20,
            kwargs={"autocommit": True, "prepare_threshold": 0},
        )
        # 等待池中至少一条连接可用
        await _pool.wait()
        _checkpointer = AsyncPostgresSaver(_pool)
        await _checkpointer.setup()
    return _checkpointer


def make_thread_id(tenant: str, user_id: str, session_id: str) -> str:
    """多租户 thread_id 拼装：tenant:user:session。"""
    return f"{tenant}:{user_id}:{session_id}"


async def close_checkpointer() -> None:
    """进程退出时释放连接池。"""
    global _pool, _checkpointer
    if _pool is not None:
        await _pool.close()
    _pool = None
    _checkpointer = None
