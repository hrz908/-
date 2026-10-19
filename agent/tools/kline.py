"""K 线数据工具。"""
from langchain_core.tools import tool

from tools import call_tool_safe, go_get


@tool
async def get_kline(code: str, period: str = "day", count: int = 60) -> dict:
    """获取指定股票的 K 线（历史行情）数据。

    Args:
        code: 股票代码，如 "600519"。
        period: 周期，可选 "day"/"week"/"month"/"60"/"30"/"15"/"5"/"1"，默认 "day"。
        count: 返回的 K 线根数，默认 60。

    Returns:
        {"ok": True, "data": [...]} 成功；{"ok": False, "error": "..."} 失败降级。
    """
    params = {"code": code, "period": period, "count": count}
    return await call_tool_safe(go_get, "/api/v1/stock/kline", params)
