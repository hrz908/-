"""个股 / 财经资讯工具（数据源：Tushare Pro）。

注意：news 接口通常需要较高 Tushare 积分 / 单独权限，免费档大概率无权限，
此时返回 {"ok": False, "error": "..."}，不影响其它工具与主流程。
"""
from langchain_core.tools import tool

from tools import call_tool_safe, tushare_query


@tool
async def get_stock_news(start_time: str = "", end_time: str = "", src: str = "sina") -> dict:
    """获取财经新闻快讯（时间、标题、内容、来源）。适合回答「最新消息 / 资讯」类问题。

    Args:
        start_time: 起始时间 "YYYY-MM-DD HH:MM:SS"，留空取最近。
        end_time: 结束时间 "YYYY-MM-DD HH:MM:SS"。
        src: 来源，可选 sina / wallstreetcn / 10jqka / eastmoney / yuncaijing，默认 sina。

    Returns:
        {"ok": True, "data": [{datetime,title,content,src}...]} 成功；失败返回 error。
    """
    params = {"src": src}
    if start_time:
        params["start_time"] = start_time
    if end_time:
        params["end_time"] = end_time
    return await call_tool_safe(
        tushare_query, "news", params,
        "datetime,content,title,channels,src",
    )
