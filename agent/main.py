"""FastAPI 入口：以 ndjson 流式吐出 Agent 事件。

事件类型：
- token : {"event":"token","data":{"delta":"..."}}         仅来自回答生成节点
- card  : {"event":"card","data":{"type":..,"payload":..}}  结构化卡片
- table : {"event":"table","data":{"type":..,"payload":..}} 结构化表格
- citations: 引用来源（RAG）
- error : {"event":"error","data":{"message":"..."}}
- done  : {"event":"done","data":{"thread_id":..,"usage":{..}}}

运行: uvicorn main:app --host 0.0.0.0 --port 8000
"""
import json
import logging
from contextlib import asynccontextmanager
from typing import AsyncGenerator

from fastapi import FastAPI
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
from langchain_core.messages import HumanMessage

from state import initial_state
from graph import get_graph
from checkpointer import close_checkpointer

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("stocka.agent")

# 只有这些“回答生成”节点的 token 才允许流入 token 事件，
# 避免把 rewrite / classify / tool_call 等内部小模型输出泄露给前端。
_TOKEN_NODES = {"synthesize", "llm_stream", "generate", "clarify"}


@asynccontextmanager
async def lifespan(app: FastAPI):
    # 预热图与 checkpointer（建表 + 连接池）
    try:
        await get_graph()
    except Exception as e:  # noqa: BLE001 - 预热失败不阻断启动，首个请求会再试
        logger.warning("graph warmup failed: %s", e)
    yield
    await close_checkpointer()


app = FastAPI(title="StockA Agent", lifespan=lifespan)


class RunRequest(BaseModel):
    thread_id: str
    query: str
    user_id: str


def _line(event: str, data: dict) -> str:
    """序列化成一行 ndjson。"""
    return json.dumps({"event": event, "data": data}, ensure_ascii=False) + "\n"


async def event_generator(thread_id: str, query: str, user_id: str) -> AsyncGenerator[str, None]:
    usage: dict = {}
    try:
        graph = await get_graph()
        inp = initial_state(user_id=user_id, thread_id=thread_id, query=query)
        inp["messages"] = [HumanMessage(content=query)]
        config = {"configurable": {"thread_id": thread_id}}

        async for item in graph.astream(
            inp, config=config, stream_mode=["messages", "custom", "updates"]
        ):
            # 多模式下每个 item 是 (mode, data)
            if isinstance(item, tuple) and len(item) == 2 and isinstance(item[0], str):
                mode, data = item
            else:  # 兜底：单模式
                mode, data = "messages", item

            if mode == "messages":
                msg, meta = data
                node = meta.get("langgraph_node") if isinstance(meta, dict) else None
                if node in _TOKEN_NODES:
                    content = getattr(msg, "content", "")
                    if isinstance(content, str) and content:
                        yield _line("token", {"delta": content})

            elif mode == "custom":
                # data 已是 {"event":..,"data":..}（由 subgraphs.emit 产出），直接透传
                if isinstance(data, dict) and "event" in data:
                    yield json.dumps(data, ensure_ascii=False) + "\n"

            elif mode == "updates":
                if isinstance(data, dict):
                    post_update = data.get("post")
                    if isinstance(post_update, dict) and post_update.get("usage"):
                        usage = post_update["usage"]

    except Exception as e:  # noqa: BLE001 - 任何异常都转成 error 事件，不让流崩掉
        logger.exception("event_generator failed")
        yield _line("error", {"message": f"{type(e).__name__}: {e}"})

    finally:
        yield _line("done", {"thread_id": thread_id, "usage": usage})


@app.post("/run/stream")
async def run_stream(req: RunRequest):
    return StreamingResponse(
        event_generator(req.thread_id, req.query, req.user_id),
        media_type="application/x-ndjson",
    )


@app.get("/healthz")
async def health():
    return {"status": "ok"}
