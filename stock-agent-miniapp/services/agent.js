// Agent WebSocket 服务：管理与后端的 AI 对话连接。
// 与其它 services 同一套约定：页面只调这里，不直接碰 wx.connectSocket。
//
// 协议（与 server 侧 agent ws 约定）：
//   上行：{ query }
//   下行：{ event, data }，event ∈
//     token / card / table / status / tool_start / tool_end / sources / error / ping / done
//
// 单例：连接跨页面生命周期复用，chat 页 onUnload 只摘监听、不断连接

import { wsBase, agentWsPath } from '../config';
import { getToken } from '../utils/request';

// 生成唯一 session_id：后端按它区分多轮会话上下文
function generateSessionId() {
  return 'sess_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
}

// 重连参数：指数退避，1s 起步、封顶 30s、最多 5 次
const RECONNECT_BASE_DELAY = 1000;
const RECONNECT_MAX_DELAY = 30000;
const MAX_RECONNECT_ATTEMPTS = 5;

class AgentSocket {
  constructor() {
    this.socket = null;
    this.sessionId = generateSessionId();
    this.connected = false;
    this.connecting = false;
    this.reconnectAttempts = 0;
    this.listeners = {}; // event -> [callback]
    this.pendingMessages = []; // 连接建立前缓存的上行消息，onOpen 后补发
    this._reconnectTimer = 0;
    this._intentionalClose = false;
  }

  // 建立连接。已连 / 正在连时是 no-op，页面可放心重复调用
  connect() {
    if (this.connected || this.connecting) return;
    this.connecting = true;
    this._intentionalClose = false;

    // 握手无法带自定义 header，token 走 query（后端对该路由允许 ?token=）
    const token = getToken();
    const url = `${wsBase}${agentWsPath}?token=${token}&session_id=${this.sessionId}`;

    // 用局部 sock 承接本次连接：disconnect() 后立即 connect()（如 newSession）时，
    // 旧 socket 的 onClose/onError 是异步回来的，若不辨别就会把新连接的状态改乱。
    // 每个回调先比对 this.socket === sock，陈旧 socket 的事件一律忽略
    const sock = wx.connectSocket({
      url,
      fail: (err) => {
        console.error('[AgentSocket] connect failed:', err);
        if (this.socket !== sock) return;
        this.connecting = false;
        this._scheduleReconnect();
      }
    });
    this.socket = sock;

    sock.onOpen(() => {
      if (this.socket !== sock) return;
      console.log('[AgentSocket] connected');
      this.connected = true;
      this.connecting = false;
      this.reconnectAttempts = 0;
      this._emit('connected', {});
      // 补发连接期间缓存的消息
      while (this.pendingMessages.length > 0) {
        this._doSend(this.pendingMessages.shift());
      }
    });

    sock.onMessage((res) => {
      if (this.socket !== sock) return;
      try {
        this._handleEvent(JSON.parse(res.data));
      } catch (e) {
        console.error('[AgentSocket] parse error:', e, res.data);
      }
    });

    sock.onClose((res) => {
      if (this.socket !== sock) return;
      console.log('[AgentSocket] closed:', res.code, res.reason);
      this.connected = false;
      this.connecting = false;
      this.socket = null;
      this._emit('disconnected', { code: res.code });
      // 非正常关闭且不是主动断开时重连
      if (res.code !== 1000 && !this._intentionalClose) {
        this._scheduleReconnect();
      }
    });

    sock.onError((err) => {
      if (this.socket !== sock) return;
      console.error('[AgentSocket] error:', err);
      this.connected = false;
      this.connecting = false;
      this._emit('error', { code: 'WS_ERROR', msg: '连接异常' });
    });
  }

  // 发送一条用户提问。未连接时先缓存再触发连接，onOpen 后自动补发
  sendMessage(query) {
    const msg = { query };
    if (!this.connected) {
      this.pendingMessages.push(msg);
      this.connect();
      return;
    }
    this._doSend(msg);
  }

  _doSend(msg) {
    if (!this.socket || !this.connected) return;
    this.socket.send({
      data: JSON.stringify(msg),
      fail: (err) => {
        console.error('[AgentSocket] send failed:', err);
        this._emit('error', { code: 'SEND_FAILED', msg: '发送失败' });
      }
    });
  }

  // 分发服务端事件到页面监听器
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
        this._emit(type, data);
        break;
      case 'ping':
        // 心跳：协议层 ping/pong 由 wx.connectSocket 自动处理，业务层无需回应
        break;
      default:
        console.log('[AgentSocket] unknown event:', type);
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
        console.error('[AgentSocket] listener error:', e);
      }
    });
  }

  // 指数退避重连
  _scheduleReconnect() {
    if (this._intentionalClose) return;
    if (this.reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
      this._emit('error', { code: 'RECONNECT_FAILED', msg: '重连失败，请检查网络' });
      return;
    }
    const delay = Math.min(
      RECONNECT_BASE_DELAY * Math.pow(2, this.reconnectAttempts),
      RECONNECT_MAX_DELAY
    );
    this.reconnectAttempts++;
    console.log(`[AgentSocket] reconnecting in ${delay}ms (attempt ${this.reconnectAttempts})`);
    clearTimeout(this._reconnectTimer);
    this._reconnectTimer = setTimeout(() => this.connect(), delay);
  }

  // 主动断开：不触发重连
  disconnect() {
    this._intentionalClose = true;
    clearTimeout(this._reconnectTimer);
    if (this.socket) {
      this.socket.close({ code: 1000, reason: 'user disconnect' });
      this.socket = null;
    }
    this.connected = false;
    this.connecting = false;
  }

  // 新建会话：换 session_id 重连，后端上下文随之重置
  newSession() {
    this.sessionId = generateSessionId();
    this.disconnect();
    this._intentionalClose = false;
    this.reconnectAttempts = 0;
    this.connect();
  }
}

// 单例导出
const agentSocket = new AgentSocket();
export default agentSocket;
