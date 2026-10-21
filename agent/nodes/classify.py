"""分类节点：意图路由。

用 small_llm + prompts/router.xml 输出 XML，parse_route 解析出 intent/confidence。
confidence < 0.6 强制降级为 clarify（宁可反问，不可乱答）。
解析失败(parse_route 已兜底)一律 clarify。

关键词兜底先行（agent-graph 规则 1）：行情/报价/K线/涨跌/估值/财报/资金类
问句命中即强制走 MCP 分支，LLM 判定只作为兜底之后的补充。
背景 bug：`查询 SH600519 的实时报价` 被判成 RAG 分支，没调任何行情工具，
直接引用无关文档作答。mcp.py 的 _DATA_KEYWORDS 只保护 MCP 分支内的 synthesize，
RAG 分支裸奔——这道闸补在路由层。
"""
import re
from datetime import datetime

from langchain_core.messages import HumanMessage

from llm import get_small_llm
from prompts.parse import render, parse_route
from nodes.rewrite import _format_history

_CONF_MIN = 0.6

# 数据类关键词：中文/混合词直接子串匹配（大小写不敏感，先 lower 再比）
_MCP_KEYWORDS_SUBSTR = (
    # 行情 / 价格类（含 agent-graph 规则 1 点名的「成交量」一族）。
    # 查询先 lower 再匹配，所以「日K/周K/月K」要写成小写形式
    "行情", "报价", "价格", "股价", "多少钱", "啥价", "涨跌", "涨幅", "收盘", "开盘",
    "最高", "最低", "k线", "日k", "周k", "月k", "年k", "走势",
    "成交量", "成交额", "换手", "量比", "振幅",
    # 估值 / 财务类
    "估值", "市盈率", "市净率", "市销率", "股息", "分红", "派息", "市值", "peg",
    "财报", "业绩", "营收", "净利润", "毛利", "净利", "roe", "eps",
    # 资金 / 榜单类
    "资金", "主力", "北向", "南向", "龙虎榜", "流入", "流出", "融资融券",
)
# 短英文指标整词匹配（词边界），防止 "apple" 命中 "pe"、"space" 命中 "ps"
_MCP_KEYWORDS_WORD = re.compile(r"(?<![a-zA-Z])(pe|pb|ps)(?![a-zA-Z])", re.IGNORECASE)
# RAG 强信号：想读文档（研报观点/解读）时不拦，交给 LLM 判读文档意图
_RAG_SIGNALS = ("研报", "观点", "解读", "投教", "知识库")


def _hit_data_keyword(query: str) -> bool:
    """数据类问句 -> True。短英文指标用词边界，中文/混合词用子串。"""
    q = (query or "").lower()
    if any(k in q for k in _MCP_KEYWORDS_SUBSTR):
        return True
    return bool(_MCP_KEYWORDS_WORD.search(q))


async def classify(state: dict) -> dict:
    # 护栏已拦截时不再分类，直接透传 clarify
    if state.get("error"):
        return {"intent": "clarify", "confidence": 0.0}

    query = state.get("rewritten") or state.get("query", "") or ""

    # 关键词兜底先行：命中数据词且无 RAG 强信号 -> 确定性进 MCP 分支，
    # 不再消耗一次 small_llm 调用，也不给 LLM 判错的机会
    if _hit_data_keyword(query) and not any(s in query for s in _RAG_SIGNALS):
        return {"intent": "mcp", "confidence": 1.0}

    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    history = _format_history(state.get("messages", []))

    prompt = render("router.xml", now=now, history=history, query=query)
    try:
        llm = get_small_llm(streaming=False, temperature=0.0)
        resp = await llm.ainvoke([HumanMessage(content=prompt)])
        route = parse_route(resp.content or "")
    except Exception:  # noqa: BLE001 - 分类失败降级 clarify
        route = {"intent": "clarify", "confidence": 0.0, "reason": "classify_error"}

    intent = route.get("intent", "clarify")
    confidence = float(route.get("confidence", 0.0) or 0.0)
    if intent not in {"mcp", "rag", "chat", "clarify"}:
        intent, confidence = "clarify", 0.0
    # 低置信度强制澄清
    if confidence < _CONF_MIN:
        intent = "clarify"

    return {"intent": intent, "confidence": confidence}
