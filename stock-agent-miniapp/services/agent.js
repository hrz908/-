// Agent SSE 服务：管理与后端的 AI 对话流式连接。
// 与其它 services 同一套约定：页面只调这里，不直接碰 wx.request。
//
// 协议（与 server/internal/agent/sse_agent.go 约定）：
//   上行：POST { query, session_id }，鉴权走 Header Bearer（握手可带自定义头）
//   下行：text/event-stream，每帧 event: <type>\ndata: <json>\n\n，type ∈
//     token / card / table / status / tool_start / tool_end / sources / error / ping / done
//
// 端到端真流式：网关把 agent 的 ndjson 逐行转成 SSE 帧并每帧 Flush，
// 小程序这边用 wx.request(enableChunked) 增量收包、增量 UTF-8 解码、即时解析并派发，
// 不做任何攒批缓冲。SSE 是「每问一请求」模型，无持久连接，故 connect() 为兼容 no-op。

import { apiBase, agentSsePath } from '../config';
import { getToken } from '../utils/request';

// 生成唯一 session_id：后端按它（拼成 thread_id）区分多轮会话上下文
function generateSessionId() {
  return 'sess_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
}

class AgentSSE {
  constructor() {
    this.task = null; // 当前 wx.request task
    this.sessionId = generateSessionId();
    this.listeners = {}; // event -> [callback]
    this._buffer = ''; // 已解码、待切分 SSE 帧的文本
    this._pending = new Uint8Array(0); // 未凑齐 UTF-8 字符的字节残留（多字节被分帧截断时）
    this._isSSE = false; // 响应头是否为 text/event-stream
    this._chunkSeen = false; // 是否收到过分块（用于不支持 onChunkReceived 时的整段兜底）
    this._ended = false; // 是否已收到 done（区分「正常结束」与「异常结束」）
  }

  // 兼容旧调用：SSE 为每问一请求，无持久连接，这里直接 no-op
  connect() {}

  // 发送一条用户提问并开启一条独立的 SSE 流。
  sendMessage(query) {
    // 上一轮若仍在流（理论上发送态会挡住，这里兜底），先中止避免两条流交错
    this._abort();
    this._buffer = '';
    this._pending = new Uint8Array(0);
    this._isSSE = false;
    this._chunkSeen = false;
    this._ended = false;

    const token = getToken();
    const task = wx.request({
      url: `${apiBase}${agentSsePath}`,
      method: 'POST',
      header: {
        Authorization: token ? `Bearer ${token}` : '',
        'Content-Type': 'application/json',
        Accept: 'text/event-stream'
      },
      enableChunked: true,
      responseType: 'arraybuffer',
      data: { query, session_id: this.sessionId },
      success: (res) => {
        this.task = null;
        const code = res && res.statusCode;
        // 非 SSE 响应（如鉴权 401 的 JSON）或异常状态码：没有事件流，按错误处置
        if (!this._isSSE || (code && code >= 400)) {
          this._emit('error', { code: 'SSE_NO_STREAM', msg: '服务响应异常' });
          return;
        }
        // 兜底：部分环境 onChunkReceived 不触发，整段响应在 success 里给，
        // 这里收尾解析一遍（仅当没有逐块解析过，避免重复派发）
        if (!this._chunkSeen && res && res.data) {
          this._buffer += this._decodeChunk(new Uint8Array(res.data));
          this._drain();
        }
      },
      fail: () => {
        this.task = null;
        this._emit('error', { code: 'SSE_FAIL', msg: '连接异常' });
      }
    });
    this.task = task;

    // 记录响应内容类型，用于 success 阶段判断是否真走了 SSE
    task.onHeadersReceived((res) => {
      const header = (res && res.header) || {};
      const ct = header['Content-Type'] || header['content-type'] || '';
      this._isSSE = ct.indexOf('text/event-stream') !== -1;
    });

    // 增量收包：ArrayBuffer -> 增量 UTF-8 解码 -> 累积 -> 切帧派发
    task.onChunkReceived((res) => {
      this._chunkSeen = true;
      const data = res.data;
      const text = typeof data === 'string' ? data : this._decodeChunk(new Uint8Array(data));
      this._buffer += text;
      this._drain();
    });
  }

  // 增量 UTF-8 解码：把新字节拼到残留字节后，尽可能多地解出完整字符，
  // 剩下不完整的多字节开头留在 _pending，等下一帧补齐（解决中文被分帧截断）
  _decodeChunk(bytes) {
    const all = new Uint8Array(this._pending.length + bytes.length);
    all.set(this._pending, 0);
    all.set(bytes, this._pending.length);
    const out = [];
    let i = 0;
    while (i < all.length) {
      const b = all[i];
      let len = 1;
      if (b < 0x80) len = 1;
      else if ((b & 0xe0) === 0xc0) len = 2;
      else if ((b & 0xf0) === 0xe0) len = 3;
      else if ((b & 0xf8) === 0xf0) len = 4;
      else {
        i++;
        continue; // 非法字节，跳过
      }
      if (i + len > all.length) break; // 不完整，等下一帧
      let cp;
      if (len === 1) cp = b;
      else if (len === 2) cp = ((b & 0x1f) << 6) | (all[i + 1] & 0x3f);
      else if (len === 3)
        cp = ((b & 0x0f) << 12) | ((all[i + 1] & 0x3f) << 6) | (all[i + 2] & 0x3f);
      else
        cp =
          ((b & 0x07) << 18) |
          ((all[i + 1] & 0x3f) << 12) |
          ((all[i + 2] & 0x3f) << 6) |
          (all[i + 3] & 0x3f);
      out.push(cp);
      i += len;
    }
    this._pending = all.subarray(i);
    let s = '';
    for (let k = 0; k < out.length; k++) s += String.fromCodePoint(out[k]);
    return s;
  }

  // 按 SSE 帧分隔符 \n\n 切出完整事件块并解析
  _drain() {
    let idx;
    while ((idx = this._buffer.indexOf('\n\n')) !== -1) {
      const block = this._buffer.slice(0, idx);
      this._buffer = this._buffer.slice(idx + 2);
      this._parseBlock(block);
    }
  }

  _parseBlock(block) {
    let event = 'message';
    const dataLines = [];
    const lines = block.split('\n');
    for (let li = 0; li < lines.length; li++) {
      const line = lines[li];
      if (line === '') continue;
      if (line[0] === ':') continue; // 注释行
      if (line.startsWith('event:')) {
        event = line.slice(6).trim();
      } else if (line.startsWith('data:')) {
        // SSE：冒号后可选一个空格
        dataLines.push(line.charAt(5) === ' ' ? line.slice(6) : line.slice(5));
      }
      // id / retry 等字段忽略
    }
    const dataStr = dataLines.join('\n');
    let data = dataStr;
    try {
      data = JSON.parse(dataStr);
    } catch (e) {
      // 解析失败保留原文，交给上层判断（正常事件 data 都是合法 JSON）
    }
    this._handleEvent({ event, data });
  }

  // 分发服务端事件到页面监听器；done 标记本轮正常结束
  _handleEvent(event) {
    const { event: type, data } = event || {};
    switch (type) {
      case 'token':
      case 'card':
      case 'table':
      case 'status':
      case 'tool_start':
      case 'tool_end':
      case 'sources':
      case 'error':
      case 'done':
        if (type === 'done') this._ended = true;
        this._emit(type, data);
        break;
      case 'ping':
        // 心跳：SSE 无协议层 pong，业务层无需回应
        break;
      default:
        console.log('[AgentSSE] unknown event:', type);
    }
  }

  on(event, callback) {
    if (!this.listeners[event]) this.listeners[event] = [];
    this.listeners[event].push(callback);
  }

  // 不传 callback 时摘掉该事件的全部监听（页面 onUnload 用）
  off(event, callback) {
    if (!this.listeners[event]) return;
    if (!callback) {
      delete this.listeners[event];
      return;
    }
    this.listeners[event] = this.listeners[event].filter((cb) => cb !== callback);
  }

  _emit(event, data) {
    const cbs = this.listeners[event] || [];
    cbs.forEach((cb) => {
      try {
        cb(data);
      } catch (e) {
        console.error('[AgentSSE] listener error:', e);
      }
    });
  }

  // 中止当前流（发送新提问 / 新建会话 / 断开时调用）
  _abort() {
    if (this.task) {
      try {
        this.task.abort();
      } catch (e) {
        // ignore
      }
      this.task = null;
    }
  }

  // 主动断开：中止在途请求（SSE 无持久连接，无需关闭 socket）
  disconnect() {
    this._abort();
  }

  // 新建会话：换 session_id，后端上下文随之重置
  newSession() {
    this._abort();
    this.sessionId = generateSessionId();
  }
}

// 单例导出
const agentSocket = new AgentSSE();
export default agentSocket;
