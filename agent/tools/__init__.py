"""工具层公共能力：安全 HTTP 调用 + Go 网关响应解包。

call_tool_safe 统一封装「8s 超时 + 重试 1 次 + 失败降级」，
任何工具的网络/解析异常都不会冒泡到图里，而是返回 {"ok": False, "error": ...}。
go_get 复用共享 httpx.AsyncClient（连接池 + keep-alive），自行解 Go 信封。

数据源结构（2026-10-23 起）：
- Go 网关（实时行情/搜索）：get_quote / symbol_search / get_peer_compare
- akshare（东财/新浪）：估值 / 资金流 / 财报 / 资讯 / 龙虎榜 / 机构研报
- baostock：历史日 K（15 个交易日窗口）
三者都不需要 token，工具全部无条件注册（原 Tushare 的积分门控已删除）。
2026-10-23 起本函数同时是 MCP 工具源（mcp_servers/stock_server.py 经
 tools/mcp_loader.py 加载）的兜底来源：MCP 子进程失败时整段回退到这里，
工具名 / 参数 / 返回契约保持一致。
"""
import asyncio
from typing import Any, Callable, Awaitable, Optional

import httpx

from config import (
    GO_SERVER_URL,
    GO_SERVER_TOKEN,
    TOOL_TIMEOUT,
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
    """把股票代码转成 Tushare 风格的 ts_code 格式（600519 -> 600519.SH）。

    Tushare 数据源已下线，但前端卡片 note 与 LLM 上下文仍用这个展示格式。
    兼容项目里出现的多种写法："600519"、"SH600519"、"600519.SH"。
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


def take_latest(rows: Any, key: str = "trade_date", limit: int = 30) -> list:
    """取按日期倒序的前 N 行（工具层统一的「只要最近若干条」出口）。

    数据源不保证返回顺序，这里显式按日期字段排序后再截断，
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
    """返回全部可供 LLM 绑定的工具（惰性导入，避免包内循环依赖）。

    所有数据源（Go 网关 / akshare / baostock）均免 token，无条件注册；
    个别接口失败时由 call_tool_safe 降级，不影响其余工具。
    """
    from tools.quote import get_quote
    from tools.search import symbol_search
    from tools.compare import get_peer_compare
    from tools.fundamental import (
        get_valuation,
        get_financial_indicator,
        get_financial_report,
    )
    from tools.moneyflow import get_money_flow
    from tools.news import get_stock_news
    from tools.history import get_history_daily
    from tools.lhb import get_top_list
    from tools.research import get_research_reports

    return [
        get_quote, symbol_search, get_peer_compare,
        get_valuation, get_financial_indicator, get_financial_report,
        get_money_flow, get_stock_news, get_history_daily, get_top_list,
        get_research_reports,
    ]
