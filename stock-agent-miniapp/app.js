// 注意：本项目统一用 ES Module（import / export）。
// 不要在这里写 require() 去加载 utils/ 或 config/ 下的模块——
// 跨规范混用会拿到空导出对象，onLaunch 一抛错就是整个小程序白屏
import { ensureLogin } from './utils/request';

App({
  globalData: {},

  onLaunch() {
    // 启动时静默登录：wx.login 换 JWT 并写入本地缓存。
    // 这里只是「提前发起」而不是 await——页面按需用 ensureLogin() 等待同一个 Promise，
    // 既能并行预热，又不会拖慢启动。登录失败内部已兜住，不会往外抛
    ensureLogin();
  }
});
