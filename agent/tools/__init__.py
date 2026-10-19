"""工具层公共能力：安全 HTTP 调用 + Go 网关响应解包。

call_tool_safe 统一封装「8s 超时 + 重试 1 次 + 失败降级」，
任何工具的网络/解析异常都不会冒泡到图里，而是返回 {"ok": False, "error": ...}。
go_get 复用共享 httpx.AsyncClient（连接池 + keep-alive），自行解 Go 信封。
"""
import asyncio
from typing import Any, Callable, Awaitable, Optional

import httpx

from config import GO_SERVER_URL, GO_SERVER_TOKEN, TOOL_TIMEOUT

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


def get_tools() -> list:
    """返回全部可供 LLM 绑定的工具（惰性导入，避免包内循环依赖）。"""
    from tools.quote import get_quote
    from tools.kline import get_kline
    from tools.search import symbol_search

    return [get_quote, get_kline, symbol_search]
