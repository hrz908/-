import { apiBase } from '../config';
import { getToken, ensureLogin, setSession } from '../utils/request';
import { get, put } from '../utils/request';

// 登录接口是公开的：不能用 utils/request 的 request()，
// 它会先 ensureLogin，形成「登录也要先登录」的死循环，
// 所以这里和 utils/request 内部的登录一样，直接 wx.request
export function login(payload) {
  return new Promise((resolve, reject) => {
    wx.login({
      success: (lr) => {
        if (!lr || !lr.code) {
          reject(new Error('微信登录失败'));
          return;
        }
        wx.request({
          url: apiBase + '/api/v1/auth/login',
          method: 'POST',
          // 昵称、头像随登录一起提交，后端建号时直接落库，
          // 省掉「登录后再调一次资料接口」的往返
          data: {
            code: lr.code,
            nickname: (payload && payload.nickname) || '',
            avatar: (payload && payload.avatar) || ''
          },
          header: { 'Content-Type': 'application/json' },
          timeout: 10000,
          success: (res) => {
            const body = res.data || {};
            if (res.statusCode === 200 && body.code === 0 && body.data) {
              setSession(body.data.token, body.data.user_id);
              resolve(body.data);
              return;
            }
            reject(new Error(body.message || '登录失败'));
          },
          fail: () => reject(new Error('网络异常'))
        });
      },
      fail: () => reject(new Error('微信登录失败'))
    });
  });
}

export function getProfile() {
  return get('/api/v1/user/profile');
}

export function updateProfile(data) {
  return put('/api/v1/user/profile', data);
}

// 头像上传必须用 wx.uploadFile（multipart），wx.request 发不了本地文件。
// 返回 { avatar_url }，是站内相对路径，展示前用 absoluteUrl 拼成完整地址
export function uploadAvatar(filePath) {
  return new Promise((resolve, reject) => {
    // 上传走的是受保护接口，先确保手上有 token
    ensureLogin(false)
      .then(() => {
        wx.uploadFile({
          url: apiBase + '/api/v1/user/avatar',
          filePath: filePath,
          name: 'file',
          header: { Authorization: 'Bearer ' + getToken() },
          success: (res) => {
            // uploadFile 的响应体是字符串，得自己解析
            let body = {};
            try {
              body = JSON.parse(res.data);
            } catch (e) {
              body = {};
            }
            if (res.statusCode === 200 && body.code === 0 && body.data) {
              resolve(body.data);
              return;
            }
            reject(new Error(body.message || '头像上传失败'));
          },
          fail: () => reject(new Error('头像上传失败'))
        });
      })
      .catch(() => reject(new Error('登录态失效，请重新登录')));
  });
}

// 后端存的是相对路径（/uploads/avatars/xxx.png），
// 图片 src 必须是完整地址，这里统一拼上后端基地址
export function absoluteUrl(path) {
  if (!path) return '';
  if (/^https?:\/\//i.test(path)) return path;
  return apiBase + path;
}
