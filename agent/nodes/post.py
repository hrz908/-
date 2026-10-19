"""收尾节点：滚动摘要、免责声明、用量统计。

在主图末尾（子图之后、END 之前）执行：
1. messages token 数超阈值 -> 用小模型滚动摘要，写入 Redis（按 thread_id）。
2. 涉及个股判断 -> 在最后一条 assistant 消息末尾追加免责声明（按 id 覆盖更新）。
3. 统计 usage 写回 state。
所有步骤都容错，任何一步失败都不阻断响应。
"""
import redis.asyncio as aioredis
from langchain_core.messages import AIMessage, HumanMessage

from config import REDIS_URL, SUMMARY_TOKEN_LIMIT
from llm import get_small_llm

_DISCLAIMER = "\n\n> 以上信息仅供参考，不构成任何投资建议。市场有风险，投资需谨慎。"

# 个股相关意图：这些场景的回答需要挂免责声明
_STOCK_INTENTS = {"mcp"}

_redis = None


def _get_redis():
    global _redis
    if _redis is None:
        _redis = aioredis.from_url(REDIS_URL, encoding="utf-8", decode_responses=True)
    return _redis


def _count_tokens(messages: list) -> int:
    """粗略统计 token：优先 tiktoken，失败则按字符数估算。"""
    text = ""
    for m in messages:
        c = m.content if isinstance(m.content, str) else str(m.content)
        text += c
    try:
        import tiktoken
        enc = tiktoken.get_encoding("cl100k_base")
        return len(enc.encode(text))
    except Exception:  # noqa: BLE001
        # 中英混合下 ~2 char/token 的保守估计
        return max(1, len(text) // 2)


def _collect_usage(messages: list) -> dict:
    """从消息的 usage_metadata 聚合 token 用量。"""
    total = {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
    for m in messages:
        um = getattr(m, "usage_metadata", None)
        if isinstance(um, dict):
            total["input_tokens"] += um.get("input_tokens", 0) or 0
            total["output_tokens"] += um.get("output_tokens", 0) or 0
            total["total_tokens"] += um.get("total_tokens", 0) or 0
    return total


async def _roll_summary(state: dict) -> None:
    """超阈值时做滚动摘要写 Redis。失败静默。"""
    messages = state.get("messages", [])
    if _count_tokens(messages) <= SUMMARY_TOKEN_LIMIT:
        return
    lines = []
    for m in messages:
        if isinstance(m, HumanMessage):
            lines.append(f"用户: {m.content}")
        elif isinstance(m, AIMessage):
            c = m.content if isinstance(m.content, str) else str(m.content)
            if c.strip():
                lines.append(f"助手: {c}")
    if not lines:
        return
    try:
        llm = get_small_llm(streaming=False, temperature=0.0)
        resp = await llm.ainvoke([HumanMessage(
            content="请把以下对话压缩成简洁的中文摘要，保留关键实体、结论与用户偏好：\n" + "\n".join(lines)
        )])
        summary = (resp.content or "").strip()
        if summary:
            r = _get_redis()
            await r.set(f"agent:summary:{state.get('thread_id','')}", summary, ex=7 * 24 * 3600)
    except Exception:  # noqa: BLE001
        pass


async def post(state: dict) -> dict:
    # 1. 滚动摘要（后台容错）
    await _roll_summary(state)

    messages = list(state.get("messages", []))
    updates: dict = {"usage": _collect_usage(messages)}

    # 2. 免责声明：涉及个股时，追加到最后一条 assistant 消息（按 id 覆盖）
    intent = state.get("intent", "")
    if intent in _STOCK_INTENTS:
        for m in reversed(messages):
            if isinstance(m, AIMessage):
                content = m.content if isinstance(m.content, str) else str(m.content)
                if content.strip() and _DISCLAIMER.strip() not in content:
                    updates["messages"] = [AIMessage(id=m.id, content=content + _DISCLAIMER)]
                break

    return updates
