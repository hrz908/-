"""FastAPI 入口：以 ndjson 流式吐出 Agent 事件。

事件类型：
- token : {"event":"token","data":{"delta":"..."}}         仅来自回答生成节点
- card  : {"event":"card","data":{"type":..,"payload":..}}  结构化卡片
- table : {"event":"table","data":{"type":..,"payload":..}} 结构化表格
- sources: 引用来源（RAG，{"docs": [{title, chunk_id, score}]}）
- tool_start / tool_end: MCP 工具调用进度
- error : {"event":"error","data":{"message":"..."}}
- done  : {"event":"done","data":{"thread_id":..,"usage":{..}}}

运行: uvicorn main:app --host 0.0.0.0 --port 8000
"""
import json
import logging
import time
from contextlib import asynccontextmanager
from typing import AsyncGenerator

from fastapi import FastAPI
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
from langchain_core.messages import HumanMessage

from state import initial_state
from graph import get_graph
from checkpointer import close_checkpointer
from nodes import log_agent_run
from tools import close_http_client

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("stocka.agent")

@asynccontextmanager
async def lifespan(app: FastAPI):
    # 预热图与 checkpointer（建表 + 连接池）
    try:
        await get_graph()
    except Exception as e:  # noqa: BLE001 - 预热失败不阻断启动，首个请求会再试
        logger.warning("graph warmup failed: %s", e)
    yield
    await close_checkpointer()
    await close_http_client()


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
    t0 = time.monotonic()
    ttft_ms = None
    error_code = None
    graph = None
    config = {"configurable": {"thread_id": thread_id}}
    try:
        graph = await get_graph()
        inp = initial_state(user_id=user_id, thread_id=thread_id, query=query)
        inp["messages"] = [HumanMessage(content=query)]

        async for item in graph.astream(
            inp, config=config,
            stream_mode=["messages", "custom", "updates"],
            subgraphs=True,
        ):
            # subgraphs=True 时每个 item 是 (namespace, mode, data) 三元组：
            # mcp/rag/chat 子图内部的 token 与 card/table/tool 事件才会冒泡到父流，
            # 否则子图被当成单个节点、内部事件全部丢失（前端只收到 done）。
            # 同时兼容不含命名空间的 (mode, data) 二元组与单模式裸 data。
            if isinstance(item, tuple) and len(item) == 3:
                _ns, mode, data = item
            elif isinstance(item, tuple) and len(item) == 2 and isinstance(item[0], str):
                mode, data = item
            else:  # 兜底：单模式
                mode, data = "messages", item

            # token 事件由生成节点（chat/rag/mcp/clarify）在 astream 循环里
            # 显式 emit("token", ...) 经 custom 模式透传，避免与 messages 模式重复。
            if mode == "custom":
                # data 已是 {"event":..,"data":..}（由 subgraphs.emit 产出），直接透传
                if isinstance(data, dict) and "event" in data:
                    yield json.dumps(data, ensure_ascii=False) + "\n"

            elif mode == "updates":
                if isinstance(data, dict):
                    post_update = data.get("post")
                    if isinstance(post_update, dict) and post_update.get("usage"):
                        usage = post_update["usage"]
                    # 护栏/分类节点写入的 error 作为审计错误码
                    for node_update in data.values():
                        if isinstance(node_update, dict) and node_update.get("error"):
                            error_code = str(node_update["error"])[:100]

    except Exception as e:  # noqa: BLE001 - 任何异常都转成 error 事件，不让流崩掉
        logger.exception("event_generator failed")
        error_code = type(e).__name__
        yield _line("error", {"message": f"{type(e).__name__}: {e}"})

    finally:
        total_ms = int((time.monotonic() - t0) * 1000)
        # 审计落库：从 checkpointer 里的最终 state 提取字段，失败不阻断 done
        try:
            intent = confidence = None
            tool_calls = 0
            if graph is not None:
                snapshot = await graph.aget_state(config)
                values = (snapshot.values if snapshot else None) or {}
                intent = values.get("intent") or None
                confidence = values.get("confidence")
                tool_calls = len(values.get("tool_results") or [])
                if not usage and isinstance(values.get("usage"), dict):
                    usage = values["usage"]
            # agent_runs.user_id 是 BIGINT，非数字时置空避免插入报错
            try:
                uid = int(user_id)
            except (TypeError, ValueError):
                uid = None
            await log_agent_run(
                user_id=uid,
                thread_id=thread_id,
                intent=intent,
                confidence=confidence,
                tokens_in=usage.get("input_tokens", 0),
                tokens_out=usage.get("output_tokens", 0),
                ttft_ms=ttft_ms,
                total_ms=total_ms,
                tool_calls=tool_calls,
                error_code=error_code,
            )
        except Exception:  # noqa: BLE001
            logger.warning("agent run audit failed", exc_info=True)
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
