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
    apiBase: 'http://127.0.0.1:8080',
    wsBase: 'ws://127.0.0.1:8080'
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

export const env = envVersion();
export const apiBase = config.apiBase;
export const wsBase = config.wsBase;
