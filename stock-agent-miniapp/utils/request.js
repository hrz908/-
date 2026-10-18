import { apiBase } from '../config';

// 登录后把 token / user_id 存本地，后续请求自动带上
const TOKEN_KEY = 'token';
const UID_KEY = 'user_id';

export function getToken() {
  try {
    return wx.getStorageSync(TOKEN_KEY) || '';
  } catch (e) {
    return '';
  }
}

export function getUserId() {
  try {
    return Number(wx.getStorageSync(UID_KEY)) || 0;
  } catch (e) {
    return 0;
  }
}

export function setSession(token, userId) {
  try {
    wx.setStorageSync(TOKEN_KEY, token);
    if (userId) wx.setStorageSync(UID_KEY, userId);
  } catch (e) {
    // 存储失败（如隐私模式）不阻断流程，退化成每次重新登录
  }
}

export function clearSession() {
  try {
    wx.removeStorageSync(TOKEN_KEY);
    wx.removeStorageSync(UID_KEY);
  } catch (e) {
    // 忽略
  }
}

// 用 wx.login 的 code 换 JWT。
// 这里直接用 wx.request 而不是下面的 doRequest，
// 否则会形成「登录也要先登录」的死循环
function login() {
  return new Promise((resolve) => {
    wx.login({
      success: (lr) => {
        if (!lr || !lr.code) {
          resolve('');
          return;
        }
        wx.request({
          url: apiBase + '/api/v1/auth/login',
          method: 'POST',
          data: { code: lr.code },
          header: { 'Content-Type': 'application/json' },
          timeout: 10000,
          success: (res) => {
            const body = res.data || {};
            if (res.statusCode === 200 && body.code === 0 && body.data) {
              setSession(body.data.token, body.data.user_id);
              resolve(body.data.token);
              return;
            }
            resolve('');
          },
          fail: () => resolve('')
        });
      },
      fail: () => resolve('')
    });
  });
}

// 登录 Promise 缓存：页面首次加载往往并发好几个请求，
// 不共享 Promise 会同时触发多次 wx.login
let loginPromise = null;

// ensureLogin 保证拿到一个可用 token。
// force = true 时强制重新登录（token 过期重试用）
export function ensureLogin(force) {
  if (!force && getToken()) return Promise.resolve(getToken());
  if (!loginPromise) {
    loginPromise = login().then((t) => {
      loginPromise = null;
      return t;
    });
  }
  return loginPromise;
}

function fail(code, message) {
  return { code, message: message || '请求失败' };
}

function doRequest(url, method, data, token, retried) {
  return new Promise((resolve, reject) => {
    wx.request({
      url: apiBase + url,
      method: method,
      data: data || {},
      header: {
        'Content-Type': 'application/json',
        Authorization: token ? `Bearer ${token}` : ''
      },
      timeout: 10000,
      success: (res) => {
        const body = res.data && typeof res.data === 'object' ? res.data : {};

        // 200 且业务码为 0 才算成功
        if (res.statusCode === 200 && body.code === 0) {
          resolve(body.data);
          return;
        }

        // token 失效：静默重新登录一次并重放原请求，不把「未登录」抛给用户
        if (res.statusCode === 401 && !retried) {
          ensureLogin(true).then((nt) => {
            if (!nt) {
              reject(fail(401, '登录已失效，请重试'));
              return;
            }
            doRequest(url, method, data, nt, true).then(resolve, reject);
          });
          return;
        }

        reject(fail(body.code || res.statusCode, body.message));
      },
      fail: (err) => reject(fail(-1, (err && err.errMsg) || '网络异常'))
    });
  });
}

function request(url, method, data) {
  return ensureLogin(false).then((token) => doRequest(url, method, data, token, false));
}

export const get = (url, data) => request(url, 'GET', data);
export const post = (url, data) => request(url, 'POST', data);
export const put = (url, data) => request(url, 'PUT', data);
export const del = (url, data) => request(url, 'DELETE', data);
