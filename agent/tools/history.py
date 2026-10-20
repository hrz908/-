"""历史日线 K 线（含复权）工具（数据源：Tushare Pro）。

Tushare 的 daily 接口返回不复权价格，复权需配合 adj_factor 自行计算：
- 后复权 hfq：price × adj_factor
- 前复权 qfq：price × adj_factor / 区间内最新交易日的 adj_factor

daily / adj_factor 是免费积分档通常就能用的接口，作为现有 Go 网关实时行情的
历史数据补充（更长历史 + 规范复权）。
"""
from langchain_core.tools import tool

from tools import call_tool_safe, tushare_query, to_ts_code

# 需要复权的价格字段
_PRICE_FIELDS = ("open", "high", "low", "close", "pre_close")


def _apply_adjust(rows: list, factors: dict, adj: str) -> list:
    """按复权因子就地调整价格字段，返回按日期升序排列的结果。"""
    if not factors:
        return rows
    # 前复权基准：区间内最新交易日的因子
    latest = factors[max(factors)]
    out = []
    for r in rows:
        row = dict(r)
        f = factors.get(row.get("trade_date"))
        if f and latest:
            if adj == "qfq":
                scale = f / latest
            elif adj == "hfq":
                scale = f
            else:
                scale = 1.0
            for k in _PRICE_FIELDS:
                if row.get(k) is not None:
                    row[k] = round(float(row[k]) * scale, 4)
            row["adj"] = adj
        out.append(row)
    # daily 默认按日期倒序，转正序（旧 -> 新）更符合阅读与画线习惯
    out.sort(key=lambda x: x.get("trade_date", ""))
    return out


@tool
async def get_history_daily(code: str, start_date: str, end_date: str, adj: str = "qfq") -> dict:
    """获取个股历史日线行情（开高低收、涨跌幅、成交量额），支持前/后复权。
    数据规范、历史长，适合趋势分析、均线计算与回测取数。

    Args:
        code: 股票代码，如 "600519"。
        start_date: 起始日期 YYYYMMDD（如 "20250101"）。
        end_date: 结束日期 YYYYMMDD（如 "20251231"）。
        adj: 复权方式，qfq=前复权 / hfq=后复权 / ""=不复权，默认 qfq。

    Returns:
        {"ok": True, "data": [{trade_date,open,high,low,close,pct_chg,vol,amount}...]} 成功；
        {"ok": False, "error": "..."} 失败（含积分不足）。
    """
    ts_code = to_ts_code(code)

    async def _do() -> dict:
        res = await tushare_query(
            "daily",
            {"ts_code": ts_code, "start_date": start_date, "end_date": end_date},
            "ts_code,trade_date,open,high,low,close,pre_close,pct_chg,vol,amount",
        )
        # 不复权或没数据时直接返回
        if not res.get("ok") or adj == "" or not res.get("data"):
            return res
        af = await tushare_query(
            "adj_factor",
            {"ts_code": ts_code, "start_date": start_date, "end_date": end_date},
            "trade_date,adj_factor",
        )
        if not af.get("ok") or not af.get("data"):
            return res  # 拿不到复权因子就退回不复权，保证有数据可用
        factors = {
            r["trade_date"]: float(r["adj_factor"])
            for r in af["data"] if r.get("adj_factor") is not None
        }
        return {"ok": True, "data": _apply_adjust(res["data"], factors, adj)}

    # 复权时需串行调两个接口，放宽超时到 15s（默认 8s 对两次往返偏紧）
    return await call_tool_safe(_do, timeout=15.0)
