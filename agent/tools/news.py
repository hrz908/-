"""个股 / 财经资讯工具（数据源：Tushare Pro）。

注意：news 接口通常需要较高 Tushare 积分 / 单独权限，免费档大概率无权限，
此时返回 {"ok": False, "error": "..."}，不影响其它工具与主流程。
"""
from datetime import datetime, timedelta

from langchain_core.tools import tool

from tools import call_tool_safe, take_latest, tushare_query

# 资讯一次最多回多少条
_MAX_NEWS_ROWS = 20
# 不给时间范围时默认回看几天（news 不给区间会返回全部历史，体量极大）
_DEFAULT_NEWS_DAYS = 3


@tool
async def get_stock_news(start_time: str = "", end_time: str = "", src: str = "sina",
                         limit: int = _MAX_NEWS_ROWS) -> dict:
    """获取财经新闻快讯（时间、标题、内容、来源）。适合回答「最新消息 / 资讯」类问题。

    Args:
        start_time: 起始时间 "YYYY-MM-DD HH:MM:SS"，留空取最近几天。
        end_time: 结束时间 "YYYY-MM-DD HH:MM:SS"。
        src: 来源，可选 sina / wallstreetcn / 10jqka / eastmoney / yuncaijing，默认 sina。
        limit: 最多返回多少条（默认 20）。

    Returns:
        {"ok": True, "data": [{datetime,title,content,src}...]} 成功；失败返回 error。
    """
    params = {"src": src}
    if not start_time and not end_time:
        # 必须给时间范围：news 不给区间会返回全部历史新闻（成千上万条），
        # 正文进对话会直接撑爆上下文。
        end = datetime.now()
        start = end - timedelta(days=_DEFAULT_NEWS_DAYS)
        params["start_time"] = start.strftime("%Y-%m-%d %H:%M:%S")
        params["end_time"] = end.strftime("%Y-%m-%d %H:%M:%S")
    else:
        if start_time:
            params["start_time"] = start_time
        if end_time:
            params["end_time"] = end_time
    res = await call_tool_safe(
        tushare_query, "news", params,
        "datetime,content,title,channels,src",
    )
    if res.get("ok"):
        res["data"] = take_latest(res.get("data"), "datetime", max(1, min(int(limit), _MAX_NEWS_ROWS)))
    return res
