"""改写节点：指代消解，把「依赖上下文的问题」改写成独立完整的问题。

例：历史在聊贵州茅台，用户问「那它的市盈率呢」-> 改写为「贵州茅台的市盈率是多少」。
用 small_llm（温度 0）；若 Redis 中存在该 thread 的滚动摘要（post 节点写入），
则拼入 prompt 作为对话背景，与最近历史一起形成完整上下文（滚动摘要闭环）。
改写失败/无历史无摘要时回退到原始 query，绝不阻断主流程。
"""
import redis.asyncio as aioredis
from langchain_core.messages import HumanMessage, AIMessage, SystemMessage

from config import REDIS_URL
from llm import get_small_llm

_SYSTEM = (
    "你是查询改写器。根据对话历史，把用户最新的问题改写成一个"
    "不依赖上下文、可独立理解的完整问题。要求：\n"
    "1. 只输出改写后的问题本身，不要解释、不要引号、不要前缀。\n"
    "2. 补全指代（它/这个/那只股票等）为具体实体。\n"
    "3. 若最新问题本身已完整，原样输出。\n"
    "4. 保持与用户相同的语言。"
)


def _format_history(messages: list, limit: int = 10) -> str:
    """把最近若干条消息压成 'role: text' 文本，供小模型理解上下文。"""
    lines = []
    for m in messages[-limit:]:
        if isinstance(m, HumanMessage):
            role = "用户"
        elif isinstance(m, AIMessage):
            role = "助手"
        else:
            continue
        content = m.content if isinstance(m.content, str) else str(m.content)
        if content.strip():
            lines.append(f"{role}: {content.strip()}")
    return "\n".join(lines)


_redis = None


def _get_redis():
    global _redis
    if _redis is None:
        _redis = aioredis.from_url(REDIS_URL, encoding="utf-8", decode_responses=True)
    return _redis


async def _get_summary(thread_id: str) -> str:
    """读 Redis 中的滚动摘要（post 节点写入）。失败静默返回空。"""
    if not thread_id:
        return ""
    try:
        summary = await _get_redis().get(f"agent:summary:{thread_id}")
    except Exception:  # noqa: BLE001 - 摘要不可用不影响改写
        return ""
    if not summary:
        return ""
    return summary.decode() if isinstance(summary, bytes) else summary


async def rewrite(state: dict) -> dict:
    query = state.get("query", "") or ""
    history = _format_history(state.get("messages", []))
    summary = await _get_summary(state.get("thread_id", ""))
    # 无历史且无摘要则无需改写
    if not history and not summary:
        return {"rewritten": query}

    # 滚动摘要作为对话背景前缀，弥补被裁剪掉的早期历史
    bg = f"对话背景摘要：\n{summary}\n\n" if summary else ""
    prompt = f"{bg}对话历史：\n{history}\n\n用户最新问题：{query}\n\n改写后的独立问题："
    try:
        llm = get_small_llm(streaming=False, temperature=0.0)
        resp = await llm.ainvoke([
            SystemMessage(content=_SYSTEM),
            HumanMessage(content=prompt),
        ])
        text = (resp.content or "").strip().strip('"').strip()
        # 空结果或多行异常时回退原始 query
        rewritten = text.splitlines()[0].strip() if text else query
        return {"rewritten": rewritten or query}
    except Exception:  # noqa: BLE001 - 改写失败不影响主流程
        return {"rewritten": query}
