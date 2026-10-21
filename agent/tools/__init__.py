"""工具层公共能力：安全 HTTP 调用 + Go 网关响应解包。

call_tool_safe 统一封装「8s 超时 + 重试 1 次 + 失败降级」，
任何工具的网络/解析异常都不会冒泡到图里，而是返回 {"ok": False, "error": ...}。
go_get 复用共享 httpx.AsyncClient（连接池 + keep-alive），自行解 Go 信封。
"""
import asyncio
from typing import Any, Callable, Awaitable, Optional

import httpx

from config import (
    GO_SERVER_URL,
    GO_SERVER_TOKEN,
    MAX_TUSHARE_ROWS,
    TOOL_TIMEOUT,
    TUSHARE_TOKEN,
    TUSHARE_API,
)

# Go 网关统一响应体 { code, message, data } 里的成功码
_CODE_OK = 0

# 共享 HTTP 客户端：避免每次工具调用都新建连接
_client: Optional[httpx.AsyncClient] = None


def get_http_client() -> httpx.AsyncClient:
    global _client
    if _client is None:
        _client = httpx.AsyncClient(
            timeout=httpx.Timeout(TOOL_TIMEOUT, connect=5.0),
            limits=httpx.Limits(max_connections=100, max_keepalive_connections=20),
        )
    return _client


async def close_http_client() -> None:
    """进程退出时关闭共享客户端（可选调用）。"""
    global _client
    if _client is not None:
        await _client.aclose()
        _client = None


async def call_tool_safe(
    fn: Callable[..., Awaitable[Any]],
    *args,
    retries: int = 1,
    timeout: float = TOOL_TIMEOUT,
    **kwargs,
) -> dict:
    """执行一个异步工具函数，带超时与重试；失败返回降级结构而非抛异常。

    若 fn 本身已返回 {"ok": ...} 信封（如 go_get），直接透传不重复包裹。
    """
    last_err: Optional[str] = None
    for attempt in range(retries + 1):
        try:
            result = await asyncio.wait_for(fn(*args, **kwargs), timeout=timeout)
            if isinstance(result, dict) and "ok" in result:
                return result
            return {"ok": True, "data": result}
        except asyncio.TimeoutError:
            last_err = "timeout"
        except Exception as e:  # noqa: BLE001 - 工具失败必须被吞掉转降级
            last_err = f"{type(e).__name__}: {e}"
        # 重试前短暂退避
        if attempt < retries:
            await asyncio.sleep(0.3)
    return {"ok": False, "error": last_err or "unknown"}


def _auth_headers() -> dict:
    h = {"Accept": "application/json"}
    if GO_SERVER_TOKEN:
        h["Authorization"] = f"Bearer {GO_SERVER_TOKEN}"
    return h


async def go_get(path: str, params: Optional[dict] = None) -> dict:
    """GET Go 网关，自动拼 base_url、带鉴权头、解包 { code, data } 信封。

    返回统一结构：{"ok": True, "data": ...} 或 {"ok": False, "error": ...}。
    HTTP 层异常（非 2xx / 网络错误）仍抛出，由 call_tool_safe 兜底降级重试。
    """
    client = get_http_client()
    url = GO_SERVER_URL.rstrip("/") + path
    resp = await client.get(url, params=params or {}, headers=_auth_headers())
    resp.raise_for_status()
    data = resp.json()
    # 解 Go 信封 { code, message, data }
    if isinstance(data, dict) and "code" in data:
        if data.get("code") != _CODE_OK:
            return {"ok": False, "error": data.get("message", "unknown error")}
        return {"ok": True, "data": data.get("data")}
    return {"ok": True, "data": data}


def to_ts_code(code: str) -> str:
    """把股票代码转成 Tushare 的 ts_code 格式（600519 -> 600519.SH）。

    兼容项目里出现的多种写法："600519"、"SH600519"、"600519.SH"。
    已带 .SH/.SZ/.BJ 后缀的原样返回（统一大写）。
    """
    c = (code or "").strip().upper()
    if "." in c:
        return c
    for p in ("SH", "SZ", "BJ"):
        if c.startswith(p):
            c = c[len(p):]
            break
    if c.startswith("6"):
        return f"{c}.SH"
    if c.startswith(("0", "3")):
        return f"{c}.SZ"
    if c.startswith(("4", "8")):
        return f"{c}.BJ"
    return f"{c}.SH"


async def tushare_query(api_name: str, params: Optional[dict] = None, fields: str = "") -> dict:
    """调 Tushare Pro HTTP REST 接口，返回统一 {"ok":..., "data":[dict,...]} 结构。

    协议：POST，body {api_name, token, params, fields}；
    响应 {code, msg, data:{fields:[...], items:[[...]]}}，这里把 fields/items
    组装成 list[dict] 方便 LLM 直接读。
    积分不足 / 无权限时 Tushare 返回 code!=0，转为 {"ok": False, "error": msg}。
    HTTP 层异常（非 2xx / 网络错误）仍抛出，由 call_tool_safe 兜底降级重试。
    """
    if not TUSHARE_TOKEN:
        return {"ok": False, "error": "tushare token not configured"}
    payload: dict = {"api_name": api_name, "token": TUSHARE_TOKEN, "params": params or {}}
    if fields:
        payload["fields"] = fields
    client = get_http_client()
    resp = await client.post(TUSHARE_API, json=payload, headers={"Accept": "application/json"})
    resp.raise_for_status()
    body = resp.json()
    if body.get("code") != _CODE_OK:
        return {"ok": False, "error": body.get("msg") or "tushare error"}
    data = body.get("data") or {}
    cols = data.get("fields") or []
    rows = [dict(zip(cols, it)) for it in (data.get("items") or [])]
    # 行数硬上限：Tushare 的 daily / daily_basic / moneyflow 等接口在只给 ts_code
    # 时会返回该股全部历史（茅台约 6000 行），整段 JSON 进对话会撑爆模型上下文
    # （实测单次估值工具就产生 30 万 token，直接触发上游 max_seq_len 报错）。
    # 这些接口默认按日期倒序返回（最新的在最前），保留前 N 行即「最近 N 条」，
    # 不改变时序语义。真正的「取最近 N 条」由各工具自己用日期区间收窄。
    if len(rows) > MAX_TUSHARE_ROWS:
        rows = rows[:MAX_TUSHARE_ROWS]
    return {"ok": True, "data": rows}


def take_latest(rows: Any, key: str = "trade_date", limit: int = 30) -> list:
    """取按日期倒序的前 N 行（工具层统一的「只要最近若干条」出口）。

    Tushare 对部分接口不保证返回顺序，这里显式按日期字段排序后再截断，
    保证拿到的永远是「最近 N 条」而不是「随机 N 条」。
    """
    if not isinstance(rows, list):
        return []

    def _sort_key(r: dict) -> str:
        v = r.get(key)
        return str(v) if v is not None else ""

    valid = [r for r in rows if isinstance(r, dict)]
    valid.sort(key=_sort_key, reverse=True)
    return valid[:limit] if limit and limit > 0 else valid


def get_tools() -> list:
    """返回全部可供 LLM 绑定的工具（惰性导入，避免包内循环依赖）。"""
    from tools.quote import get_quote
    from tools.kline import get_kline
    from tools.search import symbol_search

    tools = [get_quote, get_kline, symbol_search]

    # Tushare 增强工具：仅在配置了 token 时注册，
    # 否则 LLM 选中后必然失败（白白消耗一次工具往返与降级）
    if TUSHARE_TOKEN:
        from tools.fundamental import (
            get_valuation,
            get_financial_indicator,
            get_financial_report,
        )
        from tools.moneyflow import get_money_flow, get_top_list, get_hsgt_flow
        from tools.news import get_stock_news
        from tools.history import get_history_daily

        tools += [
            get_valuation, get_financial_indicator, get_financial_report,
            get_money_flow, get_top_list, get_hsgt_flow,
            get_stock_news, get_history_daily,
        ]
    return tools
