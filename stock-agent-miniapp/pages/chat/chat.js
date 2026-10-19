// design data（来自 Ardot「stock agent / 主页 - AI对话」）
// 纯文本走 TDesign chat-message 的 text 内容，行情/K 线这类结构化数据走自绘卡片消息

import Toast from '../../miniprogram_npm/tdesign-miniprogram/toast/index';

// 侧边抽屉导航项（TDesign cell-group / cell）
// 不列「主页」：抽屉本来就只在主页存在，这一项点了没地方去
const MENUS = [
  { key: 'watchlist', label: '自选股', icon: 'star', badge: '6', badgeTheme: 'default' },
  { key: 'alerts', label: '提醒', icon: 'notification', badge: '3', badgeTheme: 'danger' },
  { key: 'settings', label: '我的', icon: 'user' },
  { key: 'about', label: '关于', icon: 'info-circle' }
];

// 抽屉导航项 → 子页面路由，key 与 MENUS 一一对应
const ROUTES = {
  watchlist: '/pages/watchlist/watchlist',
  alerts: '/pages/alerts/alerts',
  settings: '/pages/settings/settings',
  about: '/pages/about/about'
};

// 今日大盘（TDesign cell 列表）
const MARKETS = [
  { name: '上证指数', value: '3,024.16  +0.68%', up: true },
  { name: '深证成指', value: '9,512.34  -0.42%', up: false }
];

const QUOTE = {
  name: '贵州茅台',
  code: 'SH 600519 · 白酒',
  price: '1,684.50',
  change: '+18.60',
  pct: '+1.12%',
  open: '1,672.30',
  high: '1,690.10',
  low: '1,668.00',
  meta: '已收盘 · 06-12 15:00'
};

const KLINE_ROWS = [
  { d: '06-01', c: '1,652.10', p: '+0.42%' },
  { d: '06-02', c: '1,641.30', p: '-0.65%' },
  { d: '06-05', c: '1,668.40', p: '+1.65%' },
  { d: '06-06', c: '1,655.90', p: '-0.75%' },
  { d: '06-07', c: '1,679.20', p: '+1.41%' },
  { d: '06-08', c: '1,690.40', p: '+0.67%' },
  { d: '06-09', c: '1,673.80', p: '-0.98%' },
  { d: '06-12', c: '1,684.50', p: '+0.64%' },
  { d: '06-13', c: '1,692.10', p: '+0.45%' },
  { d: '06-14', c: '1,684.50', p: '-0.45%' }
];

// 触发抽屉开关的最小横向滑动距离（px）
const SWIPE_MIN = 40;

// 对话列表底部锚点：scroll-into-view 滚动到它即可到达列表末尾
const CHAT_BOTTOM_ID = 'chat-bottom';

// chat-sender 卡片高度（rpx），取自 chat-sender.wxss：
//   22rpx×2 内边距 + 2rpx×2 边框 + textarea 区（min-height 134rpx + 16rpx 下内边距）
//   + 发送栏 64rpx
const SENDER_CARD_RPX = 262;
// 卡片外边距：组件自带 margin 是 24rpx 24rpx 80rpx，
// margin-top 已在 chat.wxml 用 custom-style 归零（那段 24rpx = 12px 会在
// 最后一条消息和输入栏之间垫出一条空隙），margin-bottom 已覆盖成 12px
const SENDER_MARGIN_TOP_RPX = 0;
const SENDER_MARGIN_BOTTOM_PX = 12;
// 对话区底边与输入栏卡片之间的间距（rpx）。
// 原值 29 里含组件 margin-top 的 24rpx；margin-top 归零后改为 5，
// 卡片是底部锚定的，这里不同步减的话那段空隙会原样留着
const SENDER_GAP_RPX = 5;

// 涨跌幅是 '+1.12%' / '-0.65%' 这类字符串，按符号判涨跌方向供卡片着色。
// 只依赖首字符符号，后续接真实行情数据时无需额外字段
function isUp(text) {
  return String(text || '').trim().charAt(0) !== '-';
}

// TDesign chat-message 的条目结构（TdChatItemMeta）
// 不传 datetime：chat-message 会用 <text wx:if="{{datetime}}"> 渲染出时间戳，
// 而产品的需求是不显示对话时间，因此这里整条消息不带时间字段。
// 同理不传 avatar / name：chat-message 的头像容器与名称行在内容为空时会自行折叠
// （__avatar:empty、__base:empty 两条规则），无需额外样式干预
function assistantMsg(data) {
  return {
    role: 'assistant',
    status: 'complete',
    content: [{ type: 'text', data }]
  };
}

function userMsg(data) {
  return {
    role: 'user',
    status: 'complete',
    content: [{ type: 'text', data }]
  };
}

// 结构化卡片消息：content 必须是空数组，chat-message 才会渲染它的 content 插槽，
// 卡片本体交给页面 WXML 绘制（行情卡、日 K 表的版式用 markdown 表格还原不出来）
function quoteCard(q) {
  return {
    role: 'assistant',
    status: 'complete',
    card: 'quote',
    content: [],
    quote: Object.assign({}, q, { up: isUp(q.pct) })
  };
}

function klineCard(rows) {
  return {
    role: 'assistant',
    status: 'complete',
    card: 'kline',
    content: [],
    kline: rows.map((r) => Object.assign({}, r, { up: isUp(r.p) }))
  };
}

// ===== 语音消息 =====
// 录音时长的两条边界：短于下限当作误触、不发；触及上限自动结束
const REC_MIN_MS = 1000;
const REC_MAX_MS = 60000;
// 手指上滑超过这个距离（px）后松开，算「取消发送」
const CANCEL_DISTANCE = 60;
// 波形装饰：固定一组高度，免得同一条语音每次重绘都长得不一样
const VOICE_BARS = [10, 20, 30, 16, 26, 36, 22, 12, 28, 18];

// 语音消息的 tempFilePath 只在本次会话可用，重新进入聊天页就失效了——
// 这一点和当前 messages 一致（本来也没有历史持久化的接口）
function voiceMsg(path, seconds) {
  return {
    role: 'user',
    status: 'complete',
    card: 'voice',
    content: [],
    voice: { path: path, duration: seconds, playing: false, bars: VOICE_BARS }
  };
}

Page({
  data: {
    drawerTop: 44,
    input: '',
    sending: false,
    padBottom: 0,
    // 输入栏容器的实际下内边距：键盘弹起时安全区在键盘之下，不再留白
    senderPadBottom: 0,
    // 键盘弹起时输入栏容器向上偏移的距离（px）
    keyboardOffset: 0,
    chatHeight: 0,
    drawerOpen: false,
    scrollIntoView: '',
    menus: MENUS,
    markets: MARKETS,
    // chat-sender 右侧按钮：图标型发送按钮
    renderPresets: [{ name: 'send', type: 'icon' }],

    // 语音输入：recording = 手指按住中，recordCanceling = 已上滑到取消区
    recording: false,
    recordCanceling: false,
    recordTip: '按住 说话',
    // 正在播放的语音消息下标，-1 表示当前没有在播
    playingIndex: -1,
    messages: [
      assistantMsg('早上好，我是 Stock Agent。今天白酒板块集体走强，要我帮你看持仓吗？'),
      userMsg('帮我查一下贵州茅台的行情，顺便看下日 K。'),
      assistantMsg('已更新 600519 的实时行情，以及最近 10 个交易日的日 K 走势：'),
      quoteCard(QUOTE),
      klineCard(KLINE_ROWS)
    ]
  },

  onLoad() {
    this.initVoice();

    let statusBarHeight = 44;
    let safeBottom = 0;
    let windowHeight = 667;
    let windowWidth = 375;
    try {
      const win = wx.getWindowInfo ? wx.getWindowInfo() : wx.getSystemInfoSync();
      statusBarHeight = win.statusBarHeight || 44;
      windowHeight = win.windowHeight || 667;
      windowWidth = win.windowWidth || 375;
      safeBottom =
        (win.safeAreaInsets && win.safeAreaInsets.bottom) ||
        (win.screenHeight && win.safeArea ? win.screenHeight - win.safeArea.bottom : 0) ||
        0;
    } catch (e) {
      // 保持默认值
    }

    // 导航栏高度 = 状态栏 + 44（TDesign navbar 内容区默认 44px）
    const navH = statusBarHeight + 44;
    const padBottom = Math.max(0, safeBottom);
    // rpx → px：chatHeight 是按 px 算的，设计稿的 rpx 要先换算
    const rpx = windowWidth / 750;
    // 输入栏让出的空间（首帧估算值，onReady 里会按实测值修正）：
    // 卡片 + 卡片下边距 + 与对话区的间距，安全区内边距在下面单独扣
    const senderH = SENDER_CARD_RPX * rpx + SENDER_MARGIN_BOTTOM_PX + SENDER_GAP_RPX * rpx;

    this._baseH = windowHeight;
    this._curH = windowHeight;
    this._navH = navH;
    this._senderH = senderH;
    this._senderMarginTop = SENDER_MARGIN_TOP_RPX * rpx;
    this._senderGap = SENDER_GAP_RPX * rpx;
    this._measureTimer = 0;
    this._kbHeight = 0;

    // 抽屉内容避让状态栏与菜单按钮
    const drawerTop = statusBarHeight + 44;
    const chatHeight = Math.max(160, windowHeight - navH - senderH - padBottom);

    this.setData({
      drawerTop,
      padBottom,
      senderPadBottom: padBottom,
      keyboardOffset: 0,
      chatHeight
    });
    this.scrollToBottom();

    if (wx.onWindowResize) {
      this._onResize = (res) => {
        const h = (res && res.size && res.size.windowHeight) || this._baseH;
        if (h === this._curH) return;
        this._curH = h;
        this.applyKeyboard();
      };
      wx.onWindowResize(this._onResize);
    }
  },


  onReady() {
    // 导航栏高度与输入栏高度都按胶囊位置 / 组件样式反算，机型间有出入，
    // 首屏渲染后实测一次修正估算值，避免最后一条消息被输入栏压住
    this.measureNavbar();
    this.measureSender();
  },

  onUnload() {
    if (this._onResize && wx.offWindowResize) wx.offWindowResize(this._onResize);
    if (this._scrollTimer) clearTimeout(this._scrollTimer);
    if (this._measureTimer) clearTimeout(this._measureTimer);
    // 录音/播放器：走的时候停干净，否则后台还能听到声音
    if (this._recordTimer) clearTimeout(this._recordTimer);
    if (this._audio) {
      this._audio.stop();
      this._audio.destroy();
    }
  },

  // placeholder 撑出的高度就是导航栏实际占用的高度（含状态栏）
  measureNavbar() {
    if (!wx.createSelectorQuery) return;
    wx.createSelectorQuery()
      .in(this)
      .select('.t-navbar__placeholder')
      .boundingClientRect((rect) => {
        if (!rect || !rect.height) return;
        this._navH = rect.height;
        this.applyKeyboard();
      })
      .exec();
  },

  // sender-wrap 是绝对定位容器（自成 BFC，子元素外边距不会合并出去），
  // 它的高度 = 卡片上外边距 + 卡片 + 卡片下外边距 + 容器的下内边距。
  // 扣掉上外边距与下内边距（padBottom 在对话区高度里另算）就是卡片 + 下边距，
  // 再加上设计间距，才是对话区真正要让出的空间。
  // 注意下内边距要读实时的 senderPadBottom：键盘弹起时它是 0
  measureSender() {
    if (!wx.createSelectorQuery) return;
    wx.createSelectorQuery()
      .in(this)
      .select('.sender-wrap')
      .boundingClientRect((rect) => {
        if (!rect || !rect.height) return;
        this._senderH =
          rect.height - this.data.senderPadBottom - this._senderMarginTop + this._senderGap;
        this.applyKeyboard();
      })
      .exec();
  },

  // 输入多行时卡片会长高（textarea 开了 auto-height），键盘收放后重测一次。
  // 这里做节流：连续触发只保留最后一次，且测量本身不会再触发新任务，不会成环
  scheduleMeasureSender() {
    if (this._measureTimer) clearTimeout(this._measureTimer);
    this._measureTimer = setTimeout(() => {
      this._measureTimer = 0;
      this.measureSender();
    }, 320);
  },

  // chat-sender 的输入事件
  onInput(e) {
    this.setData({ input: (e && e.detail && e.detail.value) || '' });
  },

  onSend() {
    const text = (this.data.input || '').trim();
    if (!text || this.data.sending) return;

    this.setData({
      messages: this.data.messages.concat([userMsg(text)]),
      input: '',
      sending: true
    });
    this.scrollToBottom();

    // 演示：模拟 AI 流式返回
    setTimeout(() => {
      const reply =
        '好的，正在为你检索「' + text + '」相关行情。当前为演示 UI，暂未接入真实行情数据。';
      this.setData({
        messages: this.data.messages.concat([assistantMsg(reply)]),
        sending: false
      });
      this.scrollToBottom();
    }, 600);
  },

  // 键盘弹起后的布局：输入栏容器整体上移 kb，对话区同步收窄 kb，
  // 两者共用同一个键盘高度，保证卡片与对话区的间距始终是设计稿的 29rpx
  applyKeyboard() {
    const alreadyShifted = Math.max(0, this._baseH - this._curH);
    // 键盘弹起时窗口可能已被系统收缩（Android 会 resize，iOS 不 resize），
    // 减掉已收缩的部分，否则键盘高度会被重复计算
    const kb = Math.max(0, (this._kbHeight || 0) - alreadyShifted);
    // 键盘弹起时安全区落在键盘之下，输入栏不再为它留白
    const senderPad = kb > 0 ? 0 : this.data.padBottom;
    const availH = this._curH - kb;
    // 兜底值取小值：兜得太高会让对话区反向溢出到输入栏上，把卡片盖住
    const chatHeight = Math.max(80, availH - this._navH - this._senderH - senderPad);
    if (
      chatHeight === this.data.chatHeight &&
      kb === this.data.keyboardOffset &&
      senderPad === this.data.senderPadBottom
    ) {
      return;
    }
    this.setData({ chatHeight, keyboardOffset: kb, senderPadBottom: senderPad });
  },

  onKeyboardHeightChange(e) {
    const height = (e && e.detail && e.detail.height) || 0;
    const kb = height > 0 ? height : 0;
    if (kb === this._kbHeight) return;
    this._kbHeight = kb;
    this.applyKeyboard();
    // 输入过程中卡片会随行数变高（textarea 开了 auto-height），键盘收/起稳定后再量一次
    this.scheduleMeasureSender();
  },

  /* ===== 语音输入 ===== */

  // 录音管理器和音频播放器各自都是单例，建一次复用。
  // 挂在 this 上而不是 data 里：data 会参与视图 diff，塞这种对象只会白白加重开销
  initVoice() {
    this._recorder = wx.getRecorderManager();
    this._audio = wx.createInnerAudioContext();

    this._recorder.onStop((res) => {
      const path = (res && res.tempFilePath) || '';
      const ms = (res && res.duration) || 0;
      // 是否取消要在状态复位之前读出来：onRecordEnd 里已经把结果放进 _recordCanceled
      const canceled = this._recordCanceled;

      this._recording = false;
      this.setData({ recording: false, recordCanceling: false, recordTip: '按住 说话' });

      if (canceled || !path) return;
      if (ms < REC_MIN_MS) {
        wx.showToast({ icon: 'none', title: '说话时间太短', duration: 1200 });
        return;
      }
      this.sendVoice(path, Math.max(1, Math.round(ms / 1000)));
    });

    this._recorder.onError(() => {
      this._recording = false;
      this.setData({ recording: false, recordCanceling: false, recordTip: '按住 说话' });
      wx.showToast({ icon: 'none', title: '录音失败，请重试' });
    });

    // 播完或播不动都要把 playing 标记撤掉，按钮变回播放态
    this._audio.onEnded(() => this.stopVoiceUI());
    this._audio.onError(() => this.stopVoiceUI());
  },

  onRecordStart(e) {
    if (this._recording) return;

    // 麦克风权限：之前拒绝过的话 authorize 会直接失败，
    // 那时只能让用户去设置页打开，这里给个明确出口，别点了没反应
    wx.authorize({
      scope: 'scope.record',
      success: () => this.startRecord(e),
      fail: () => {
        wx.showModal({
          title: '需要麦克风权限',
          content: '发送语音需要麦克风权限，是否去设置页开启？',
          confirmText: '去设置',
          success: (r) => {
            if (r.confirm) wx.openSetting();
          }
        });
      }
    });
  },

  startRecord(e) {
    const touch = e && e.touches && e.touches[0];
    this._startY = (touch && touch.clientY) || 0;
    this._recording = true;
    this._recordCanceled = false;

    this.setData({ recording: true, recordCanceling: false, recordTip: '松开 发送' });
    this._recorder.start({ format: 'mp3', duration: REC_MAX_MS });

    // 录满自动停：onStop 会照常走发送流程
    clearTimeout(this._recordTimer);
    this._recordTimer = setTimeout(() => {
      if (this._recording) this._recorder.stop();
    }, REC_MAX_MS);
  },

  // 上滑取消：越过阈值才算，免得手指轻微抖动就触发
  onRecordMove(e) {
    if (!this._recording) return;
    const touch = e && e.touches && e.touches[0];
    const y = (touch && touch.clientY) || this._startY;
    const cancel = this._startY - y > CANCEL_DISTANCE;
    if (cancel === this.data.recordCanceling) return;
    this.setData({
      recordCanceling: cancel,
      recordTip: cancel ? '松开手指，取消发送' : '松开 发送'
    });
  },

  onRecordEnd() {
    if (!this._recording) return;
    clearTimeout(this._recordTimer);
    this._recordCanceled = !!this.data.recordCanceling;
    // stop 是异步的，录音结果在 onStop 里收，这里不立刻收尾
    this._recorder.stop();
  },

  sendVoice(path, seconds) {
    this.setData({ messages: this.data.messages.concat([voiceMsg(path, seconds)]) });
    this.scrollToBottom();
    this.replyToVoice(seconds);
  },

  // 演示回复：真实场景这一步是把语音送去识别，再用识别出的文字走一遍问答
  replyToVoice(seconds) {
    this.setData({ sending: true });
    setTimeout(() => {
      this.setData({
        messages: this.data.messages.concat([
          assistantMsg('收到 ' + seconds + ' 秒语音。当前为演示 UI，语音还没接识别服务。')
        ]),
        sending: false
      });
      this.scrollToBottom();
    }, 600);
  },

  // 点语音气泡：点正在播的那条就停，点别的就切过去播
  playVoice(e) {
    const index = Number((e && e.currentTarget.dataset && e.currentTarget.dataset.index) || -1);
    const msg = this.data.messages[index];
    if (!msg || !msg.voice) return;

    if (this.data.playingIndex === index) {
      this._audio.stop();
      this.stopVoiceUI();
      return;
    }

    this._audio.stop();
    this._audio.src = msg.voice.path;
    this._audio.play();

    const messages = this.data.messages.map((m, i) => {
      if (!m.voice) return m;
      return Object.assign({}, m, {
        voice: Object.assign({}, m.voice, { playing: i === index })
      });
    });
    this.setData({ messages: messages, playingIndex: index });
  },

  // playingIndex 与 messages 里的 playing 是同一件事的两份记录：
  // 后者驱动气泡上的图标，前者用来判断「这次点的是不是正在播的那条」
  stopVoiceUI() {
    const messages = this.data.messages.map((m) =>
      m.voice && m.voice.playing
        ? Object.assign({}, m, { voice: Object.assign({}, m.voice, { playing: false }) })
        : m
    );
    this.setData({ messages: messages, playingIndex: -1 });
  },

  scrollToBottom() {
    if (this._scrollTimer) clearTimeout(this._scrollTimer);
    // scroll-into-view 在值不变时不会重新滚动，先清空再置位
    this.setData({ scrollIntoView: '' });
    // 等新消息渲染完成（卡片较多时首次渲染会慢于本次调用）再定位
    this._scrollTimer = setTimeout(() => {
      this.setData({ scrollIntoView: CHAT_BOTTOM_ID });
    }, 80);
  },

  // ===== 侧边抽屉 =====
  onMenuTap() {
    this._pendingMenu = '';
    this.setData({ drawerOpen: true });
  },

  // popup 的 visible-change：detail.visible 为目标状态（点遮罩关闭时也会触发）
  onDrawerVisibleChange(e) {
    const visible = !!(e && e.detail && e.detail.visible);
    if (visible !== this.data.drawerOpen) this.setData({ drawerOpen: visible });
  },

  // 抽屉只在主页面存在，子页面没有，因此不保留「当前页」状态，
  // 每一项都是纯粹的跳转入口。
  // 这里只负责「开始收起」并记下要去哪，真正的跳转放到收起动画结束之后：
  // popup 的 transition 是靠定时器收尾的，若同一帧就 navigateTo，
  // 新页面会直接盖上来把收起动画掐断，真机上看着就是「抽屉没收起」
  onMenuSelect(e) {
    this._pendingMenu = e.currentTarget.dataset.menu || '';
    if (!this.data.drawerOpen) {
      // 理论上不会走到这里（菜单只在抽屉展开时可点），兜底直接跳
      this.flushPendingNav();
      return;
    }
    this.setData({ drawerOpen: false });
  },

  // popup 收起动画结束（transition mixin 的 leaved 事件）：此刻再跳转，动画已完整播完
  onDrawerLeaved() {
    this.flushPendingNav();
  },

  flushPendingNav() {
    const menu = this._pendingMenu;
    this._pendingMenu = '';
    const url = menu && ROUTES[menu];
    if (url) wx.navigateTo({ url });
  },

  // ===== 手势开关抽屉：右滑打开，左滑关闭 =====
  onSwipeStart(e) {
    this._startX = e.touches[0].clientX;
    this._startY = e.touches[0].clientY;
    this._lastX = this._startX;
    this._lastY = this._startY;
    this._swipeLocked = false;
  },

  onSwipeMove(e) {
    this._lastX = e.touches[0].clientX;
    this._lastY = e.touches[0].clientY;
    if (this._swipeLocked) return;
    const dx = this._lastX - this._startX;
    const dy = this._lastY - this._startY;
    if (Math.abs(dx) < SWIPE_MIN || Math.abs(dx) <= Math.abs(dy)) return;

    this._swipeLocked = true;
    if (dx > 0 && !this.data.drawerOpen) {
      this.setData({ drawerOpen: true });
    } else if (dx < 0 && this.data.drawerOpen) {
      this.setData({ drawerOpen: false });
    }
  },

  onSwipeEnd() {
    this._swipeLocked = false;
  },

  onTodo(e) {
    const name = (e && e.currentTarget.dataset && e.currentTarget.dataset.name) || '该功能';
    Toast({ context: this, message: name + '（演示）' });
  }
});
