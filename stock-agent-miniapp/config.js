// 环境配置
//
// 小程序没有 .env 机制，官方推荐用 envVersion 区分环境：
//   develop  开发版（开发者工具 / 真机调试）
//   trial    体验版
//   release  正式版
//
// 改后端地址只动这张表，其它文件一律从这里取

const ENV_CONFIG = {
  develop: {
    // 远程服务器（ubuntu@tearsinrain.top）：开发版直连 HTTPS，需在开发者工具里
    // 勾选「不校验合法域名、TLS 版本以及 HTTPS 证书」（project.config.json 的 urlCheck 已为 false）
    apiBase: 'https://tearsinrain.top',
    wsBase: 'wss://tearsinrain.top'
  },
  trial: {
    apiBase: 'https://trial-api.example.com',
    wsBase: 'wss://trial-api.example.com'
  },
  release: {
    apiBase: 'https://api.example.com',
    wsBase: 'wss://api.example.com'
  }
};

function envVersion() {
  try {
    const info = wx.getAccountInfoSync();
    return (info && info.miniProgram && info.miniProgram.envVersion) || 'release';
  } catch (e) {
    // 老基础库或异常场景，按正式环境处理
    return 'release';
  }
}

function currentConfig() {
  return ENV_CONFIG[envVersion()] || ENV_CONFIG.release;
}

const config = currentConfig();

export const apiBase = config.apiBase;

// 实时语音识别的 WebSocket 基地址（server 侧 /api/v1/ws/asr）：
// 小程序录音分片直传网关，网关转发腾讯云实时 ASR，边说边出字。
// 正式环境必须是 wss，且域名要在小程序后台的 socket 合法域名里登记。
export const wsBase = config.wsBase;

// AI 对话的 SSE 流式通道（server 侧 agent stream）：每次提问一个独立
// POST 请求，响应 text/event-stream 逐帧下发，走 Header Bearer 鉴权
//（见 services/agent.js 与 server/internal/agent/sse_agent.go）
export const agentSsePath = '/api/v1/agent/stream';
