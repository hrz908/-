"""龙虎榜席位明细工具（数据源：Tushare Pro `top_inst`）。

产品约定：资金流向卡片的龙虎榜只取三个量——席位、买入额、卖出额。
top_inst 按「方向」逐行返回（side=0 买入席位 / 1 卖出席位），这里把
两侧各取前 5 个席位，统一成前端资金流向卡 lhb 区块的行结构：
    [{seat, buy, sell}, ...]（金额单位：万元）

需要 2000 积分；无权限时返回 {"ok": False, "error": ...} 优雅降级，
由 mcp 子图的 _emit_structured 决定是否并入 fundflow 卡片。
"""
from langchain_core.tools import tool

from tools import call_tool_safe, take_latest, tushare_query, to_ts_code

# 买卖两侧各最多回多少个席位（前端区块展示密度有限，5 个足够）
_MAX_SEATS_PER_SIDE = 5


@tool
async def get_top_list(code: str, trade_date: str = "") -> dict:
    """获取个股龙虎榜席位明细：只取每个席位的买入额、卖出额（共三个量：席位/买入/卖出）。
    适合回答「这只股票上龙虎榜了吗 / 哪些营业部在买卖」类问题。

    Args:
        code: 股票代码，如 "600519"。
        trade_date: 交易日期 YYYYMMDD，留空取最近一个有数据的交易日。

    Returns:
        {"ok": True, "data": {trade_date, rows: [{seat, buy, sell}...]}} 成功
        （rows 按金额降序，买卖两侧各最多 5 席；金额单位万元）；
        {"ok": False, "error": "..."} 失败（含积分不足、当日未上榜）。
    """
    params = {"ts_code": to_ts_code(code)}
    if trade_date:
        params["trade_date"] = trade_date
    res = await call_tool_safe(
        tushare_query, "top_inst", params,
        "trade_date,side,bui_unit,sell_unit,buy,sell",
    )
    if not res.get("ok"):
        return res
    rows = res.get("data") or []
    if not rows:
        return {"ok": False, "error": "当日未上榜或暂无龙虎榜数据"}

    # 只看最近一个交易日的明细（模型带错日期时兜底取最新）
    trade_day = take_latest(rows, "trade_date", 1)
    day = trade_day[0].get("trade_date", "") if trade_day else ""

    buys, sells = [], []
    for r in rows:
        if r.get("trade_date") != day:
            continue
        if str(r.get("side")) == "0":  # 买入席位
            buys.append({"seat": str(r.get("bui_unit") or ""),
                         "buy": r.get("buy"), "sell": None})
        else:  # 卖出席位
            sells.append({"seat": str(r.get("sell_unit") or ""),
                          "buy": None, "sell": r.get("sell")})
    # 同侧按金额降序取前 5，三个量之外的维度一律不带
    buys.sort(key=lambda r: float(r.get("buy") or 0), reverse=True)
    sells.sort(key=lambda r: float(r.get("sell") or 0), reverse=True)
    merged = buys[:_MAX_SEATS_PER_SIDE] + sells[:_MAX_SEATS_PER_SIDE]
    if not merged:
        return {"ok": False, "error": "当日未上榜或暂无龙虎榜数据"}
    return {"ok": True, "data": {"trade_date": day, "rows": merged}}
