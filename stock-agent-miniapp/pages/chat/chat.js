// 对话页：消息收发走 Agent WebSocket（services/agent.js 单例），流式 token 逐字追加；
// 纯文本走 TDesign chat-message 的 text 内容，行情/K 线这类结构化数据走自绘卡片消息

import agentSocket from '../../services/agent';
import { quote } from '../../services/stock';
import { transcribe } from '../../services/asr';

// 侧边抽屉导航项（TDesign cell-group / cell）
// 不列「主页」：抽屉本来就只在主页存在，这一项点了没地方去
const MENUS = [
  { key: 'watchlist', label: '自选股', icon: 'star' },
  { key: 'alerts', label: '提醒', icon: 'notification' },
  { key: 'settings', label: '我的', icon: 'user' }
];

// 抽屉导航项 → 子页面路由，key 与 MENUS 一一对应
const ROUTES = {
  watchlist: '/pages/watchlist/watchlist',
  alerts: '/pages/alerts/alerts',
  settings: '/pages/settings/settings'
};

// 今日大盘指数：代码 -> 展示名。实时行情在 onLoad 与抽屉打开时从后端 /stock/quote 拉取。
// 指数代码必须带市场前缀（SH/SZ），否则 6 位纯数字会被后端按个股规则误判市场
const MARKET_INDICES = [
  { code: 'SH000001', name: '上证指数' },
  { code: 'SZ399001', name: '深证成指' },
  { code: 'SZ399006', name: '创业板指' },
  { code: 'SH000680', name: '科创综指' }
];

// 首屏占位：实时数据到位前先显示名称 + 占位符，避免抽屉里一片空白
const MARKETS_PLACEHOLDER = MARKET_INDICES.map((m) => ({ name: m.name, value: '--', pct: '--', up: true }));

// 指数点位加千分位：3718.44 -> 3,718.44（不用 toLocaleString，小程序端支持不稳）
function formatIndex(v) {
  const n = parseFloat(v);
  if (isNaN(n)) return v == null ? '--' : String(v);
  const fixed = n.toFixed(2);
  const dot = fixed.indexOf('.');
  return fixed.slice(0, dot).replace(/\B(?=(\d{3})+(?!\d))/g, ',') + fixed.slice(dot);
}

// 触发抽屉开关的最小横向滑动距离（px）
const SWIPE_MIN = 40;

// 发送看门狗：超过这个时长一个字都没回来，就认为这一轮没有响应（秒）
const SEND_TIMEOUT_MS = 45000;

// 对话列表底部锚点：scroll-into-view 滚动到它即可到达列表末尾
const CHAT_BOTTOM_ID = 'chat-bottom';

// 输入栏胶囊高度（rpx）：取「两行」时的高度，见 chat.wxss 的 .sender-bar
//   .sender-bar 内边距 8rpx×2 + 边框 2rpx×2 = 20rpx
//   .bar-field 内边距 14rpx×2 = 28rpx
//   .bar-input 两行 88rpx（line-height 44rpx × 2，超出内部滚动）
//   合计 136rpx；单行时会被 min-height 96rpx 兜住
// 按最大高度预留，首屏才不会把最后一条消息盖住（onReady 实测后会按真实高度修正）
const SENDER_BAR_RPX = 136;
// 胶囊上方留白（rpx）：取自 .sender-wrap 的 padding-top（--sp-2）
const SENDER_PAD_TOP_RPX = 16;
// 胶囊下方留白（rpx）：取自 .sender-bar 的 margin-bottom（--sp-2），
// 让胶囊整体离屏幕底边上移一档。与 chatHeight 的估算一起算，
// 否则首帧会少让出这 8px（onReady 实测后会修正，但首帧会抖一下）
const SENDER_BAR_MARGIN_BOTTOM_RPX = 16;
// 卡片外边距：自绘胶囊后没有组件外边距了，只保留底部与安全区之间的这段距离
const SENDER_MARGIN_TOP_RPX = 0;
const SENDER_MARGIN_BOTTOM_PX = 12;
// 对话区底边与输入栏卡片之间的间距（rpx）。
// 原值 29 里含组件 margin-top 的 24rpx；margin-top 归零后改为 5，
// 卡片是底部锚定的，这里不同步减的话那段空隙会原样留着
const SENDER_GAP_RPX = 5;
// 键盘弹起时，输入栏在键盘顶边之上再抬的间距（rpx）。
// 只按键盘高度上抬的话，胶囊底边正好压在键盘顶边上：机型不同（键盘高度、
// 安全区、是否有候选栏）看起来时紧时松。这里统一加一段固定间距，
// 与键盘多高无关；用 rpx 是为了跟其他间距一样随屏幕宽度等比缩放，
// 各尺寸下观感一致（换算在 onLoad 里做，chatHeight 用的是 px）
const KEYBOARD_GAP_RPX = 16;

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
    // type 用 markdown：chat-content 只对 markdown 走 t-chat-markdown（marked 解析），
    // text 会被当纯文本转义输出，**加粗**、- 列表、表格都会原样露出来
    content: [{ type: 'markdown', data }]
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
  // 后端 QuoteDetail 的涨跌幅字段是 change_percent（如 "-0.33"），且没有 meta 字段；
  // 没有的信息在页面上拼出来，缺了就留空
  const pctNum = parseFloat(q.change_percent);
  const pct = isNaN(pctNum) ? '--' : (pctNum > 0 ? '+' : '') + pctNum.toFixed(2) + '%';
  const meta = [
    q.time || '',
    q.volume ? '量 ' + q.volume + '手' : '',
    q.amount ? '额 ' + q.amount + '万' : ''
  ].filter(Boolean).join(' · ');
  return {
    role: 'assistant',
    status: 'complete',
    card: 'quote',
    content: [],
    quote: Object.assign({}, q, { pct: pct, meta: meta, up: isUp(pct) })
  };
}

// 日 K 卡片最多渲染的行数。后端一次可能给几十根 K 线，全量渲染会把对话区撑爆、
// 流式期间每帧重排造成画面抖动；卡片只做「最近走势」概览，取最后 _KLINE_MAX_ROWS 行
const KLINE_MAX_ROWS = 10;

function klineCard(rows) {
  // 后端 KlineRow 字段是 date / close / change_percent，
  // 卡片模板展示「日期 / 收盘 / 涨跌幅」，在这里做映射
  const all = Array.isArray(rows) ? rows : [];
  const shown = all.slice(-KLINE_MAX_ROWS);
  const kline = shown.map((r) => {
    const pctNum = parseFloat(r.change_percent);
    const pct = isNaN(pctNum) ? '--' : (pctNum > 0 ? '+' : '') + pctNum.toFixed(2) + '%';
    return { d: r.date, c: r.close, p: pct, up: !isNaN(pctNum) && pctNum >= 0 };
  });
  return {
    role: 'assistant',
    status: 'complete',
    card: 'kline',
    content: [],
    kline: kline,
    // 副标题按实际渲染的行数走：行数被截断时如实标出「最近 N 个交易日」
    klineSub: '最近 ' + kline.length + ' 个交易日 · 前复权' + (all.length > kline.length ? '（共 ' + all.length + ' 根）' : '')
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

// 流式渲染节奏：合批间隔(ms)、不足一整行时至少攒够多少字符才落视图、
// 滚动跟随的去抖间隔(ms)。三者共同决定「多久重排一次」
const STREAM_FLUSH_MS = 100;
const STREAM_MIN_DELTA = 40;
const SCROLL_FOLLOW_MS = 80;

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
    // 对话区滚动位置：用数值 scroll-top 控制。
    // 旧写法 scroll-into-view 每次都要「先清空锚点再置位」，流式期间
    // 会不停重启滚动动画，看起来就是画面上下抽动。
    // scrollAnimated 只在非流式场景（发消息、插卡片）打开，流式跟随一律瞬移
    scrollTop: 0,
    scrollAnimated: false,
    menus: MENUS,
    markets: MARKETS_PLACEHOLDER,

    // 输入模式：false = 键盘输入，true = 语音输入（模式键在两者之间切换）
    voiceMode: false,
    // textarea 的受控 focus：切回键盘态时靠它主动唤起键盘
    inputFocus: false,
    // 语音输入：recording = 手指按住中，recordCanceling = 已上滑到取消区
    recording: false,
    recordCanceling: false,
    recordTip: '按住说话',
    // 正在播放的语音消息下标，-1 表示当前没有在播
    playingIndex: -1,
    messages: [
      assistantMsg('你好，我是溜来。你可以问我行情、K 线、研报等问题。')
    ],
    // 状态提示（「正在思考…」「正在查询…」）：挂在消息列表底部，done/error 后清空
    statusText: ''
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
    // 胶囊 + 上下留白 + 与对话区的间距 + 底部余量，安全区内边距在下面单独扣
    const senderH =
      (SENDER_BAR_RPX + SENDER_PAD_TOP_RPX + SENDER_BAR_MARGIN_BOTTOM_RPX + SENDER_GAP_RPX) * rpx +
      SENDER_MARGIN_BOTTOM_PX;

    this._baseH = windowHeight;
    this._curH = windowHeight;
    this._navH = navH;
    this._senderH = senderH;
    this._senderMarginTop = SENDER_MARGIN_TOP_RPX * rpx;
    this._senderGap = SENDER_GAP_RPX * rpx;
    // 键盘弹起时额外上抬的间距（px）：键盘高度是 px，这里一次性换算好
    this._keyboardGap = KEYBOARD_GAP_RPX * rpx;
    this._measureTimer = 0;
    this._sendWatchdog = 0;
    this._kbHeight = 0;
    // 流式输出追踪：当前 streaming 消息在 messages 里的下标（-1 表示无）、
    // 累计文本、已落到视图层的文本、合批定时器
    this._streamIndex = -1;
    this._streamText = '';
    this._streamFlushed = '';
    this._streamTimer = 0;
    // 滚动跟随状态：是否停在底部（用户上翻看历史时不再被拽回去）、
    // 最近一次写入视图的 scroll-top
    this._atBottom = true;
    this._scrollTopVal = 0;

    // 抽屉内容避让状态栏：statusBarHeight 下方再留 28px（原 44，按需求整体上移一点）。
    // 抽屉在左侧、胶囊菜单在右上，横向不重叠，故减小留白不会撞上菜单按钮
    const drawerTop = statusBarHeight + 28;
    const chatHeight = Math.max(160, windowHeight - navH - senderH - padBottom);

    this.setData({
      drawerTop,
      padBottom,
      senderPadBottom: padBottom,
      keyboardOffset: 0,
      chatHeight
    });
    this.scrollToBottom({ force: true });
    // 预热四大指数行情：抽屉首次打开前就把数据备好
    this.loadMarkets();

    if (wx.onWindowResize) {
      this._onResize = (res) => {
        const h = (res && res.size && res.size.windowHeight) || this._baseH;
        if (h === this._curH) return;
        this._curH = h;
        this.applyKeyboard();
      };
      wx.onWindowResize(this._onResize);
    }

    // 接入 Agent WS：先挂监听再连接，免得漏掉 onOpen 后立即到达的事件
    this._initAgentSocket();
  },

  // ===== Agent WebSocket =====
  // agentSocket 是单例，连接跨页面生命周期复用；这里只负责挂/摘本页的监听
  _initAgentSocket() {
    // 流式 token：逐字追加到当前 streaming 状态的 assistant 消息。
    // 性能约束：token 频率很高，不能每次都把整个 messages 回传给视图层——
    // 增量先积在 _streamText 里，按帧（约 100ms）合批后用路径级 setData
    // （messages[i].content[0].data）只更新那一段文本
    agentSocket.on('token', (data) => {
      const delta = (data && data.delta) || '';
      if (!delta) return;
      this._appendToken(delta);
    });

    // 行情卡片：结构化数据，直接复用现有的 quoteCard 渲染。
    // 插入卡片后要把当前流式消息收尾（_finishStreaming）：
    // 后端常见的节奏是「文字 → 卡片 → 文字」，不断流的话
    // 卡片之后的 token 会被追加到卡片上方的旧消息里，顺序就乱了
    agentSocket.on('card', (data) => {
      if (!data || data.type !== 'quote_card' || !data.payload) return;
      this._flushStream({ force: true });
      this._finishStreaming();
      this.setData({ messages: this.data.messages.concat([quoteCard(data.payload)]) });
      this.scrollToBottom({ force: true });
    });

    // K 线表格：payload 约定为 { rows: [...] }，兼容直接给数组
    agentSocket.on('table', (data) => {
      if (!data || data.type !== 'kline_table' || !data.payload) return;
      const rows = data.payload.rows || data.payload;
      if (!Array.isArray(rows) || !rows.length) return;
      this._flushStream({ force: true });
      this._finishStreaming();
      this.setData({ messages: this.data.messages.concat([klineCard(rows)]) });
      this.scrollToBottom({ force: true });
    });

    // 后端主动推的状态提示
    agentSocket.on('status', (data) => {
      this.setData({ statusText: (data && data.label) || '' });
    });

    // 工具调用：开始查行情/研报时给个可读的提示，结束后清掉
    agentSocket.on('tool_start', (data) => {
      this.setData({ statusText: (data && data.label) || '正在查询...' });
    });

    agentSocket.on('tool_end', () => {
      this.setData({ statusText: '' });
    });

    // 错误：复位发送态并提示，不往消息列表里塞错误气泡
    agentSocket.on('error', (data) => {
      this._flushStream({ force: true });
      this._finishStreaming();
      this._clearSendWatchdog();
      this.setData({ sending: false, statusText: '' });
      wx.showToast({
        icon: 'none',
        title: (data && data.msg) || '服务异常',
        duration: 2000
      });
    });

    // 本轮回答结束：流式消息转 complete，解锁发送按钮
    agentSocket.on('done', () => {
      this._flushStream({ force: true });
      this._finishStreaming();
      this._clearSendWatchdog();
      this.setData({ sending: false, statusText: '' });
    });

    // 断连：连接层会自动重连，这里只在正在等回答时给个提示
    agentSocket.on('disconnected', () => {
      if (this.data.sending) {
        this.setData({ statusText: '连接已断开，正在重连...' });
      }
    });

    agentSocket.connect();
  },

  // 把增量文本积进缓冲；首个 token 到达时确定目标消息（没有 streaming 消息
  // 就新建一条）并立即刷一帧，后续 token 只积缓冲，由定时器合批刷新
  _appendToken(delta) {
    // 已经有内容在回来，说明这一轮是活的，撤掉看门狗
    this._clearSendWatchdog();
    if (this._streamIndex < 0) {
      const messages = this.data.messages;
      const last = messages[messages.length - 1];
      if (last && last.role === 'assistant' && last.status === 'streaming') {
        this._streamIndex = messages.length - 1;
        this._streamText = (last.content[0] && last.content[0].data) || '';
      } else {
        this._streamIndex = messages.length;
        this._streamText = '';
        this.setData({
          messages: messages.concat([
            {
              role: 'assistant',
              status: 'streaming',
              // 与 assistantMsg 一致用 markdown：流式期间 chat-markdown 每次合批刷新
              // 都会整段重新解析，能正确渲染未完成段落到收敛的整个过程
              content: [{ type: 'markdown', data: '' }]
            }
          ]),
          statusText: ''
        });
      }
    }
    this._streamText += delta;
    if (!this._streamTimer) {
      // 合批：窗口内连续到达的 token 只安排一次刷新。
      // 滚动交给 _flushStream 在 setData 回调里做 —— 回调触发时新内容已经渲染完，
      // 量到的是新高度，一次滚到位；若在刷新前就滚，会先按旧高度定位、
      // 下一帧再补一次，表现出来就是上下跳
      const first = this._streamFlushed === '';
      this._streamTimer = setTimeout(() => {
        this._streamTimer = 0;
        this._flushStream({ afterRender: true });
      }, first ? 0 : STREAM_FLUSH_MS);
    }
  },

  // 把缓冲区落到视图：只更新目标消息的文本字段（路径级 setData）。
  // opts.force  = 收尾（done / error / 插卡片）时把缓冲一字不落地渲染完
  // opts.afterRender = 刷新完成后再跟随滚动，用于流式逐帧刷新
  _flushStream(opts) {
    const force = !!(opts && opts.force);
    const afterRender = !!(opts && opts.afterRender);
    if (this._streamTimer) {
      clearTimeout(this._streamTimer);
      this._streamTimer = 0;
    }
    if (this._streamIndex === undefined || this._streamIndex < 0) return;
    if (this._streamFlushed === this._streamText) return;

    let text = this._streamText;
    if (!force) {
      // 只在「行边界」落视图：半行文本、以及未闭合的 markdown 块（表格、代码 fence、
      // 列表项）都会让气泡高度忽大忽小，已渲染内容因此上下跳。
      // 把不完整的一行留在缓冲里，等下一批凑齐整行再一起渲染，
      // 这样气泡高度只会单调递增，视觉上就是平滑长高
      const cut = text.lastIndexOf('\n');
      if (cut > this._streamFlushed.length) {
        text = text.slice(0, cut + 1);
      } else if (text.length - this._streamFlushed.length < STREAM_MIN_DELTA) {
        // 一行都没攒完、增量也很小：等下一批，避免每个 token 都触发一次重排
        return;
      }
      // 兜底：一直等不到换行（超长单行）时，攒够 STREAM_MIN_DELTA 也照常渲染
    }
    if (text === this._streamFlushed) return;

    this._streamFlushed = text;
    const patch = {};
    patch['messages[' + this._streamIndex + '].content[0].data'] = text;
    if (afterRender) {
      // 渲染完成后再跟随滚动：回调里量到的高度是新的，滚动一次到位
      this.setData(patch, () => this.scrollToBottom());
    } else {
      this.setData(patch);
    }
  },
  // 流式消息收尾：status 转 complete，复位流式追踪状态。
  // done 和卡片插入都走这里：后者是为了让卡片之后的 token 另起一条消息，
  // 两者都不碰 sending（是否解锁发送按钮只由 done / error 决定）
  _finishStreaming() {
    const idx = this._streamIndex;
    this._streamIndex = -1;
    this._streamText = '';
    this._streamFlushed = '';
    if (idx < 0) return;
    const patch = {};
    patch['messages[' + idx + '].status'] = 'complete';
    this.setData(patch);
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
    if (this._streamTimer) clearTimeout(this._streamTimer);
    this._clearSendWatchdog();
    // 录音/播放器：走的时候停干净，否则后台还能听到声音
    if (this._recordTimer) clearTimeout(this._recordTimer);
    if (this._audio) {
      this._audio.stop();
      this._audio.destroy();
    }
    // 摘掉本页在 agent 单例上挂的全部监听；连接本身保留（单例跨页面复用）
    agentSocket.off('token');
    agentSocket.off('card');
    agentSocket.off('table');
    agentSocket.off('status');
    agentSocket.off('tool_start');
    agentSocket.off('tool_end');
    agentSocket.off('error');
    agentSocket.off('done');
    agentSocket.off('disconnected');
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

  // 输入事件（textarea）
  onInput(e) {
    this.setData({ input: (e && e.detail && e.detail.value) || '' });
    // 单行 -> 两行时胶囊会变高，对话区要让出相应空间，否则最后一条消息会被压住。
    // 这里走节流测量：连续打字只在停顿时量一次
    this.scheduleMeasureSender();
  },

  // focus 由受控字段回写：切回键盘态时要靠它主动唤起键盘，
  // 用户自己点输入框时也会走到这里，保持视图与状态一致
  onInputFocus() {
    if (!this.data.inputFocus) this.setData({ inputFocus: true });
  },

  onInputBlur() {
    if (this.data.inputFocus) this.setData({ inputFocus: false });
  },

  // 键盘 / 语音互切。语音态下整块不渲染 textarea，键盘随之收起；
  // 再点一次切回键盘态，并主动聚焦把键盘唤起来
  onToggleInputMode() {
    // 正在录音时不允许切：切走会让手指还按着的那次录音没有归宿
    if (this._recording) return;

    if (this.data.voiceMode) {
      this.setData({ voiceMode: false, inputFocus: true });
      return;
    }

    this.setData({ voiceMode: true, inputFocus: false });
    // 组件移除后键盘并不会立刻收，这里显式收一次
    if (wx.hideKeyboard) wx.hideKeyboard();
    // 键盘高度事件不一定补发 0，手动归零，防止输入栏悬在半空
    this._kbHeight = 0;
    this.applyKeyboard();
  },

  onSend() {
    this.sendText(this.data.input);
  },

  // 发送一条文本：打字与语音识别结果走同一条链路，
  // 避免「识别出来的话」和「打出来的话」在界面上表现不一致
  sendText(raw) {
    const text = (raw || '').trim();
    if (!text || this.data.sending) return;

    // 上一轮流式状态先复位（正常情况下 done 已经收过尾，这里是兜底）
    this._flushStream({ force: true });
    this._finishStreaming();

    this.setData({
      messages: this.data.messages.concat([userMsg(text)]),
      input: '',
      sending: true,
      statusText: '正在思考...'
    });
    this.scrollToBottom({ force: true });
    this._armSendWatchdog();

    // 通过 WS 发送；未连接时 agentSocket 内部会先缓存并触发连接
    agentSocket.sendMessage(text);
  },

  // 发送看门狗：sending 只在 done / error 里复位，服务没起、WS 没连上或消息被缓存
  // 却始终没建立连接时，这两个事件永远不来，sending 就一直为 true，
  // 之后每次发送都被 sendText 开头那句静默吞掉 —— 表现为「点了发送没反应」。
  // 收到第一个 token 说明链路活着，立刻撤掉（见 _appendToken）
  _armSendWatchdog() {
    this._clearSendWatchdog();
    this._sendWatchdog = setTimeout(() => {
      this._sendWatchdog = 0;
      if (!this.data.sending) return;
      this.setData({ sending: false, statusText: '' });
      wx.showToast({ icon: 'none', title: '服务暂时没有响应，请重试', duration: 2000 });
    }, SEND_TIMEOUT_MS);
  },

  _clearSendWatchdog() {
    if (this._sendWatchdog) {
      clearTimeout(this._sendWatchdog);
      this._sendWatchdog = 0;
    }
  },

  // 键盘弹起后的布局：输入栏容器整体上移 kb，对话区同步收窄 kb，
  // 两者共用同一个键盘高度，保证卡片与对话区的间距始终是设计稿的 29rpx
  applyKeyboard() {
    const alreadyShifted = Math.max(0, this._baseH - this._curH);
    // 键盘弹起时窗口可能已被系统收缩（Android 会 resize，iOS 不 resize），
    // 减掉已收缩的部分，否则键盘高度会被重复计算
    const kb = Math.max(0, (this._kbHeight || 0) - alreadyShifted);
    // 输入栏的上抬量 = 键盘高度 + 固定间距：只抬键盘高度的话胶囊会贴着键盘顶边
    const lift = kb > 0 ? kb + (this._keyboardGap || 0) : 0;
    // 键盘弹起时安全区落在键盘之下，输入栏不再为它留白
    const senderPad = kb > 0 ? 0 : this.data.padBottom;
    const availH = this._curH - lift;
    // 兜底值取小值：兜得太高会让对话区反向溢出到输入栏上，把卡片盖住
    const chatHeight = Math.max(80, availH - this._navH - this._senderH - senderPad);
    if (
      chatHeight === this.data.chatHeight &&
      lift === this.data.keyboardOffset &&
      senderPad === this.data.senderPadBottom
    ) {
      return;
    }
    this.setData({ chatHeight, keyboardOffset: lift, senderPadBottom: senderPad });
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
      this.setData({ recording: false, recordCanceling: false, recordTip: '按住说话' });

      if (canceled || !path) return;
      if (ms < REC_MIN_MS) {
        wx.showToast({ icon: 'none', title: '说话时间太短', duration: 1200 });
        return;
      }
      this.sendVoice(path, Math.max(1, Math.round(ms / 1000)));
    });

    this._recorder.onError(() => {
      this._recording = false;
      this.setData({ recording: false, recordCanceling: false, recordTip: '按住说话' });
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

  // 松手后的语音流程：先把语音气泡放出去（用户说了什么要看得见、能回放），
  // 再把录音送去识别，识别成文字后按普通文本发送
  sendVoice(path, seconds) {
    this.setData({
      messages: this.data.messages.concat([voiceMsg(path, seconds)]),
      sending: true,
      statusText: '语音识别中...'
    });
    this.scrollToBottom({ force: true });

    transcribe(path)
      .then((text) => {
        // 先摘掉「识别中」，再走发送：sendText 内部会拦住 sending 为真的情况
        this.setData({ sending: false, statusText: '' });
        this.sendText(text);
      })
      .catch((err) => {
        this.setData({ sending: false, statusText: '' });
        // 识别失败不静默：语音气泡已经留在列表里，用户至少能重听自己说了什么
        wx.showToast({
          icon: 'none',
          title: (err && err.message) || '没听清，请再说一次',
          duration: 1500
        });
      });
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

  // 滚到底：改用数值 scroll-top 定位，不再走 scroll-into-view。
  // 旧实现每帧都要「清空锚点 → 重设锚点」，配合滚动动画会不停重启，
  // 长文本流式下表现为画面反复上下抽动。现在：
  // - 按内容高度递增地写 scroll-top（值不变不会触发滚动，天然去抖）
  // - 用户上翻看历史时不跟随，只有新消息 / 卡片这类场景才 force
  // - 流式进行中关闭滚动动画，避免动画过程里的位置回弹
  scrollToBottom(opts) {
    const force = !!(opts && opts.force);
    if (!force && this._atBottom === false) return;
    if (this._scrollTimer) return;
    this._scrollTimer = setTimeout(() => {
      this._scrollTimer = 0;
      const query = wx.createSelectorQuery().in(this);
      query.select('.chat-inner').boundingClientRect();
      query.exec((res) => {
        const rect = res && res[0];
        if (!rect || !rect.height) return;
        // scroll-top 会被自动夹到「内容高度 - 容器高度」，直接给内容总高即可；
        // 内容变长时这个值单调递增，每次都会真正滚到底
        const top = Math.ceil(rect.height);
        if (top <= this._scrollTopVal) return;
        this._scrollTopVal = top;
        this.setData({ scrollTop: top, scrollAnimated: this._streamIndex < 0 });
      });
    }, SCROLL_FOLLOW_MS);
  },

  // 记录用户是否停在底部：由 scroll-view 的 bindscroll 驱动。
  // 只有贴底时才自动跟随新内容，用户往上翻历史不会被拽回去
  onChatScroll(e) {
    const d = e && e.detail;
    if (!d) return;
    const viewH = this.data.chatHeight || 0;
    const maxTop = Math.max(0, (d.scrollHeight || 0) - viewH);
    // 距底部 24px 内都算贴底：容忍小数像素与测量误差
    this._atBottom = maxTop - (d.scrollTop || 0) <= 24;
  },

  // ===== 侧边抽屉 =====
  // 拉取四大指数实时行情 -> 抽屉「今日大盘」。
  // 单只失败降级为占位符（--），不阻断整个抽屉；涨红跌绿由 up 字段驱动
  loadMarkets() {
    Promise.all(MARKET_INDICES.map((m) => quote(m.code).catch(() => null))).then((list) => {
      const markets = MARKET_INDICES.map((m, i) => {
        const d = list[i];
        if (!d) return { name: m.name, value: '--', pct: '--', up: true };
        const pctNum = parseFloat(d.change_percent);
        const num = isNaN(pctNum) ? 0 : pctNum;
        const up = num >= 0; // >=0 红（涨），<0 绿（跌）
        return {
          name: m.name,
          value: formatIndex(d.price),
          pct: (up ? '+' : '') + num.toFixed(2) + '%',
          up
        };
      });
      this.setData({ markets });
    });
  },

  onMenuTap() {
    this._pendingMenu = '';
    this.setData({ drawerOpen: true });
    this.loadMarkets(); // 每次打开抽屉刷新一次（后端 30s 缓存，开销可控）
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
  // 输入栏内的手势起点标记（wxml 用 bindtouchstart 绑到 .sender-wrap）。
  // 用「标记 + 判断」取代原来的 catch：catch 会让整个输入栏的子元素点击失效，
  // 输入框点不动、模式键和发送键全都不响应。这里只记一个信号，
  // 由 onSwipeStart 决定这次手势是否让给输入（光标移动 / 按住说话）
  onSenderTouchStart() {
    this._inSender = true;
  },

  // 点输入区任意位置都聚焦 textarea：原生组件的点击热区未必铺满整块，
  // 给一个明确的聚焦入口，点空白也能唤起键盘
  onTapInputField() {
    if (!this.data.inputFocus) this.setData({ inputFocus: true });
  },

  onSwipeStart(e) {
    this._startX = e.touches[0].clientX;
    this._startY = e.touches[0].clientY;
    this._lastX = this._startX;
    this._lastY = this._startY;
    // 子元素的 bindtouchstart 先于根节点执行，这里读到的是本次手势的起点归属；
    // 读完即清，下一次手势重新判定
    const inSender = this._inSender;
    this._inSender = false;
    // 起点在输入栏（或正在录音）：这一整段手势都不用于开关抽屉。
    // 录音这条是兜底：按住说话时若滑出按钮范围，事件仍可能落到根节点
    this._swipeLocked = !!this._recording || !!inSender;
  },

  onSwipeMove(e) {
    if (this._recording) return;
    this._lastX = e.touches[0].clientX;
    this._lastY = e.touches[0].clientY;
    if (this._swipeLocked) return;
    const dx = this._lastX - this._startX;
    const dy = this._lastY - this._startY;
    if (Math.abs(dx) < SWIPE_MIN || Math.abs(dx) <= Math.abs(dy)) return;

    this._swipeLocked = true;
    if (dx > 0 && !this.data.drawerOpen) {
      this.setData({ drawerOpen: true });
      this.loadMarkets(); // 滑动打开抽屉同样刷新指数
    } else if (dx < 0 && this.data.drawerOpen) {
      this.setData({ drawerOpen: false });
    }
  },

  onSwipeEnd() {
    this._swipeLocked = false;
  }
});
