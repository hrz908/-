"""提示词渲染与路由结果解析。

render: 用「定向替换」而非 str.format 填充 {var}，避免模板里出现的花括号被误解析。
parse_route: 解析分类节点输出的 XML（失败时兜底解析 JSON，再失败降级 clarify）。
"""
import os
import re
import json
from functools import lru_cache

_PROMPT_DIR = os.path.dirname(os.path.abspath(__file__))


@lru_cache(maxsize=None)
def load_template(name: str) -> str:
    """读取 prompts 目录下的模板文件（带缓存）。name 如 'router.xml'。"""
    with open(os.path.join(_PROMPT_DIR, name), "r", encoding="utf-8") as f:
        return f.read()


def render(name: str, **vars) -> str:
    """把 {key} 占位符替换为对应值；未提供的占位符替换为空串。"""
    text = load_template(name)
    for key, val in vars.items():
        text = text.replace("{" + key + "}", "" if val is None else str(val))
    # 清掉剩余未填充的占位符，避免把 {xxx} 原样发给模型
    text = re.sub(r"\{[a-zA-Z_][a-zA-Z0-9_]*\}", "", text)
    return text


def extract(tag: str, text: str):
    """抽取 <tag>...</tag> 内容，取最后一个匹配（模型可能重复输出）。"""
    m = re.findall(rf"<{tag}>(.*?)</{tag}>", text, re.S)
    return m[-1].strip() if m else None


def parse_route(text: str) -> dict:
    """解析路由输出，返回 {intent, confidence, reason, entities}。

    优先按 XML 解析；非法/缺失时尝试 JSON；都失败降级 clarify（parse_failed）。
    """
    intent = extract("intent", text)
    if intent in {"mcp", "rag", "chat", "clarify"}:
        try:
            conf = float(extract("confidence", text) or 0.5)
        except ValueError:
            conf = 0.5
        return {
            "intent": intent,
            "confidence": conf,
            "reason": extract("reason", text) or "",
            "entities": {
                "symbol": extract("symbol", text) or "",
                "market": extract("market", text) or "",
                "date_range": extract("date_range", text) or "",
            },
        }
    try:
        d = json.loads(text[text.find("{"):text.rfind("}") + 1])
        return {**d, "confidence": float(d.get("confidence", 0.5))}
    except Exception:
        return {"intent": "clarify", "confidence": 0.0, "reason": "parse_failed"}
