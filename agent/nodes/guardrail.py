"""护栏节点：轻量 prompt-injection 检测 + 每轮请求的状态边界。

只做规则匹配（不额外调 LLM），命中即写入 state["error"]，
由主图条件边路由到降级节点，绝不让可疑输入进入后续链路。
作为主图入口节点，还负责把「本轮工具结果」清零（见 guardrail 内注释）。
"""
import re

from langgraph.types import Overwrite

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


async def guardrail(state: dict) -> dict:
    """检测注入话术；同时把「本轮工具结果」清零。

    tool_results 用 operator.add reducer 跨工具轮次累加（一轮 ReAct 多跳），
    但它会随 checkpointer 跨「用户轮次」一起留存：不清零的话 synthesize 会把
    历史轮次的卡片重发一遍（实测同 thread 第二轮卡片成倍）。
    每轮请求都从本节点开始，用 Overwrite 绕过 reducer 强制清零，
    保证 synthesize 只看到「本轮」取数结果。
    """
    query = state.get("query", "") or ""
    if _REGEX.search(query):
        return {
            "error": "guardrail_blocked",
            "intent": "clarify",
            "confidence": 0.0,
            "tool_results": Overwrite([]),
        }
    return {"error": None, "tool_results": Overwrite([])}
