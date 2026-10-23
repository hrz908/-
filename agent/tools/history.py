"""历史日线 K 线工具（数据源：Tushare Pro）。

产品约定：K 线走势只需要 15 个交易日数据。这里把窗口收死——
默认与上限都是 15 个交易日，模型传更大的 days 会被夹紧，
避免「拉十年日 K」把对话上下文与前端卡片同时撑爆。

Tushare 的 daily 接口返回不复权价格，复权需配合 adj_factor 自行计算：
- 后复权 hfq：price × adj_factor
- 前复权 qfq：price × adj_factor / 区间内最新交易日的 adj_factor

daily / adj_factor 是免费积分档通常就能用的接口，作为现有 Go 网关实时行情的
历史数据补充（更长历史 + 规范复权）。
"""
from datetime import datetime, timedelta

from langchain_core.tools import tool

from tools import call_tool_safe, take_latest, tushare_query, to_ts_code

# 需要复权的价格字段
_PRICE_FIELDS = ("open", "high", "low", "close", "pre_close")

# K 线窗口：默认与硬上限一致（产品约定：走势只需要 15 个交易日）
_KLINE_DAYS = 15

# 「N 个交易日」换算成自然日的放大系数 + 节假日余量。
# A 股节假日 + 周末约占自然日的 1/3，2 倍窗口再 +10 天足够兜住春节/国庆长假
_CALENDAR_FACTOR = 2
_CALENDAR_PAD = 10


def _kline_window(days: int, end_date: str) -> tuple:
    """返回 (start_date, end_date)：覆盖最近 days 个交易日的自然日窗口。"""
    end = datetime.now()
    if end_date:
        try:
            end = datetime.strptime(end_date, "%Y%m%d")
        except ValueError:
            pass  # 格式不对就退回今天，反正后面按行数再截
    span = max(1, days) * _CALENDAR_FACTOR + _CALENDAR_PAD
    start = end - timedelta(days=span)
    return start.strftime("%Y%m%d"), end.strftime("%Y%m%d")


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
async def get_history_daily(
    code: str, days: int = _KLINE_DAYS, adj: str = "qfq", end_date: str = ""
) -> dict:
    """获取个股最近 N 个交易日的日 K 线（开高低收、涨跌幅、成交量额），支持前/后复权。
    K 线走势只需要 15 个交易日数据：days 默认 15 且上限 15，更大的值会被截到 15。

    Args:
        code: 股票代码，如 "600519"。
        days: 返回最近多少个交易日，默认 15，上限 15（产品约定，传入更大的值无效）。
        adj: 复权方式，qfq=前复权 / hfq=后复权 / ""=不复权，默认 qfq。
        end_date: 截止日期 YYYYMMDD，留空取到今天。

    Returns:
        {"ok": True, "data": [{trade_date,open,high,low,close,pct_chg,vol,amount}...]}
        成功（按日期升序，最多 15 行）；{"ok": False, "error": "..."} 失败（含积分不足）。
    """
    ts_code = to_ts_code(code)
    # 产品约定：走势只要 15 个交易日，这里收死窗口而不是信任模型给的天数
    n = max(1, min(int(days) if days else _KLINE_DAYS, _KLINE_DAYS))
    start_date, end = _kline_window(n, end_date)

    async def _do() -> dict:
        res = await tushare_query(
            "daily",
            {"ts_code": ts_code, "start_date": start_date, "end_date": end},
            "ts_code,trade_date,open,high,low,close,pre_close,pct_chg,vol,amount",
        )
        if not res.get("ok") or not res.get("data"):
            return res
        # 窗口兜底截断：即使上游返回了更多行，也只保留最近 n 个交易日
        rows = take_latest(res["data"], "trade_date", n)
        if adj == "":
            rows.reverse()  # 不复权路径没走 _apply_adjust，手动转升序
            return {"ok": True, "data": rows}
        af = await tushare_query(
            "adj_factor",
            {"ts_code": ts_code, "start_date": start_date, "end_date": end},
            "trade_date,adj_factor",
        )
        if not af.get("ok") or not af.get("data"):
            # 拿不到复权因子就退回不复权，保证有数据可用
            rows.reverse()
            return {"ok": True, "data": rows}
        factors = {
            r["trade_date"]: float(r["adj_factor"])
            for r in af["data"] if r.get("adj_factor") is not None
        }
        if not factors:
            # 复权因子一行都解析不出：退回不复权并保证升序，别把倒序数据直接给下游
            rows.reverse()
            return {"ok": True, "data": rows}
        return {"ok": True, "data": _apply_adjust(rows, factors, adj)}

    # 复权时需串行调两个接口，放宽超时到 15s（默认 8s 对两次往返偏紧）
    return await call_tool_safe(_do, timeout=15.0)
