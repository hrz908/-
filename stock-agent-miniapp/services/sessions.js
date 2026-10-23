// 会话历史服务：小程序「历史对话」抽屉的数据源。
// 与其它 services 同一套约定：页面只调这里，不直接碰 wx.request。
//
// 后端链路：本服务 -> Go 网关 /api/v1/agent/sessions* -> Python Agent /sessions*
// （注册表 chat_sessions + LangGraph checkpoints，删除会连 checkpoint 一起清）。
// 鉴权走 utils/request 的 Bearer，与 agent SSE 同一套。
import { get, post, del } from '../utils/request';

const API = '/api/v1';

// 会话列表：按最近活跃倒序。
// 返回 [{ session_id, title, updated_at }]
export function listSessions() {
  return get(`${API}/agent/sessions`);
}

// 注册新对话：「新对话」按钮跳转前调用，让空会话立即进历史列表
// （标题先显示「新对话」，首条消息到达后被替换成首个问题）
export function createSession(sessionId, title) {
  return post(`${API}/agent/sessions`, { session_id: sessionId, title: title || '' });
}

// 某会话的纯文本消息：历史列表点选后恢复上下文用。
// 返回 [{ role: 'user' | 'assistant', content }]；结构化卡片不持久化，恢复后只有文本
export function sessionMessages(sessionId) {
  return get(`${API}/agent/sessions/${encodeURIComponent(sessionId)}/messages`);
}

// 删除会话：后端连 checkpoints 与滚动摘要一起清，删完不可恢复
export function deleteSession(sessionId) {
  return del(`${API}/agent/sessions/${encodeURIComponent(sessionId)}`);
}

// (触发一次全量重编译：新增模块需重建模块表)
