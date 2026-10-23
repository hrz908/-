"""分类节点：意图路由。

用 small_llm + prompts/router.xml 输出意图与置信度，优先走结构化输出
（with_structured_output），失败时回退 XML + parse_route。confidence < 0.6
强制降级为 clarify（宁可反问，不可乱答）。解析失败一律 clarify。

意图三分（LangGraph 主图，2026-10-23 起）：
- stock: 一切股票相关问题，统一先进 RAG 漏斗——检索知识库命中则带引用作答，
  未命中由主图把 intent 改写成 stock_agent 重路由进炒股 agent（工具取数）。
- chat: 闲聊寒暄，直接陪聊，不挂工具。
- off_topic: 与股票无关的正经话题，走固定话术婉拒（nodes/refuse.py）。
- clarify: 问题含糊或低置信度，反问澄清。

关键词兜底先行（agent-graph 规则 1）：行情/报价/K线/涨跌/估值/财报/资金类
问句命中即确定性判为 stock——既省一次路由 LLM 调用，也防 LLM 把取数问句
判错；漏斗不会拿无关文档硬答，因为 rag 子图还有 LLM 判读把门。
"""
import re
from datetime import datetime
from pydantic import BaseModel, Field

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
    # 资金 / 榜单 / 同业对比 / 资讯类
    "资金", "主力", "龙虎榜", "流入", "流出",
    "同业", "同行", "竞品", "对比", "新闻", "动态", "消息",
    # 注意：北向/南向/融资融券已移除——对应工具 2026-10-22 下线，
    # 关键词留在表里会把这些问句路由进 MCP 却无工具可调（死路由）
)
# 短英文指标整词匹配（词边界），防止 "apple" 命中 "pe"、"space" 命中 "ps"
_MCP_KEYWORDS_WORD = re.compile(r"(?<![a-zA-Z])(pe|pb|ps)(?![a-zA-Z])", re.IGNORECASE)


class _RouteDecision(BaseModel):
    """意图路由的结构化输出契约（与 router.xml 的 <route> 字段一一对应）。"""
    intent: str = Field(description="stock|chat|off_topic|clarify 之一")
    confidence: float = Field(description="对该判断的把握，0~1 的小数")
    reason: str = Field(default="", description="一句话理由")
    symbol: str = Field(default="", description="标的代码或名称，可空")
    market: str = Field(default="", description="SH|SZ|BJ|HK|US，可空")
    date_range: str = Field(default="", description="时间范围描述，可空")


async def _route_decision(prompt: str) -> dict:
    """意图判定：优先用结构化输出（with_structured_output），失败回退 XML 解析。

    两条路径都收敛到同一个 dict 形态（intent / confidence / entities），
    保证下游状态写入口径一致；任一路径异常都安全降级为 clarify。
    """
    small = get_small_llm(streaming=False, temperature=0.0)
    # 主路径：结构化输出，参数传递更稳、解析更可靠
    try:
        decision = await small.with_structured_output(_RouteDecision).ainvoke(
            [HumanMessage(content=prompt)]
        )
        return {
            "intent": decision.intent,
            "confidence": float(decision.confidence or 0.0),
            "entities": {
                "symbol": decision.symbol or "",
                "market": decision.market or "",
                "date_range": decision.date_range or "",
            },
        }
    except Exception:  # noqa: BLE001 - 结构化不可用（模型/版本）时退回 XML
        pass
    # 回退路径：沿用 XML + parse_route，保证兼容性与健壮性
    try:
        resp = await small.ainvoke([HumanMessage(content=prompt)])
        r = parse_route(resp.content or "")
    except Exception:  # noqa: BLE001
        return {"intent": "clarify", "confidence": 0.0, "entities": {}}
    return {
        "intent": r.get("intent", "clarify"),
        "confidence": float(r.get("confidence", 0.0) or 0.0),
        "entities": r.get("entities", {}),
    }


def _hit_data_keyword(query: str) -> bool:
    """数据类问句 -> True。短英文指标用词边界，中文/混合词用子串。"""
    q = (query or "").lower()
    if any(k in q for k in _MCP_KEYWORDS_SUBSTR):
        return True
    return bool(_MCP_KEYWORDS_WORD.search(q))


async def classify(state: dict) -> dict:
    # 护栏已拦截时不再分类，直接透传 clarify
    if state.get("error"):
        return {"intent": "clarify", "confidence": 0.0, "entities": {}}

    query = state.get("rewritten") or state.get("query", "") or ""

    # 关键词兜底先行：命中数据词 -> 确定性判为 stock（漏斗内部再决定查文档还是取数），
    # 不再消耗一次 small_llm 调用，也不给 LLM 判错的机会
    if _hit_data_keyword(query):
        return {"intent": "stock", "confidence": 1.0, "entities": {}}

    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    history = _format_history(state.get("messages", []))

    prompt = render("router.xml", now=now, history=history, query=query)

    route = await _route_decision(prompt)
    intent = route["intent"]
    confidence = route["confidence"]
    if intent not in {"stock", "chat", "off_topic", "clarify"}:
        intent, confidence = "clarify", 0.0
    # 低置信度强制澄清
    if confidence < _CONF_MIN:
        intent = "clarify"

    return {"intent": intent, "confidence": confidence, "entities": route["entities"]}
