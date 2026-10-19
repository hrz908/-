import Toast from '../../miniprogram_npm/tdesign-miniprogram/toast/index';
import { clearSession, isLoggedIn, setLoggedIn } from '../../utils/request';
import { login, getProfile, updateProfile, uploadAvatar, absoluteUrl } from '../../services/user';

// 本地资料存一份：后端暂时没有用户资料接口，先落 storage，
// 将来有接口了把这两处读写换成 services 层调用即可
const PROFILE_KEY = 'sa_profile';
// 上一次从微信那边取到的昵称。
// 平台不提供任何读取昵称的接口，只能让用户在 type="nickname" 的输入框里点选一次；
// 存下来之后，再点「使用微信昵称」就能直接替换输入框内容，不用重新拉起面板
const WECHAT_NICKNAME_KEY = 'sa_wechat_nickname';
const NICKNAME_MAX = 20;

// 没完善过资料时的兜底昵称。微信不允许静默读取头像/昵称，
// 「微信用户」就是官方返回的默认昵称；头像留空显示灰底圆块，
// 微信号没有任何接口能拿到，显示「未获取」
const DEFAULT_NICKNAME = '微信用户';

// 头像路径可能是远程 URL（已经上传过的）或本地临时路径（刚选的），
// 只有后者需要走上传
function isRemoteUrl(path) {
  return /^https?:\/\//i.test(path);
}

// 缓存下来的微信昵称，没有（还没授权过 / 换了设备）就返回空串
function cachedWechatNickname() {
  try {
    return wx.getStorageSync(WECHAT_NICKNAME_KEY) || '';
  } catch (e) {
    return '';
  }
}

Page({
  data: {
    pushEnabled: true,

    // 是否已授权登录：决定底部按键是「登录」还是「退出登录」
    isLoggedIn: false,

    // 个人信息
    nickname: '',
    wechatId: '',
    avatar: '',

    // 编辑资料弹窗
    profileVisible: false,
    editNickname: '',
    editAvatar: '',
    // 超长时的输入框提示文案。空串表示没超——t-input 的 tips 为空就不渲染这一行
    nicknameTip: '',
    // 昵称输入框是否处于「取微信昵称」模式：同时决定 type（nickname/text）与 focus。
    // 只有点下方「使用微信昵称」才为 true，平时为 false，避免一聚焦就弹候选栏
    nicknamePicking: false,

    // 授权登录弹窗
    loginVisible: false,
    loginNickname: '',
    // 选完的微信头像是本地临时路径，登录成功后会上传换回正式 URL
    loginAvatar: '',
    loginTip: '',
    loginPicking: false,
    loginSubmitting: false,

    // 退出登录二次确认
    logoutVisible: false
  },

  onLoad() {
    const profile = wx.getStorageSync(PROFILE_KEY) || {};
    const logged = isLoggedIn();

    this.setData({
      // 没存过就默认开：设计稿里推送开关是开着的
      pushEnabled: wx.getStorageSync('sa_push_enabled') !== false,
      nickname: profile.nickname || DEFAULT_NICKNAME,
      wechatId: profile.wechatId || '',
      avatar: profile.avatar || '',
      isLoggedIn: logged
    });

    // 已登录时以后端资料为准：换设备/重装后本地缓存是空的，
    // 只有后端这份能恢复出头像和昵称。失败不打断页面，用本地缓存顶着
    if (logged) this.loadProfile();
  },


  goBack() {
    // 立即出栈，不做任何自绘前置动画
    wx.navigateBack();
  },

  /* ===== 登录 ===== */

  // 个人信息卡：未登录时没有资料可编辑，直接引导去授权登录
  onProfileTap() {
    if (this.data.isLoggedIn) {
      this.openProfile();
      return;
    }
    this.openLogin();
  },

  openLogin() {
    this.setData({
      loginVisible: true,
      loginNickname: this.data.nickname === '未登录' ? '' : this.data.nickname,
      loginAvatar: '',
      loginTip: '',
      loginPicking: false
    });
  },

  cancelLogin() {
    this.setData({ loginVisible: false });
  },

  onLoginNickname(e) {
    const value = (e && e.detail && e.detail.value) || '';
    // 输入框正处在 nickname 类型（微信面板刚带回内容）时，这个值就是
    // 用户从微信选进来的昵称，存一份，以后点「使用微信昵称」直接填
    if (this.data.loginPicking && value.trim()) {
      wx.setStorageSync(WECHAT_NICKNAME_KEY, value.trim());
    }
    this.setData({
      loginNickname: value,
      loginTip: this.tipFor(value)
    });
  },

  // 与编辑资料弹窗同一套逻辑：有缓存就直接填，没有才去拉起微信的昵称面板
  fillLoginNickname() {
    const cached = cachedWechatNickname();
    if (cached) {
      this.setData({
        loginNickname: cached,
        loginTip: this.tipFor(cached)
      });
      return;
    }
    this.setData({ loginPicking: true });
  },

  onLoginNicknameBlur() {
    this.setData({ loginPicking: false });
  },

  onChooseLoginAvatar(e) {
    const url = e && e.detail && e.detail.avatarUrl;
    // 这里是微信给的临时路径，登录成功后再上传换成正式 URL
    if (url) this.setData({ loginAvatar: url });
  },

  // 登录：先换 token 并让后端按昵称建号，再把头像（如果有）传上去。
  // 分两步是因为上传是受保护接口，得先有 token
  submitLogin() {
    if (this.data.loginSubmitting) return;

    const nickname = String(this.data.loginNickname || '').trim();
    if (!nickname) {
      Toast({ context: this, message: '请先填写昵称' });
      return;
    }
    const tip = this.tipFor(nickname);
    if (tip) {
      Toast({ context: this, message: tip });
      return;
    }

    this.setData({ loginSubmitting: true });

    login({ nickname: nickname })
      .then((res) => {
        setLoggedIn(true);
        const profileNickname = (res && res.nickname) || nickname;

        // 头像不能塞进登录接口：multipart 上传必须用 wx.uploadFile，
        // 而且那是受保护接口，得先有 token 才能调
        if (!this.data.loginAvatar) {
          return { nickname: profileNickname, avatar: absoluteUrl(res && res.avatar) };
        }
        return uploadAvatar(this.data.loginAvatar).then(
          (up) => ({ nickname: profileNickname, avatar: absoluteUrl(up && up.avatar_url) }),
          // 头像传失败不阻断登录：账号已经建好了，下次编辑资料再换头像
          () => ({ nickname: profileNickname, avatar: absoluteUrl(res && res.avatar) })
        );
      })
      .then((result) => {
        this.setData({
          isLoggedIn: true,
          nickname: result.nickname,
          avatar: result.avatar,
          loginVisible: false,
          loginSubmitting: false
        });
        Toast({ context: this, message: '登录成功' });
      })
      .catch((err) => {
        this.setData({ loginSubmitting: false });
        Toast({ context: this, message: (err && err.message) || '登录失败' });
      });
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
        // 拉不到就用本地缓存，不弹错打扰用户
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
      profileVisible: true
    });
  },

  onEditNickname(e) {
    const value = (e && e.detail && e.detail.value) || '';
    // 输入框正处在 nickname 类型（微信面板刚带回内容）时，这个值就是
    // 用户从微信选进来的昵称，存一份，以后点「使用微信昵称」直接填
    if (this.data.nicknamePicking && value.trim()) {
      wx.setStorageSync(WECHAT_NICKNAME_KEY, value.trim());
    }
    this.setData({
      editNickname: value,
      nicknameTip: this.tipFor(value)
    });
  },

  // 「使用微信昵称」：有缓存就直接用它替换输入框内容，不再弹任何面板。
  //
  // 完全没有缓存时（首次使用 / 换设备）才退回原来的做法：
  // 平台不提供读取昵称的接口，做不到「点了就凭空填进来」，
  // 只能把输入框切成 nickname 类型并聚焦，由用户本人点选一次。
  // 选完的值会在上面的 onEditNickname 里存进缓存，从此以后都是直接填
  fillWechatNickname() {
    const cached = cachedWechatNickname();
    if (cached) {
      this.setData({
        editNickname: cached,
        nicknameTip: this.tipFor(cached)
      });
      return;
    }
    this.setData({ nicknamePicking: true });
  },

  // 失焦后复位成普通输入框：既能关掉候选栏，
  // 也让 nicknamePicking 回到 false，下次点按键才会重新触发
  onNicknameBlur() {
    this.setData({ nicknamePicking: false });
  },

  // 平时不显示 x/20 计数，只有超了才给一句提示。
  // 按字符数算（中文昵称按字符更符合直觉），trim 后判断，纯空格不算超
  tipFor(value) {
    const len = String(value || '').trim().length;
    return len > NICKNAME_MAX ? '昵称不能超过 ' + NICKNAME_MAX + ' 个字符' : '';
  },

  // 用微信头像：open-type="chooseAvatar" 的原生事件，
  // 弹出的面板里就有「使用微信头像」这一项，e.detail.avatarUrl 是选完的临时路径
  onChooseWechatAvatar(e) {
    const url = e && e.detail && e.detail.avatarUrl;
    if (url) this.persistAvatar(url);
  },

  // 微信给的头像链接是临时路径，冷启动后会失效、头像变回默认。
  // 这里用 saveFile 转存到小程序本地目录（USER_DATA_PATH）再记路径，
  // 这样重启后头像还在。等后端有头像接口了，把这里换成上传后拿到的 URL
  persistAvatar(tempPath) {
    // getFileSystemManager / saveFile 是基础库核心 API，不需要再做存在性判断
    wx.getFileSystemManager().saveFile({
      tempFilePath: tempPath,
      success: (res) => {
        this.setData({ editAvatar: res.savedFilePath || tempPath });
      },
      fail: () => {
        // 转存失败就用临时路径顶上，至少本次会话里头像是对的
        this.setData({ editAvatar: tempPath });
      }
    });
  },

  cancelEdit() {
    this.setData({ profileVisible: false });
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

    // 已登录：写回后端（后端才是资料的归属）；未登录：只落本地缓存
    if (this.data.isLoggedIn) {
      this.saveProfileRemote(nickname, this.data.editAvatar);
      return;
    }

    wx.setStorageSync(PROFILE_KEY, {
      nickname: nickname,
      wechatId: this.data.wechatId,
      avatar: this.data.editAvatar
    });
    this.setData({
      nickname: nickname,
      avatar: this.data.editAvatar,
      profileVisible: false
    });
    Toast({ context: this, message: '资料已保存' });
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
        wx.setStorageSync(PROFILE_KEY, {
          nickname: result.nickname,
          wechatId: this.data.wechatId,
          avatar: result.avatar
        });
        this.setData({
          nickname: result.nickname,
          avatar: result.avatar,
          profileVisible: false
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
    // 清掉本地 token / user_id 与登录标记，下次请求会重新走 wx.login。
    // 不跳走：底部按键当场变回「登录」，退出结果看得见
    clearSession();
    // 微信昵称也一起清：它是上一个人的，换号登录后不能再直接填出来
    wx.removeStorageSync(WECHAT_NICKNAME_KEY);
    this.setData({
      logoutVisible: false,
      isLoggedIn: false,
      nickname: DEFAULT_NICKNAME,
      avatar: ''
    });
    Toast({ context: this, message: '已退出登录' });
  }
});
