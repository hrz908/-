"""改写节点：指代消解，把「依赖上下文的问题」改写成独立完整的问题。

例：历史在聊贵州茅台，用户问「那它的市盈率呢」-> 改写为「贵州茅台的市盈率是多少」。
用 small_llm（温度 0）；改写失败/无历史时回退到原始 query，绝不阻断主流程。
"""
from langchain_core.messages import HumanMessage, AIMessage, SystemMessage

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


async def rewrite(state: dict) -> dict:
    query = state.get("query", "") or ""
    history = _format_history(state.get("messages", []))
    # 无历史则无需改写
    if not history:
        return {"rewritten": query}

    prompt = f"对话历史：\n{history}\n\n用户最新问题：{query}\n\n改写后的独立问题："
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
