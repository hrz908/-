import { apiBase, wsBase } from '../config';
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

// 实时语音识别：按住说话时把录音分片边录边传给网关，边说边出字。
//
// 协议（与 server/internal/asr/stream.go 约定）：
//   地址：{wsBase}/api/v1/ws/asr?token=<jwt>（WebSocket 握手带不了自定义头，走 query 鉴权）
//   上行：二进制帧 = mp3 分片；文本 {"type":"end"} 结束、{"type":"cancel"} 取消
//   下行：{"type":"partial","text":...} 中间结果、
//         {"type":"final","text":...} 终稿、
//         {"type":"error","message":...} 异常
//
// 分片来自 RecorderManager.onFrameRecorded，回调可能早于 WebSocket 建立，
// 所以先入队、连上后一次性补发；连不上或中途断开则标记 broken，
// 由页面在松手后退回整段上传识别（transcribe），功能不会因此不可用。
const STREAM_OPEN_TIMEOUT = 8000;
const STREAM_MAX_QUEUE = 600;

export class AsrStream {
  constructor(handlers) {
    const h = handlers || {};
    this._onPartial = h.onPartial || function () {};
    this._onFinal = h.onFinal || function () {};
    this._onError = h.onError || function () {};

    this._task = null;
    this._opened = false;
    this._broken = false;
    this._closed = false;
    this._ending = false;
    this._queue = [];
    this._openPromise = null;
  }

  // 建立连接；返回 Promise，失败时 reject（同时触发 onError），页面可忽略。
  open() {
    if (this._openPromise) return this._openPromise;

    this._openPromise = ensureLogin(false).then(
      () =>
        new Promise((resolve, reject) => {
          const token = getToken() || '';
          const url = wsBase + '/api/v1/ws/asr?token=' + encodeURIComponent(token);
          const task = wx.connectSocket({ url });
          this._task = task;

          let timer = setTimeout(() => {
            timer = 0;
            this._fail('连接实时识别服务超时', reject);
          }, STREAM_OPEN_TIMEOUT);

          task.onOpen(() => {
            if (timer) clearTimeout(timer);
            timer = 0;
            if (this._closed) {
              this._shut(task);
              return;
            }
            this._opened = true;
            this._flushQueue();
            resolve();
          });

          task.onMessage((res) => this._handle(res && res.data));

          task.onError(() => {
            if (timer) clearTimeout(timer);
            timer = 0;
            this._fail('实时识别连接异常', reject);
          });

          task.onClose(() => {
            if (timer) clearTimeout(timer);
            timer = 0;
            // 正常收尾（收到 final 或主动 cancel）不算异常
            if (!this._closed) this._fail('实时识别连接中断', reject);
          });
        })
    );

    // 调用方通常只关心 onError 回调，这里吞掉 rejection 避免未处理告警
    this._openPromise.catch(function () {});
    return this._openPromise;
  }

  // 推入一帧录音（ArrayBuffer）。连接未就绪时排队，避免开头几个字丢失。
  push(frameBuffer) {
    if (this._closed || this._broken || !frameBuffer) return;
    if (!this._opened) {
      if (this._queue.length < STREAM_MAX_QUEUE) this._queue.push(frameBuffer);
      return;
    }
    this._sendBinary(frameBuffer);
  }

  // 松手：告诉服务端音频结束，等 final 回来。
  finish() {
    if (this._closed || this._broken || this._ending) return;
    this._ending = true;
    if (!this._opened) return; // 连接建好后 _flushQueue 会补发 end
    this._sendText('{"type":"end"}');
  }

  // 取消（上滑放弃）：直接断开，不等结果。
  cancel() {
    if (this._closed) return;
    if (this._opened && !this._broken) this._sendText('{"type":"cancel"}');
    this.close();
  }

  close() {
    if (this._closed) return;
    this._closed = true;
    this._queue = [];
    if (this._task) this._shut(this._task);
    this._task = null;
  }

  // 连接是否已失效（页面据此决定要不要回退整段识别）
  broken() {
    return this._broken;
  }

  _shut(task) {
    try {
      task.close({});
    } catch (e) {
      // 已关闭 / 尚未连上时 close 可能抛错，忽略即可
    }
  }

  _flushQueue() {
    const queue = this._queue;
    this._queue = [];
    queue.forEach((buf) => this._sendBinary(buf));
    if (this._ending) this._sendText('{"type":"end"}');
  }

  _sendBinary(buf) {
    if (!this._task || this._closed || this._broken) return;
    try {
      this._task.send({ data: buf });
    } catch (e) {
      this._fail('发送录音失败');
    }
  }

  _sendText(text) {
    if (!this._task || this._closed || this._broken) return;
    try {
      this._task.send({ data: text });
    } catch (e) {
      this._fail('发送录音失败');
    }
  }

  _handle(raw) {
    if (typeof raw !== 'string' || this._broken) return;
    let msg = null;
    try {
      msg = JSON.parse(raw);
    } catch (e) {
      return;
    }
    if (!msg) return;

    if (msg.type === 'partial') {
      this._onPartial(String(msg.text || ''));
      return;
    }
    if (msg.type === 'final') {
      this.close();
      this._onFinal(String(msg.text || ''));
      return;
    }
    if (msg.type === 'error') {
      this._fail(msg.message || '识别失败');
    }
  }

  // 失败只通知一次；已收过部分结果也不影响最终回退（回退用整段录音更准）
  _fail(message, reject) {
    if (this._broken || this._closed) {
      if (reject) reject(new Error(message));
      return;
    }
    this._broken = true;
    this._queue = [];
    if (reject) reject(new Error(message));
    this._onError(message);
    this.close();
  }
}

export default { transcribe, AsrStream };
