"""护栏节点：轻量 prompt-injection 检测。

只做规则匹配（不额外调 LLM），命中即写入 state["error"]，
由主图条件边路由到降级节点，绝不让可疑输入进入后续链路。
"""
import re

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
    """检测注入话术。命中返回 error，否则原样放行。"""
    query = state.get("query", "") or ""
    if _REGEX.search(query):
        return {
            "error": "guardrail_blocked",
            "intent": "clarify",
            "confidence": 0.0,
        }
    return {"error": None}
