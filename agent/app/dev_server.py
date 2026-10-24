"""本地开发启动器（Windows 专用）：用 SelectorEventLoop 起 uvicorn。

背景：psycopg 3 的 async 模式不支持 Windows 默认的 ProactorEventLoop，
而 uvicorn 0.54 在 Windows 上会硬编码 `asyncio.ProactorEventLoop`
（`uvicorn.loops.asyncio.asyncio_loop_factory`），连 asyncio 策略都不认。
因此这里绕开 `Server.run()`（它先 setup_event_loop 再 asyncio.run），
直接在自己的 SelectorEventLoop 里 `await server.serve()`。

Docker/Linux 无此问题，生产仍用 `uvicorn app.main:app --host 0.0.0.0 --port 8000`
（见 Dockerfile），不需要本脚本。

用法（在 agent/ 目录下，必须走 -m 才能让 `app.main` 可导入）：
    .venv/Scripts/python.exe -X utf8 -m app.dev_server [--port 8000]
"""
import asyncio
import selectors
import sys

import uvicorn


def _port() -> int:
    if "--port" in sys.argv:
        i = sys.argv.index("--port") + 1
        if i < len(sys.argv) and sys.argv[i].isdigit():
            return int(sys.argv[i])
    return 8000


def main() -> None:
    config = uvicorn.Config("app.main:app", host="127.0.0.1", port=_port(), log_level="info")
    server = uvicorn.Server(config)
    if sys.platform == "win32":
        # 直接调 serve()，避开 Server.run() 内部硬编码的 ProactorEventLoop
        factory = lambda: asyncio.SelectorEventLoop(selectors.SelectSelector())  # noqa: E731
        asyncio.run(server.serve(), loop_factory=factory)
    else:
        asyncio.run(server.serve())


if __name__ == "__main__":
    main()
