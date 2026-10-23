"""个股 / 财经资讯工具（数据源：Tushare Pro）。

产品约定：公司动态只取当日信息——不给时间范围时窗口固定为今天 00:00 到现在，
不再回看历史。带 code 时会先解析股票名称、只保留正文提到该股的新闻，
避免把市场级快讯当公司动态回答。

注意：news 接口通常需要较高 Tushare 积分 / 单独权限，免费档大概率无权限，
此时返回 {"ok": False, "error": "..."}，不影响其它工具与主流程。
"""
import re
from datetime import datetime

from langchain_core.tools import tool

from tools import call_tool_safe, go_get, take_latest, tushare_query

# 资讯一次最多回多少条
_MAX_NEWS_ROWS = 20


def _today_window() -> tuple:
    """当日窗口：今天 00:00:00 到现在（产品约定：公司动态仅取当日信息）。"""
    now = datetime.now()
    start = now.replace(hour=0, minute=0, second=0, microsecond=0)
    return start.strftime("%Y-%m-%d %H:%M:%S"), now.strftime("%Y-%m-%d %H:%M:%S")


async def _stock_keywords(code: str) -> list:
    """把股票代码解析成正文匹配关键词：股票名称 + 6 位代码。

    名称来自 Go 网关行情快照（同一数据源，一次调用）；解析失败返回空列表，
    调用方跳过过滤（诚实返回市场快讯，好过把结果清空显得「没新闻」）。
    """
    try:
        res = await call_tool_safe(go_get, "/api/v1/stock/quote", {"code": code})
    except Exception:  # noqa: BLE001 - 名称解析失败不阻断主流程
        return []
    if not isinstance(res, dict) or not res.get("ok"):
        return []
    data = res.get("data") or {}
    name = str(data.get("name") or "").strip()
    digits = re.sub(r"\D", "", code)
    return [k for k in {name, digits} if k]


@tool
async def get_stock_news(
    code: str = "", start_time: str = "", end_time: str = "",
    src: str = "sina", limit: int = _MAX_NEWS_ROWS,
) -> dict:
    """获取财经新闻快讯（时间、标题、内容、来源），公司动态只看当日。
    个股动态必须带 code：只保留正文提到该股（名称或代码）的新闻。

    Args:
        code: 股票代码（如 "600519"），带此参数时按当日 + 个股过滤。
        start_time: 起始时间 "YYYY-MM-DD HH:MM:SS"，留空取当日 00:00 起。
        end_time: 结束时间 "YYYY-MM-DD HH:MM:SS"，留空取到当前时刻。
        src: 来源，可选 sina / wallstreetcn / 10jqka / eastmoney / yuncaijing，默认 sina。
        limit: 最多返回多少条（默认 20）。

    Returns:
        {"ok": True, "data": [{datetime,title,content,src}...]} 成功；失败返回 error。
    """
    if not start_time and not end_time:
        # 必须给时间范围：news 不给区间会返回全部历史新闻（成千上万条），
        # 正文进对话会直接撑爆上下文。默认窗口 = 当日（产品约定）
        params_s, params_e = _today_window()
    else:
        params_s, params_e = start_time, end_time
    params = {"src": src, "start_time": params_s, "end_time": params_e}

    res = await call_tool_safe(
        tushare_query, "news", params,
        "datetime,content,title,channels,src",
    )
    if res.get("ok"):
        rows = take_latest(res.get("data"), "datetime", max(1, min(int(limit), _MAX_NEWS_ROWS)))
        if code:
            kws = await _stock_keywords(code)
            if kws:
                rows = [
                    r for r in rows
                    if any(k in f"{r.get('title', '')}{r.get('content', '')}" for k in kws)
                ]
        res["data"] = rows
    return res
