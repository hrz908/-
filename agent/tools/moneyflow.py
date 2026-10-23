"""资金流工具（数据源：Tushare Pro）。

接口需要一定 Tushare 积分，积分不足时返回 {"ok": False, "error": ...}，
由 call_tool_safe 透传，不影响其它工具与主流程。
"""
from datetime import datetime, timedelta

from langchain_core.tools import tool

from tools import call_tool_safe, take_latest, tushare_query, to_ts_code

# 资金流一次最多回多少行（每天一行，30 行约一个多月）
_MAX_MONEYFLOW_ROWS = 30


@tool
async def get_money_flow(code: str, trade_date: str = "", days: int = 30) -> dict:
    """获取个股资金流向：小单/中单/大单/超大单的买入卖出金额与主力净流入。
    适合回答「资金在流入还是流出 / 主力动向」类问题。

    Args:
        code: 股票代码。
        trade_date: 交易日期 YYYYMMDD，留空取最近一段。
        days: 留空 trade_date 时回看多少天（默认 30，上限 365）。

    Returns:
        {"ok": True, "data": [{...}]} 成功（按日期倒序，最多 30 行）；
        {"ok": False, "error": "..."} 失败。
    """
    params = {"ts_code": to_ts_code(code)}
    if trade_date:
        params["trade_date"] = trade_date
    else:
        # 同 get_valuation：不给日期区间的话 Tushare 会把该股全部历史资金流返回，
        # 一次几千行会把对话上下文撑爆，这里限定到最近 N 天。
        end = datetime.now()
        start = end - timedelta(days=max(1, min(int(days), 365)))
        params["start_date"] = start.strftime("%Y%m%d")
        params["end_date"] = end.strftime("%Y%m%d")
    res = await call_tool_safe(
        tushare_query, "moneyflow", params,
        "ts_code,trade_date,buy_sm_amount,sell_sm_amount,buy_md_amount,sell_md_amount,"
        "buy_lg_amount,sell_lg_amount,buy_elg_amount,sell_elg_amount,net_mf_amount",
    )
    if res.get("ok"):
        res["data"] = take_latest(res.get("data"), "trade_date", _MAX_MONEYFLOW_ROWS)
    return res
