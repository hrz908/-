"""财务基本面工具（数据源：Tushare Pro）。

daily_basic（每日估值指标）与 fina_indicator（财务指标）都需要一定 Tushare 积分，
积分不足时 tushare_query 返回 {"ok": False, "error": ...}，由 call_tool_safe 透传，
不会抛异常打断对话。
"""
import asyncio
from datetime import datetime, timedelta

from langchain_core.tools import tool

from tools import call_tool_safe, take_latest, tushare_query, to_ts_code

# 估值工具一次最多回多少行：够回答「最近估值怎么走」，又不会把上下文撑大
_MAX_VALUATION_ROWS = 30
# 财务指标一次最多回多少行（每个报告期一行，约 40 个季度）
_MAX_FIN_ROWS = 40
# days 参数上限：防止模型要「十年估值」把上下文撑爆
_MAX_RANGE_DAYS = 365


def _date_window(days: int) -> tuple:
    """返回 (start_date, end_date)，按 days 收窄到最近 N 天。"""
    end = datetime.now()
    start = end - timedelta(days=max(1, min(int(days), _MAX_RANGE_DAYS)))
    return start.strftime("%Y%m%d"), end.strftime("%Y%m%d")


@tool
async def get_valuation(code: str, trade_date: str = "", days: int = 30) -> dict:
    """获取个股每日估值与交易指标：市盈率 PE(静态/TTM)、市净率 PB、市销率 PS、
    换手率、量比、股息率、总市值、流通市值。适合回答「贵不贵 / 估值多少」类问题。

    Args:
        code: 股票代码，如 "600519" 或 "600519.SH"。
        trade_date: 交易日期 YYYYMMDD，留空取最近一段。
        days: 留空 trade_date 时回看多少天（默认 30，上限 365）。

    Returns:
        {"ok": True, "data": [{...}]} 成功（按日期倒序，最多 30 行）；
        {"ok": False, "error": "..."} 失败（含积分不足、访问频率超限）。
    """
    params = {"ts_code": to_ts_code(code)}
    if trade_date:
        params["trade_date"] = trade_date
    else:
        # 必须显式给日期区间：只给 ts_code 时 Tushare 会返回该股全部历史
        #（茅台约 6000 行），整段 JSON 进对话会直接撑爆模型上下文。
        params["start_date"], params["end_date"] = _date_window(days)
    res = await call_tool_safe(
        tushare_query, "daily_basic", params,
        "ts_code,trade_date,close,turnover_rate,volume_ratio,pe,pe_ttm,pb,ps,ps_ttm,"
        "dv_ratio,dv_ttm,total_mv,circ_mv",
    )
    if res.get("ok"):
        res["data"] = take_latest(res.get("data"), "trade_date", _MAX_VALUATION_ROWS)
    return res


@tool
async def get_financial_indicator(code: str, period: str = "", limit: int = _MAX_FIN_ROWS) -> dict:
    """获取个股财务指标：每股收益 EPS、净资产收益率 ROE、销售毛利率、销售净利率、
    资产负债率、每股净资产、营收/净利润同比增速。适合基本面与盈利能力问答。

    Args:
        code: 股票代码。
        period: 报告期 YYYYMMDD（如 "20241231"），留空取最近若干期。
        limit: 最多返回多少个报告期（默认 40）。

    Returns:
        {"ok": True, "data": [{...}]} 成功；{"ok": False, "error": "..."} 失败。
    """
    params = {"ts_code": to_ts_code(code)}
    if period:
        params["period"] = period
    res = await call_tool_safe(
        tushare_query, "fina_indicator", params,
        "ts_code,end_date,eps,bps,roe,roe_dt,grossprofit_margin,netprofit_margin,"
        "debt_to_assets,or_yoy,netprofit_yoy",
    )
    if res.get("ok"):
        res["data"] = take_latest(res.get("data"), "end_date", max(1, min(int(limit), _MAX_FIN_ROWS)))
    return res


@tool
async def get_financial_report(code: str, period: str = "") -> dict:
    """一次性获取个股财报核心数据（组合工具）：财务指标 + 利润表 + 现金流量表。
    适合回答「业绩怎么样 / 财报数据 / 营收和利润」类问题；
    也是小程序「财报速览卡片」的数据源（卡片需要三张表拼齐才完整）。

    Args:
        code: 股票代码，如 "600519" 或 "600519.SH"。
        period: 报告期 YYYYMMDD（如 "20241231"），留空取最新一期。

    Returns:
        {"ok": True, "data": {"indicator": {...}|None, "income": {...}|None,
        "cashflow": {...}|None}}；三张表全部取不到才返回 {"ok": False, "error": ...}。
        单表失败只置 None，不影响其余表（积分不足时常见：部分表有权限、部分没有）。
    """
    params = {"ts_code": to_ts_code(code)}
    if period:
        params["period"] = period

    # 三个接口并发：财报卡要三张表，串行会把一次问答拖成三倍延迟
    indicator, income, cashflow = await asyncio.gather(
        call_tool_safe(
            tushare_query, "fina_indicator", dict(params),
            "ts_code,end_date,eps,roe,grossprofit_margin,netprofit_margin,or_yoy,netprofit_yoy",
        ),
        call_tool_safe(
            tushare_query, "income", dict(params),
            "ts_code,end_date,revenue,operate_cost,total_profit,n_income,n_income_attr_p",
        ),
        call_tool_safe(
            tushare_query, "cashflow", dict(params),
            "ts_code,end_date,n_cashflow_act",
        ),
    )

    def _first(res):
        # 每张表只取最新一期（Tushare 默认按报告期倒序返回）
        if not isinstance(res, dict) or not res.get("ok"):
            return None
        rows = res.get("data") or []
        return rows[0] if rows and isinstance(rows[0], dict) else None

    data = {
        "indicator": _first(indicator),
        "income": _first(income),
        "cashflow": _first(cashflow),
    }
    if all(v is None for v in data.values()):
        # 全失败：透传其中一个错误，保持与其它工具一致的降级结构
        err = next(
            (r.get("error") for r in (indicator, income, cashflow)
             if isinstance(r, dict) and not r.get("ok")),
            "no financial data",
        )
        return {"ok": False, "error": err}
    return {"ok": True, "data": data}
