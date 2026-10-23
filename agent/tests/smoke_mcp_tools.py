#!/usr/bin/env python
"""MCP 工具源联调冒烟：验证 FastMCP server 能被 adapters 拉起并正常调用。

用法（在 agent/ 下）：.venv\\Scripts\\python.exe -X utf8 tests\\smoke_mcp_tools.py
覆盖：
1. MCP 子进程能起、工具能列出（工具名与直连模式一致，共 10 个）；
2. 工具描述 / 参数 schema 能经 MCP 传递（LLM bind_tools 所需）；
3. 调用一次 symbol_search 验证 {"ok": ...} 信封归一（Go 网关未启动时应返回
   ok=False 的失败信封而不是抛异常）；
4. 关闭会话不报错。

注意：会真实拉起 stdio 子进程；symbol_search 依赖 Go 网关，未启动时该项按
「失败信封」判定（只要不是异常且信封结构正确即 PASS）。
"""
import asyncio
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
os.environ.setdefault("STOCK_MCP_ENABLED", "true")

from tools import mcp_loader  # noqa: E402

_FAILED = []


def check(name, cond, detail=""):
    print(("  PASS  " if cond else "  FAIL  ") + name + ("" if cond else "  " + str(detail)))
    if not cond:
        _FAILED.append(name)


async def main() -> int:
    tools = await mcp_loader.aload_stock_tools()
    names = sorted(t.name for t in tools)
    from tools import get_tools

    direct = sorted(t.name for t in get_tools())
    check(f"MCP 工具数 {len(names)}", len(names) == len(direct), names)
    check("工具名与直连一致", names == direct, names)
    check("确实走 MCP 源", mcp_loader._state is not None and mcp_loader._state[0] is not None)

    by_name = {t.name: t for t in tools}
    q = by_name.get("symbol_search")
    check("工具描述非空", bool(q and q.description))
    schema = (q.args_schema if isinstance(q.args_schema, dict)
              else q.args_schema.model_json_schema()) if q else {}
    props = list((schema.get("properties") or {}).keys())
    check("参数 schema 含 query/limit", "query" in props and "limit" in props, props)

    res = await q.ainvoke({"query": "贵州茅台", "limit": 2}) if q else {}
    check("调用返回 dict 信封", isinstance(res, dict) and "ok" in res,
          type(res).__name__ + " " + str(res)[:120])
    check("失败时带 error 字段", res.get("ok") is True or bool(res.get("error")), str(res)[:120])

    await mcp_loader.close_mcp_tools()
    print("\nALL PASSED" if not _FAILED else f"\nFAILED: {_FAILED}")
    return 0 if not _FAILED else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
