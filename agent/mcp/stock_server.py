"""股票数据 MCP Server（FastMCP，stdio 传输）。

把 agent/tools 的 10 个取数工具原样暴露为 MCP 工具，供
tools/mcp_loader.py（langchain-mcp-adapters）加载：
- 工具名 / 参数签名 / docstring 与进程内直连模式完全一致，上层无感知；
- 返回统一 {"ok": True, "data": ...} / {"ok": False, "error": ...} 信封，
  JSON 可序列化（MCP content 走 JSON 文本）。

启动：`python mcp/stock_server.py`——通常由 mcp_loader 以 stdio
子进程拉起（同镜像、同环境变量，无需额外端口）；也可手动运行配合 MCP
Inspector 调试。部署形态延续设计文档开放问题 1 的 stdio 方案。
"""
import functools
import sys
from pathlib import Path

# 脚本方式运行时 sys.path 里只有 mcp/，补上 agent 根目录才能 import tools
_AGENT_DIR = str(Path(__file__).resolve().parent.parent)
if _AGENT_DIR not in sys.path:
    sys.path.insert(0, _AGENT_DIR)

from mcp.server.fastmcp import FastMCP  # noqa: E402 - 需先补 sys.path

from tools import get_tools  # noqa: E402

mcp = FastMCP("stock-data")


def _register(tool_obj) -> None:
    """把进程内 @tool 注册为同名 MCP 工具（签名 / 文档 / 返回原样透传）。

    functools.wraps 复制原函数的 __doc__/__annotations__/__wrapped__，
    FastMCP 据此推导工具描述与参数 schema，无需手工同步两份定义。
    """
    fn = tool_obj.coroutine or tool_obj.func

    @functools.wraps(fn)
    async def _wrapped(*args, **kwargs):
        try:
            return await fn(*args, **kwargs)
        except Exception as e:  # noqa: BLE001 - 工具失败必须降级为信封，不让 MCP 报错
            return {"ok": False, "error": f"{type(e).__name__}: {e}"}

    mcp.tool(name=tool_obj.name, description=tool_obj.description or "")(_wrapped)


for _t in get_tools():
    _register(_t)


if __name__ == "__main__":
    mcp.run()
