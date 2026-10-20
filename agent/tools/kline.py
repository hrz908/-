"""K 线数据工具。"""
from langchain_core.tools import tool

from tools import call_tool_safe, go_get


@tool
async def get_kline(code: str, period: str = "day", count: int = 10) -> dict:
    """获取指定股票的 K 线（历史行情）数据。

    Args:
        code: 股票代码，如 "600519"。
        period: 周期，可选 "day"/"week"/"month"/"60"/"30"/"15"/"5"/"1"，默认 "day"。
        count: 返回的 K 线根数，默认 10。

    Returns:
        {"ok": True, "data": [...]} 成功；{"ok": False, "error": "..."} 失败降级。
    """
    # 默认只给最近 10 根：一次 60 根既撑爆对话卡片（前端逐行渲染），
    # 也会把整段 JSON 塞进上下文；用户明确要更长区间时由模型传更大的 count
    params = {"code": code, "period": period, "count": count}
    return await call_tool_safe(go_get, "/api/v1/stock/kline", params)
