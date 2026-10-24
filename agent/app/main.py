"""FastAPI 入口：以 ndjson 流式吐出 Agent 事件。

事件类型：
- token   : {"event":"token","data":{"delta":"..."}}          仅来自回答生成节点
- card    : {"event":"card","data":{"type":..,"payload":..}}   结构化卡片
- table   : {"event":"table","data":{"type":..,"payload":..}}  结构化表格
- sources : RAG 引用来源，{"docs":[{title, chunk_id, score}]}
- tool_start / tool_end : 工具调用进度（MCP 子进程 / 进程内直连）
- error   : {"event":"error","data":{"message":"..."}}
- done    : {"event":"done","data":{"thread_id":..,"usage":{..}}}

会话历史接口（供小程序「历史对话」抽屉，经 Go 网关转发）：
- GET    /sessions?user_id=                 会话列表（标题 + 最近活跃）
- POST   /sessions                          注册新对话占位 / 每轮 run upsert
- GET    /sessions/{sid}/messages?user_id=  恢复某会话的纯文本消息
- DELETE /sessions/{sid}?user_id=           删除会话（注册行 + checkpoints + 摘要）
所有接口都校验 thread_id 归属（default:user:session），防越权访问他人会话。

运行: uvicorn main:app --host 0.0.0.0 --port 8000
"""
import json
import logging
import time
from contextlib import asynccontextmanager
from typing import AsyncGenerator

from fastapi import FastAPI, HTTPException
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
from langchain_core.messages import HumanMessage

from core.state import initial_state
from core.graph import get_graph
from core.checkpointer import close_checkpointer, prune_thread_checkpoints
from nodes import log_agent_run
from core.sessions import (
    delete_session,
    ensure_sessions_table,
    list_sessions,
    session_messages,
    thread_id_of,
    upsert_session,
    valid_session_id,
)
from tools import close_http_client

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("stocka.agent")


def _check_ownership(user_id: str, session_id: str) -> str:
    """校验 session_id 合法性并拼出 thread_id，非法一律 400。

    Go 网关的 user_id 来自 JWT（不可伪造），这里再补一层防御：
    thread_id 必须等于 default:{user_id}:{session_id}，从结构上杜绝
    用他人 session_id 读写别人会话的可能。
    """
    if not (user_id or "").strip() or not valid_session_id(session_id):
        raise HTTPException(status_code=400, detail="invalid user_id or session_id")
    return thread_id_of(user_id.strip(), session_id)

@asynccontextmanager
async def lifespan(app: FastAPI):
    # 预热图与 checkpointer（建表 + 连接池）
    try:
        await get_graph()
    except Exception as e:  # noqa: BLE001 - 预热失败不阻断启动，首个请求会再试
        logger.warning("graph warmup failed: %s", e)
    # 会话注册表（chat_sessions）：历史对话列表的存储，幂等建表
    try:
        await ensure_sessions_table()
    except Exception as e:  # noqa: BLE001 - 建表失败不阻断启动，接口侧会懒建重试
        logger.warning("ensure chat_sessions failed: %s", e)
    yield
    await close_checkpointer()
    await close_http_client()
    # MCP 工具会话（stdio 子进程）退出时断开，失败静默
    try:
        from tools.mcp_loader import close_mcp_tools
        await close_mcp_tools()
    except Exception:  # noqa: BLE001 - 收尾失败不影响进程退出
        pass


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
        # 每轮 run 前裁掉该线程的历史 checkpoint 版本：PostgresSaver 不回收旧版本，
        # 不裁则单线程行数随 run 数线性增长；失败静默，不影响本轮对话
        await prune_thread_checkpoints(thread_id)
        inp = initial_state(user_id=user_id, thread_id=thread_id, query=query)
        inp["messages"] = [HumanMessage(content=query)]

        async for item in graph.astream(
            inp, config=config,
            stream_mode=["messages", "custom", "updates"],
            subgraphs=True,
        ):
            # subgraphs=True 时 item 是 (namespace, mode, data) 三元组：只有带上它，
            # stock/rag/chat 子图内部的 token 与 card/table/tool 事件才会冒泡到父流，
            # 否则子图被当成单个节点、内部事件全丢（前端只收到 done）；
            # 同时兼容不含命名空间的 (mode, data) 二元组与单模式裸 data
            if isinstance(item, tuple) and len(item) == 3:
                _ns, mode, data = item
            elif isinstance(item, tuple) and len(item) == 2 and isinstance(item[0], str):
                mode, data = item
            else:  # 兜底：单模式
                mode, data = "messages", item

            # token 由生成节点（chat/rag/stock/refuse/clarify）在 astream 循环里
            # 显式 emit("token", ...) 经 custom 模式透传，避免与 messages 模式重复
            if mode == "custom":
                # data 已是 {"event":..,"data":..}（由 subgraphs.emit 产出），直接透传
                if isinstance(data, dict) and "event" in data:
                    # 首个对用户可见的事件即 TTFT（首 token 或首张卡片）；
                    # 该审计字段此前恒为 NULL，在此补齐
                    if ttft_ms is None:
                        ttft_ms = int((time.monotonic() - t0) * 1000)
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
        # 审计落库：从 checkpointer 的最终 state 提取字段，失败不阻断 done
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


# ===== 会话历史（历史对话抽屉）=====

class SessionUpsert(BaseModel):
    user_id: str
    session_id: str
    title: str = ""


@app.get("/sessions")
async def get_sessions(user_id: str):
    """会话列表：按最近活跃倒序；user_id 由 Go 网关从 JWT 解析后注入。"""
    if not (user_id or "").strip():
        raise HTTPException(status_code=400, detail="invalid user_id")
    return {"sessions": await list_sessions(user_id.strip())}


@app.post("/sessions")
async def post_session(body: SessionUpsert):
    """注册/刷新会话。

    两种调用方：
    - 「新对话」按钮：title 为空 → 占位标题「新对话」，auto_title=true，
      首条真实消息到达后标题被替换为首个问题；
    - 每轮 run（Go 网关代理时带 query）：同一条会话只更新 updated_at，
      已有真实标题不被覆盖。
    """
    _check_ownership(body.user_id, body.session_id)
    await upsert_session(
        body.user_id.strip(), body.session_id, body.title, auto_title=not body.title.strip()
    )
    return {"ok": True}


@app.get("/sessions/{session_id}/messages")
async def get_session_messages(session_id: str, user_id: str):
    """某会话的纯文本消息（切会话/恢复上下文）。"""
    _check_ownership(user_id, session_id)
    return {"messages": await session_messages(user_id.strip(), session_id)}


@app.delete("/sessions/{session_id}")
async def remove_session(session_id: str, user_id: str):
    """删除会话：注册行 + checkpoints + Redis 滚动摘要。"""
    _check_ownership(user_id, session_id)
    deleted = await delete_session(user_id.strip(), session_id)
    return {"ok": True, "deleted": deleted}
