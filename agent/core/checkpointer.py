"""Postgres checkpointer：为图提供跨请求的对话持久化。

用连接池而非单连接，避免并发请求争抢一条连接。首次使用时惰性建池并 setup 表结构。
"""
import logging
from typing import Optional

from psycopg_pool import AsyncConnectionPool
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver

from config import DATABASE_URL

logger = logging.getLogger("stocka.agent")

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


async def close_checkpointer() -> None:
    """进程退出时释放连接池。"""
    global _pool, _checkpointer
    if _pool is not None:
        await _pool.close()
    _pool = None
    _checkpointer = None


# 版本数超过该值就触发裁剪（一轮 run 约 10+ 个 superstep，各落一份全量 checkpoint）。
# 触发即裁到 1 份，稳态下单线程 checkpoint 行数 ≈ 一轮 run 的量，不再随 run 数增长。
_PRUNE_TRIGGER = 4


async def prune_thread_checkpoints(thread_id: str, keep: int = 1) -> bool:
    """把一个线程的历史 checkpoint 版本裁到只剩最新 keep 份。

    PostgresSaver 每个 superstep 都会写一份全量 checkpoint 且从不回收旧版本：
    不裁的话单线程行数随 run 数线性增长（消息裁剪只让单份 blob 有界，管不到行数）。
    做法是「删旧 + 补回最新」——alist 取最新一份，adelete_thread 清全线程，
    aput 原样补回，全程只用 saver 公开 API，不碰表结构。

    裁剪是优化不是功能：任何一步失败都只记日志并返回 False，
    绝不能让它把一轮对话搞失败（最坏损失是该线程的历史版本，最新状态不丢）。
    """
    if not thread_id:
        return False
    try:
        cp = await get_checkpointer()
        if not hasattr(cp, "adelete_thread"):
            return False
        config = {"configurable": {"thread_id": thread_id, "checkpoint_ns": ""}}
        latest = None
        seen = 0
        async for tn in cp.alist(config, limit=_PRUNE_TRIGGER + 1):
            seen += 1
            if latest is None:
                latest = tn
        if latest is None or seen <= keep:
            return False
        await cp.adelete_thread(thread_id)
        # channel_versions 不是所有版本都有该字段，取不到就传空 dict：
        # 之后本轮 run 的每次 aput 都会带上真实 new_versions 重建
        new_versions = getattr(latest, "channel_versions", None) or {}
        await cp.aput(
            {
                "configurable": {
                    "thread_id": thread_id,
                    "checkpoint_ns": "",
                    "checkpoint_id": latest.checkpoint["id"],
                }
            },
            latest.checkpoint,
            latest.metadata,
            new_versions,
        )
        return True
    except Exception as e:  # noqa: BLE001 - 裁剪失败不阻断对话
        logger.warning("prune checkpoints failed for %s: %s", thread_id, e)
        return False
