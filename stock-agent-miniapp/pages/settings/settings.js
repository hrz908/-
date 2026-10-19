import Toast from '../../miniprogram_npm/tdesign-miniprogram/toast/index';
import { clearSession, ensureLogin } from '../../utils/request';
import { getProfile, updateProfile, uploadAvatar, absoluteUrl } from '../../services/user';

const NICKNAME_MAX = 10;

// 没完善过资料时的兗底昵称。后端建号时默认给的就是这个值，
// 前端保持一致：后端返回空昵称时也用这个显示
const DEFAULT_NICKNAME = '微信用户';

// 头像路径可能是远程 URL（已经上传过的）或本地临时路径（刚选的），
// 只有后者需要走上传
function isRemoteUrl(path) {
  return /^https?:\/\//i.test(path);
}

Page({
  data: {
    pushEnabled: true,

    // 个人信息
    nickname: '',
    avatar: '',

    // 编辑资料弹窗
    profileVisible: false,
    editNickname: '',
    editAvatar: '',
    // 超长时的输入框提示文案。空串表示没超——t-input 的 tips 为空就不渲染这一行
    nicknameTip: '',
    // 昵称输入框是否处于「取微信昵称」模式：只决定 type（nickname/text）。
    // 只有点下方「使用微信昵称」才为 true，平时为 false，避免一聚焦就弹候选栏。
    // 注意：不要让它同时控制 focus——focus 一直为 true 会强制输入框常驻聚焦，
    // 键盘就关不掉。focus 单独由 nicknameFocus 控制并在用完即释放
    nicknamePicking: false,
    // 是否强制聚焦：仅在点击「使用微信昵称」后短暂为 true 以唤起微信面板，
    // 选完 / 失焦后立刻置回 false，否则键盘无法收起
    nicknameFocus: false,
    // 「使用微信昵称」按键刚触发过聚焦的标记：用于区分「按键唤起面板」和
    // 「用户手动点进输入框」。面板关掉后若残留 nickname 类型，用户再次点输入框
    // 不应再弹面板，只能靠按键唤起，所以这次聚焦消费掉标记后即按手动处理
    nicknameArmed: false,

    // 退出登录二次确认
    logoutVisible: false,

    // 底部退出登录按钮距屏幕底部的距离（安全区 + 留白），与其它子页面一致
    bottomGap: 20
  },

  onLoad() {
    // 计算底部安全区，让退出登录按钮与自选股 / 提醒页的底部按钮处于同一位置
    let safeBottom = 0;
    try {
      const win = wx.getWindowInfo ? wx.getWindowInfo() : wx.getSystemInfoSync();
      safeBottom =
        (win.safeAreaInsets && win.safeAreaInsets.bottom) ||
        (win.screenHeight && win.safeArea ? win.screenHeight - win.safeArea.bottom : 0) ||
        0;
    } catch (e) {
      // 拿不到就用默认值
    }
    this.setData({
      bottomGap: 20 + safeBottom,
      // 没存过就默认开：设计稿里推送开关是开着的
      pushEnabled: wx.getStorageSync('sa_push_enabled') !== false
    });

    // 打开页面时确保有 token（App.onLaunch 已经静默登录过，
    // 这里只是兑底：万一 token 被清，自动重新静默登录一次）。
    // ensureLogin 拿到 token 后再拉资料，失败不打断页面，用默认值顶着
    ensureLogin(false).then(() => this.loadProfile());
  },


  goBack() {
    // 立即出栈，不做任何自绘前置动画
    wx.navigateBack();
  },

  /* ===== 个人信息卡 ===== */

  // 个人信息卡：点击打开编辑资料弹窗，头像昵称的完善由用户主动触发
  onProfileTap() {
    this.openProfile();
  },

  // 拉取后端资料覆盖本地展示
  loadProfile() {
    getProfile()
      .then((res) => {
        this.setData({
          nickname: (res && res.nickname) || DEFAULT_NICKNAME,
          avatar: absoluteUrl(res && res.avatar)
        });
      })
      .catch(() => {
        // 拉不到不弹错打扰用户，页面保持登录时的值
      });
  },

  // TDesign switch 的 change 事件恒为 { value: boolean }（组件内部用 customValue
  // 取值，默认 [true, false]），所以直接取 e.detail.value，不用再做类型兜底
  onTogglePush(e) {
    const pushEnabled = e.detail.value;
    this.setData({ pushEnabled });
    wx.setStorageSync('sa_push_enabled', pushEnabled);
    Toast({
      context: this,
      message: pushEnabled ? '消息推送已开启' : '消息推送已关闭'
    });
  },

  /* ===== 编辑资料弹窗 ===== */

  openProfile() {
    this.setData({
      editNickname: this.data.nickname,
      editAvatar: this.data.avatar,
      nicknameTip: this.tipFor(this.data.nickname),
      nicknamePicking: false,
      nicknameFocus: false,
      nicknameArmed: false,
      profileVisible: true
    });
  },

  // 把微信昵称（来自 change 或 nicknamereview 事件）写入输入框，并退出「取微信昵称」模式。
  // 关键：一旦取到昵称就立刻把 nicknamePicking 置回 false，让 type 切回 text。
  // 否则 type=nickname 的输入框每次聚焦都会唤起微信昵称面板，点进去就一直跳。
  // 选到的昵称是普通字符串、已存进 editNickname，切回 text 通过绑定重新写入不会丢
  applyWechatNickname(value) {
    const picking = this.data.nicknamePicking;
    // 只在「按键唤起且真正选到昵称」这一次退出 nickname 模式并释放强制聚焦。
    // 普通编辑（picking 已为 false）完全不碰 nicknameFocus，这样删除字符不会把键盘收起来
    const exitPicking = picking && !!value;
    this.setData({
      editNickname: value,
      nicknameTip: this.tipFor(value),
      nicknamePicking: exitPicking ? false : this.data.nicknamePicking,
      nicknameFocus: exitPicking ? false : this.data.nicknameFocus
    });
    // 防御：个别基础库在 type 从 nickname 切回 text 的瞬间会清掉显示值，
    // 仅当确实被清空时才延迟重写回昵称，绝不覆盖用户正在做的编辑
    if (exitPicking) {
      setTimeout(() => {
        if (!this.data.editNickname) this.setData({ editNickname: value });
      }, 60);
    }
  },

  onEditNickname(e) {
    const value = (e && e.detail && e.detail.value) || '';
    this.applyWechatNickname(value);
  },

  // 微信昵称面板的「昵称审核」事件：部分基础库版本只通过这个事件回传选中的昵称，
  // 作为 change 的补充兜底，确保无论哪个事件先到都能自动填入
  onNicknameReview(e) {
    const detail = (e && e.detail) || {};
    const value = detail.value || detail.nickname || '';
    if (value) this.applyWechatNickname(value);
  },

  // 失焦兜底：点开「使用微信昵称」后没选就点了别处（输入框仍为空），
  // 把 nickname 模式复位、释放强制聚焦，避免空值 + nickname 类型 + 残留聚焦
  // 导致微信候选栏再次弹出。已选到昵称的情况由 applyWechatNickname 处理（type 已切回 text）
  onNicknameBlur() {
    if (this.data.nicknamePicking) {
      this.setData({
        nicknamePicking: false,
        nicknameFocus: false
      });
    }
  },

  // 聚焦兜底：用户手动点进输入框时，若仍处于 nickname 模式但这次聚焦不是由
  // 「使用微信昵称」按键触发（nicknameArmed 已被消费），说明微信面板已关掉却残留了
  // nickname 类型——这里强制切回 text，保证点输入框本身绝不会再弹昵称面板，
  // 只有点「使用微信昵称」按键才能唤起。按键触发的那次聚焦会先消费掉 armed 标记
  onNicknameFocus() {
    if (this.data.nicknamePicking && !this.data.nicknameArmed) {
      this.setData({ nicknamePicking: false, nicknameFocus: false });
    } else if (this.data.nicknameArmed) {
      // 消费按键触发的聚焦标记，之后任何聚焦都按「用户手动」处理
      this.setData({ nicknameArmed: false });
    }
  },

  // 「使用微信昵称」：每次都弹微信官方的昵称选择面板。
  // 平台不提供读取昵称的接口，只能把输入框切成 nickname 类型并聚焦，
  // 由用户本人点选一次。清空输入框是因为微信面板只在输入框为空时才弹出。
  // 这里只短暂把 nicknameFocus 置 true 唤起面板，选完 / 失焦即释放（见 onEditNickname / onNicknameBlur）
  fillWechatNickname() {
    this.setData({
      editNickname: '',
      nicknameTip: '',
      nicknamePicking: true,
      nicknameFocus: true,
      // 标记本次聚焦由按键触发，允许唤起微信面板；消费后即视为手动聚焦
      nicknameArmed: true
    });
  },

  // 弹窗关闭（openProfile / cancelEdit / saveProfile）时也会统一复位 nicknamePicking 与 nicknameFocus

  // 平时不显示 x/20 计数，只有超了才给一句提示。
  // 按字符数算（中文昵称按字符更符合直觉），trim 后判断，纯空格不算超
  tipFor(value) {
    const len = String(value || '').trim().length;
    return len > NICKNAME_MAX ? '昵称不能超过 ' + NICKNAME_MAX + ' 个字符' : '';
  },

  // 用微信头像：open-type="chooseAvatar" 的原生事件，
  // 弹出的面板里就有「使用微信头像」这一项，e.detail.avatarUrl 是选完的临时路径。
  // 临时路径先存着，点「保存」时统一上传到后端换永久 URL
  onChooseWechatAvatar(e) {
    const url = e && e.detail && e.detail.avatarUrl;
    if (url) this.setData({ editAvatar: url });
  },

  cancelEdit() {
    this.setData({ profileVisible: false, nicknamePicking: false, nicknameFocus: false, nicknameArmed: false });
  },

  saveProfile() {
    const nickname = String(this.data.editNickname || '').trim();
    if (!nickname) {
      Toast({ context: this, message: '昵称不能为空' });
      return;
    }
    // 输入框不再硬截断，所以保存前必须自己兜一次，别把超长昵称写进库
    const tip = this.tipFor(nickname);
    if (tip) {
      Toast({ context: this, message: tip });
      return;
    }

    this.saveProfileRemote(nickname, this.data.editAvatar);
  },

  // 头像可能是本地临时路径（刚选的新头像）或已经是远程 URL（上次保存过的），
  // 只有本地路径才需要走上传
  saveProfileRemote(nickname, avatarPath) {
    const ensureAvatar =
      avatarPath && !isRemoteUrl(avatarPath)
        ? uploadAvatar(avatarPath).then((up) => absoluteUrl(up && up.avatar_url))
        : Promise.resolve(avatarPath);

    ensureAvatar
      .then((avatar) =>
        updateProfile({ nickname: nickname, avatar: avatar }).then((res) => ({
          avatar: avatar,
          nickname: (res && res.nickname) || nickname
        }))
      )
      .then((result) => {
        this.setData({
          nickname: result.nickname,
          avatar: result.avatar,
          profileVisible: false,
          nicknamePicking: false,
          nicknameFocus: false,
          nicknameArmed: false
        });
        Toast({ context: this, message: '资料已保存' });
      })
      .catch((err) => {
        Toast({ context: this, message: (err && err.message) || '保存失败' });
      });
  },

  /* ===== 退出登录 ===== */

  onLogout() {
    this.setData({ logoutVisible: true });
  },

  onLogoutCancel() {
    this.setData({ logoutVisible: false });
  },

  onLogoutConfirm() {
    // 清掉本地 token / user_id 与登录标记，下次请求会重新走静默登录。
    // 页面复位到默认状态，不跳走：底部「退出登录」按键当场变回「退出登录」
    clearSession();
    this.setData({
      logoutVisible: false,
      nickname: DEFAULT_NICKNAME,
      avatar: ''
    });
    Toast({ context: this, message: '已退出登录' });
  }
});
