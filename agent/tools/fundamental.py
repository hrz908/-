"""财务基本面工具（数据源：Tushare Pro）。

daily_basic（每日估值指标）与 fina_indicator（财务指标）都需要一定 Tushare 积分，
积分不足时 tushare_query 返回 {"ok": False, "error": ...}，由 call_tool_safe 透传，
不会抛异常打断对话。
"""
from langchain_core.tools import tool

from tools import call_tool_safe, tushare_query, to_ts_code


@tool
async def get_valuation(code: str, trade_date: str = "") -> dict:
    """获取个股每日估值与交易指标：市盈率 PE(静态/TTM)、市净率 PB、市销率 PS、
    换手率、量比、股息率、总市值、流通市值。适合回答「贵不贵 / 估值多少」类问题。

    Args:
        code: 股票代码，如 "600519" 或 "600519.SH"。
        trade_date: 交易日期 YYYYMMDD，留空取最新。

    Returns:
        {"ok": True, "data": [{...}]} 成功；{"ok": False, "error": "..."} 失败（含积分不足）。
    """
    params = {"ts_code": to_ts_code(code)}
    if trade_date:
        params["trade_date"] = trade_date
    return await call_tool_safe(
        tushare_query, "daily_basic", params,
        "ts_code,trade_date,close,turnover_rate,volume_ratio,pe,pe_ttm,pb,ps,ps_ttm,"
        "dv_ratio,dv_ttm,total_mv,circ_mv",
    )


@tool
async def get_financial_indicator(code: str, period: str = "") -> dict:
    """获取个股财务指标：每股收益 EPS、净资产收益率 ROE、销售毛利率、销售净利率、
    资产负债率、每股净资产、营收/净利润同比增速。适合基本面与盈利能力问答。

    Args:
        code: 股票代码。
        period: 报告期 YYYYMMDD（如 "20241231"），留空取最新若干期。

    Returns:
        {"ok": True, "data": [{...}]} 成功；{"ok": False, "error": "..."} 失败。
    """
    params = {"ts_code": to_ts_code(code)}
    if period:
        params["period"] = period
    return await call_tool_safe(
        tushare_query, "fina_indicator", params,
        "ts_code,end_date,eps,bps,roe,roe_dt,grossprofit_margin,netprofit_margin,"
        "debt_to_assets,or_yoy,netprofit_yoy",
    )
