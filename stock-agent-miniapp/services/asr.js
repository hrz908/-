import { apiBase } from '../config';
import { getToken, ensureLogin } from '../utils/request';

// 语音转文字：把本地录音文件上传到网关，由网关转发上游 ASR，返回识别文本。
//
// 必须走 wx.uploadFile（multipart）：wx.request 发不了本地临时文件，
// 而 utils/request.js 里固定了 Content-Type: application/json，也用不上。
// 响应体的 data 是字符串，要自己 JSON.parse（与 services/user.js 上传头像一致）。
export function transcribe(filePath) {
  return ensureLogin(false).then(
    () =>
      new Promise((resolve, reject) => {
        wx.uploadFile({
          url: apiBase + '/api/v1/asr/transcribe',
          filePath: filePath,
          name: 'file',
          header: { Authorization: 'Bearer ' + (getToken() || '') },
          timeout: 30000,
          success: (res) => {
            let body = {};
            try {
              body = JSON.parse(res.data || '{}');
            } catch (e) {
              body = {};
            }
            if (res.statusCode === 200 && body.code === 0 && body.data) {
              const text = (body.data.text || '').trim();
              // 空文本当失败处理：发出一条空消息比不识别更让人困惑
              if (text) resolve(text);
              else reject(new Error('empty'));
              return;
            }
            reject(new Error(body.message || '识别失败'));
          },
          fail: (err) => reject(new Error((err && err.errMsg) || '识别失败'))
        });
      })
  );
}

export default { transcribe };
