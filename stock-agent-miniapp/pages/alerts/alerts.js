import Toast from '../../miniprogram_npm/tdesign-miniprogram/toast/index';

// 设计稿数据（来自 Ardot「stock agent / 提醒」，node 2:395）
const ALERTS = [
  {
    id: 1,
    iconType: 'trend-up',
    title: '贵州茅台 突破 1,700.00',
    desc: '价格 ≥ 1,700.00 · 日线收盘确认',
    time: '今日 14:30 触发',
    enabled: true
  },
  {
    id: 2,
    iconType: 'trend-down',
    title: '宁德时代 跌破 235.00',
    desc: '价格 ≤ 235.00 · 即时提醒',
    time: '未触发',
    enabled: true
  },
  {
    id: 3,
    iconType: 'bell',
    title: '比亚迪 涨幅超过 5%',
    desc: '涨跌幅 ≥ 5.00% · 盘中每分钟检查',
    time: '昨日 14:52 触发',
    enabled: true
  },
  {
    id: 4,
    iconType: 'volume',
    title: '招商银行 成交量放大',
    desc: '成交量 ≥ 20 日均量的 1.5 倍',
    time: '已暂停 3 天',
    enabled: false
  }
];

// TDesign tabs：状态筛选
const FILTERS = [
  { key: 'all', label: '全部' },
  { key: 'enabled', label: '已启用' },
  { key: 'paused', label: '已暂停' }
];

// 概览宫格（TDesign grid）数据
function makeStats(alerts) {
  return [
    { key: 'total', num: alerts.length, label: '全部提醒', cls: 'ink' },
    { key: 'enabled', num: alerts.filter((a) => a.enabled).length, label: '已启用', cls: 'blue' },
    { key: 'today', num: 1, label: '今日触发', cls: 'red' }
  ];
}

// 列表按状态过滤；每项带上原始下标 index，供 switch 回写原数组
function makeFiltered(alerts, filter) {
  return alerts
    .filter((a) => {
      if (filter === 'enabled') return a.enabled;
      if (filter === 'paused') return !a.enabled;
      return true;
    })
    .map((a) => Object.assign({}, a, { index: alerts.indexOf(a) }));
}

Page({
  data: {
    bottomGap: 20,
    alerts: ALERTS,
    stats: makeStats(ALERTS),
    filters: FILTERS,
    filter: 'all',
    filtered: makeFiltered(ALERTS, 'all'),
  },


  onLoad() {
    let safeBottom = 0;
    try {
      const win = wx.getWindowInfo ? wx.getWindowInfo() : wx.getSystemInfoSync();
      safeBottom =
        (win.safeAreaInsets && win.safeAreaInsets.bottom) ||
        (win.screenHeight && win.safeArea ? win.screenHeight - win.safeArea.bottom : 0) ||
        0;
    } catch (e) {
      // 保持默认值
    }
    // 底部按钮距屏幕底部的距离
    const bottomGap = 20 + safeBottom;
    this.setData({ bottomGap });
  },

  /* ================= 筛选（TDesign tabs 受控模式） ================= */
  // tabs 的 change 事件：detail = { value, label }
  onTabsChange(e) {
    const key = e.detail && e.detail.value;
    if (!key || key === this.data.filter) return;
    this.setData({ filter: key, filtered: makeFiltered(this.data.alerts, key) });
  },

  /* ================= 启用开关 ================= */
  // TDesign switch 的 change 事件：e.detail.value 为切换后的布尔值
  toggleSwitch(e) {
    const index = Number(e.currentTarget.dataset.index);
    if (isNaN(index) || !this.data.alerts[index]) return;

    const detail = e && e.detail;
    const next =
      detail && typeof detail.value === 'boolean'
        ? detail.value
        : !this.data.alerts[index].enabled;

    const key = 'alerts[' + index + '].enabled';
    const alerts = this.data.alerts.map((a, i) => (i === index ? Object.assign({}, a, { enabled: next }) : a));
    this.setData({
      [key]: next,
      stats: makeStats(alerts),
      filtered: makeFiltered(alerts, this.data.filter)
    });
  },

  /* ================= 其他 ================= */
  onAdd() {
    Toast({ context: this, message: '新建提醒（演示）' });
  },

  goBack() {
    // 立即出栈，不做任何自绘前置动画：
    // 任何「先动画后出栈」的写法都会把等待时间直接变成点击响应延迟
    wx.navigateBack({ fail: () => wx.switchTab && wx.switchTab({ url: '/pages/chat/chat' }) });
  }
});
