"""股票工具源加载器：MCP Server（FastMCP，stdio 子进程）优先，进程内直连兜底。

设计（LangGraph 架构 §4.4 / 开放问题 1）：
- 取数工具统一由 mcp/stock_server.py 以 MCP 协议暴露（stdio 子进程，
  与 agent 同镜像、同环境变量），langchain-mcp-adapters 负责加载成 BaseTool；
- 任何一步失败（未装 adapters / 子进程起不来 / 列工具为空）都整段回退到
  tools.get_tools() 的进程内 @tool 直连——工具名、参数、{"ok": ...} 返回契约
  三者完全一致，上层 stock 子图无感知；
- 会话在进程生命周期内只建立一次（子进程里 akshare 导入较重，避免每轮请求
  重复拉起），main.py 退出时调 close_mcp_tools() 收尾。

版本注意：mcp python-sdk 必须钉在 1.x——2.x 移除了 RequestContext，
langchain-mcp-adapters 0.3.x 一导入就炸（requirements.txt 已锁定）。
"""
import json
import logging
import os
import sys

from config import STOCK_MCP_ENABLED, STOCK_MCP_SERVER_PATH

logger = logging.getLogger("stocka.agent")

# (close | None, tools)：close 非 None 表示走的是 MCP 会话，退出时要断开
_state = None


async def aload_stock_tools() -> list:
    """返回股票工具集（进程级缓存）。MCP 不可用时自动回退直连，绝不抛异常。"""
    global _state
    if _state is not None:
        return _state[1]
    client, tools = None, None
    if STOCK_MCP_ENABLED:
        try:
            client, tools = await _load_via_mcp()
            logger.info("stock tools loaded via MCP: %s", [t.name for t in tools])
        except Exception as e:  # noqa: BLE001 - MCP 源失败是预期内降级，不是故障
            logger.warning(
                "stock MCP source unavailable (%s: %s), fallback to in-process tools",
                type(e).__name__, e,
            )
            tools = None
    if tools is None:
        from tools import get_tools

        tools = get_tools()
    _state = (client, tools)
    return tools


async def _load_via_mcp():
    """拉起 stock MCP Server（stdio）并加载工具；失败抛异常由上层兜底。

    adapters 0.3.x 明确禁止把 MultiServerMCPClient 当 async 上下文管理器使用
    （直接 NotImplementedError），官方推荐的持久会话写法是 client.session() +
    load_mcp_tools()：会话手动保持到进程退出，避免每次工具调用重拉子进程。
    返回的 close 是可调用的异步收尾函数。
    """
    from langchain_mcp_adapters.client import MultiServerMCPClient
    from langchain_mcp_adapters.tools import load_mcp_tools

    client = MultiServerMCPClient({
        "stock-data": {
            "transport": "stdio",
            "command": sys.executable,
            "args": [STOCK_MCP_SERVER_PATH],
            "env": {**os.environ},
        },
    })
    session_cm = client.session("stock-data")

    async def _close() -> None:
        try:
            await session_cm.__aexit__(None, None, None)
        except Exception:  # noqa: BLE001 - 退出失败无需处理
            pass

    try:
        session = await session_cm.__aenter__()
        raw = await load_mcp_tools(session)
    except Exception:
        await _close()
        raise
    if not raw:
        await _close()
        raise RuntimeError("MCP server exposed no tools")
    return _close, [_wrap_mcp_tool(t) for t in raw]


def _wrap_mcp_tool(t):
    """把 MCP 工具包成返回 {"ok": ...} dict 的工具，与直连模式契约对齐。

    adapters 返回的 BaseTool.ainvoke 产出 MCP content（JSON 字符串或结构），
    stock 子图的 tool_execute / synthesize 只认 dict 信封，这里统一归一。
    """
    from langchain_core.tools import StructuredTool

    async def _coro(**kwargs):
        try:
            res = await t.ainvoke(kwargs)
        except Exception as e:  # noqa: BLE001 - MCP 调用失败转降级信封
            return {"ok": False, "error": f"{type(e).__name__}: {e}"}
        return _as_envelope(res)

    return StructuredTool(
        name=t.name,
        description=t.description or "",
        args_schema=t.args_schema,
        coroutine=_coro,
    )


def _as_envelope(res):
    """把 MCP 返回归一成 {"ok": ..., "data"/"error": ...} 信封。

    adapters 0.3.x 的返回形状依调用路径而变（实测）：
    - 成功：MCP content 列表 [{"type": "text", "text": '{"ok": true, ...}'}]；
    - 失败/部分路径：已解析好的 dict 信封。
    这里统一拆到文本层再解析 JSON，保证与直连工具的返回契约完全一致。
    """
    text = _extract_text(res)
    if text is not None:
        return _parse_envelope_text(text)
    if isinstance(res, dict):
        return res
    if res is None or isinstance(res, (list, int, float, bool)):
        return {"ok": True, "data": res}
    return {"ok": True, "data": {"text": str(res)}}


def _extract_text(res):
    """从 MCP content 结构里取出文本载荷；不是 content 结构时返回 None。"""
    if isinstance(res, str):
        return res
    if isinstance(res, dict):
        if res.get("type") == "text" and isinstance(res.get("text"), str):
            return res["text"]
        content = res.get("content")
        if isinstance(content, list):
            return _extract_text(content)
        return None
    if isinstance(res, list):
        parts = [p for p in (_extract_text(x) for x in res) if p]
        return chr(10).join(parts) or None
    return None


def _parse_envelope_text(text: str):
    """JSON 文本 -> 信封 dict；非 JSON 文本按纯文本装信封。"""
    s = text.strip()
    if s.startswith("{") or s.startswith("["):
        try:
            parsed = json.loads(s)
        except Exception:  # noqa: BLE001 - 非 JSON 文本按纯文本装信封
            parsed = None
        if isinstance(parsed, dict):
            return parsed
        if parsed is not None:
            return {"ok": True, "data": parsed}
    return {"ok": True, "data": {"text": s}}


async def close_mcp_tools() -> None:
    """进程退出时断开 MCP 会话（可选调用，失败静默）。"""
    global _state
    if _state is not None and callable(_state[0]):
        await _state[0]()
    _state = None
