"""护栏节点：轻量 prompt-injection 检测 + 每轮请求的状态边界。

只做规则匹配（不额外调 LLM），命中即写入 state["error"]，
由主图条件边路由到降级节点，绝不让可疑输入进入后续链路。
作为主图入口节点，还负责把「本轮工具结果」清零（见 guardrail 内注释），
并把 messages 裁到窗口内（见 _trim_history），避免 checkpoint 随轮数膨胀。
"""
import re

from langchain_core.messages import ToolMessage
from langgraph.types import Overwrite

from core.llm import count_message_tokens

# 常见注入/越权话术（中英），大小写不敏感
_INJECTION_PATTERNS = [
    r"忽略(以上|之前|前面)(的)?(所有)?(规则|指令|提示|设定)",
    r"无视(以上|之前|前面)(的)?(所有)?(规则|指令|提示|设定)",
    r"忘记(你(的|是)|之前)(的)?(身份|规则|指令|设定|提示)",
    r"你现在(是|不再是|扮演)",
    r"ignore\s+(all\s+)?(previous|above|prior)\s+(instructions?|rules?|prompts?)",
    r"disregard\s+(all\s+)?(previous|above|prior)",
    r"forget\s+(everything|your\s+instructions|the\s+rules)",
    r"you\s+are\s+now\s+(a|an|no\s+longer)",
    r"system\s*prompt",
    r"reveal\s+(your\s+)?(system|instructions|prompt)",
    r"new\s+instructions?:",
    r"developer\s+mode",
    r"jailbreak",
]
_REGEX = re.compile("|".join(f"(?:{p})" for p in _INJECTION_PATTERNS), re.IGNORECASE)

# 消息窗口：checkpoint 每轮只增不减，blob 会随会话轮数线性膨胀
# （每个 checkpoint 版本都存全量 messages）。生成侧本来就有 recent_turns
# 窗口（6~10 条），这里裁到 40 条对上下文零影响，纯粹是给存储瘦身。
_MSG_WINDOW = 40
# 窗口内再按 token 兜一道：超了继续从头部丢（防单条超大粘贴把窗口撑爆）
_MSG_TOKEN_BUDGET = 12000


def _trim_history(messages: list) -> list:
    """把 messages 裁到窗口内，返回可直接 Overwrite 的列表。

    两个约束：
    1. 末尾必须是「本轮新问题」（最后一条 HumanMessage），只裁头部；
    2. 裁出的头部不能留下悬挂的 ToolMessage——其配对的
       AIMessage(tool_calls) 已被裁掉，孤儿 ToolMessage 会让上游接口报错
       （口径同 state.recent_turns 的剔除逻辑）。
    """
    out = list(messages or [])
    if len(out) > _MSG_WINDOW:
        out = out[-_MSG_WINDOW:]
    while out and isinstance(out[0], ToolMessage):
        out = out[1:]
    while len(out) > 2 and count_message_tokens(out) > _MSG_TOKEN_BUDGET:
        out = out[1:]
        while out and isinstance(out[0], ToolMessage):
            out = out[1:]
    return out


async def guardrail(state: dict) -> dict:
    """检测注入话术；同时把「本轮工具结果」清零、messages 裁到窗口内。

    tool_results 用 operator.add reducer 跨工具轮次累加（一轮 ReAct 多跳），
    但它会随 checkpointer 跨「用户轮次」一起留存：不清零的话 synthesize 会把
    历史轮次的卡片重发一遍（实测同 thread 第二轮卡片成倍）。
    每轮请求都从本节点开始，用 Overwrite 绕过 reducer 强制清零，
    保证 synthesize 只看到「本轮」取数结果。
    messages 同理：用 Overwrite 整体替换为裁剪后的窗口，阻止 checkpoint 膨胀。
    """
    query = state.get("query", "") or ""
    trimmed = _trim_history(state.get("messages") or [])
    if _REGEX.search(query):
        return {
            "error": "guardrail_blocked",
            "intent": "clarify",
            "confidence": 0.0,
            "tool_results": Overwrite([]),
            "messages": Overwrite(trimmed),
        }
    return {"error": None, "tool_results": Overwrite([]), "messages": Overwrite(trimmed)}
