import Toast from '../../miniprogram_npm/tdesign-miniprogram/toast/index';
import * as stockApi from '../../services/stock';
import * as alertApi from '../../services/alert';

// 提醒列表数据来自后端 GET /api/v1/alerts，字段与「新增提醒」弹窗一一对应
// （股票 / 触发条件 / 阈值 / 基准周期 / 检查频率）；
// 卡片上的大号阈值、描述、状态标签等文案由 decorate 在拉取后算好，WXML 直接取

// 下拉最多展示几条联想结果（与自选股页保持一致）
const SEARCH_LIMIT = 5;

// 触发条件选项
const CONDITION_OPTIONS = [
  { label: '价格突破', value: 'priceUp' },
  { label: '价格跌破', value: 'priceDown' },
  { label: '涨幅提醒', value: 'pctUp' },
  { label: '跌幅提醒', value: 'pctDown' },
  { label: '成交量异动', value: 'volume' }
];

// 基准周期选项（成交量提醒时选均量天数）
const VOLUME_BASE_OPTIONS = [
  { label: '5日均量', value: 5 },
  { label: '10日均量', value: 10 },
  { label: '20日均量', value: 20 }
];

// 检查频率选项
const FREQUENCY_OPTIONS = [
  { label: '盘中即时', value: 'realtime' },
  { label: '日线收盘确认', value: 'close' }
];

// 条件类型 -> 阈值输入的配置（单位、占位提示、校验规则）
// 单位单独显示在输入框尾部（清除图标右边），不写进 placeholder
const THRESHOLD_CONFIG = {
  priceUp:   { unit: '元', placeholder: '如 1700.00', min: 0, max: Infinity },
  priceDown: { unit: '元', placeholder: '如 235.00', min: 0, max: Infinity },
  pctUp:     { unit: '%', placeholder: '如 5.0', min: 0, max: 100 },
  pctDown:   { unit: '%', placeholder: '如 5.0', min: 0, max: 100 },
  volume:    { unit: '倍', placeholder: '如 1.5', min: 0.1, max: 100 }
};

// 列表文案：把弹窗里的字段拼成一句，与「新增提醒」表单一一对应
// （触发条件 + 阈值 [+ 基准周期] · 检查频率）
function describeAlert(a) {
  const condOpt = CONDITION_OPTIONS.filter(o => o.value === a.conditionType)[0];
  const freqOpt = FREQUENCY_OPTIONS.filter(o => o.value === a.checkFrequency)[0];

  let condDesc;
  if (a.conditionType === 'volume') {
    condDesc = `成交量放大到 ${a.volumeBase || 20}日均量的 ${a.threshold} 倍`;
  } else {
    const unit = THRESHOLD_CONFIG[a.conditionType] ? THRESHOLD_CONFIG[a.conditionType].unit : '';
    condDesc = `${condOpt ? condOpt.label : ''} ${a.threshold}${unit}`;
  }
  return `${condDesc} · ${freqOpt ? freqOpt.label : ''}`;
}

// 某条触发时间是否落在今天
function isToday(iso) {
  if (!iso) return false;
  const d = parseDate(iso);
  if (!d) return false;
  const today = new Date();
  return (
    d.getFullYear() === today.getFullYear() &&
    d.getMonth() === today.getMonth() &&
    d.getDate() === today.getDate()
  );
}

// 解析后端 RFC3339 时间；解析不出来返回 null，交给调用方兜底
function parseDate(iso) {
  if (!iso) return null;
  const d = new Date(iso);
  return isNaN(d.getTime()) ? null : d;
}

// 概览宫格（TDesign grid）数据：只保留「全部提醒」和「今日触发」两格，
// 今日触发按 last_triggered_at 是否今天来数
function makeStats(alerts) {
  const todayCount = alerts.filter((a) => isToday(a.lastTriggeredAt)).length;
  return [
    { key: 'total', num: alerts.length, label: '全部提醒', cls: 'ink' },
    { key: 'today', num: todayCount, label: '今日触发', cls: 'ink' }
  ];
}

// 大号阈值文案 + 趋势图标：按条件类型决定展示形式
//   价格类：¥1,700.00（涨跌用上下箭头）
//   涨跌幅：+5.00% / -5.00%
//   成交量：1.5 倍
function priceTextOf(a) {
  if (a.conditionType === 'priceUp' || a.conditionType === 'priceDown') {
    return `¥${a.threshold}`;
  }
  if (a.conditionType === 'pctUp') return `${a.threshold}%`;
  if (a.conditionType === 'pctDown') return `${a.threshold}%`;
  return `${a.threshold} 倍`;
}

function trendIconOf(a) {
  if (a.conditionType === 'priceUp' || a.conditionType === 'pctUp') return 'arrow-up';
  if (a.conditionType === 'priceDown' || a.conditionType === 'pctDown') return 'arrow-down';
  return 'chart-bar';
}

// 趋势颜色跟随涨跌：突破 / 涨幅用红，跌破 / 跌幅用绿，成交量保持红
function trendColorOf(a) {
  if (a.conditionType === 'priceDown' || a.conditionType === 'pctDown') return 'var(--c-green)';
  return 'var(--c-red)';
}

// 状态标签配色：已触发时跟随涨跌（跌 = 绿、涨 = 红），未触发用中性灰
function triggerThemeOf(a) {
  if (!a.triggered) return 'default';
  if (a.conditionType === 'priceDown' || a.conditionType === 'pctDown') return 'success';
  return 'danger';
}

// 触发时间文案：今天 ->「今日 HH:mm 触发」，昨天 ->「昨日 HH:mm 触发」，
// 一个月内 ->「N 天前触发」，更早 ->「M月D日 HH:mm 触发」；没触发过 ->「未触发」
function timeTextOf(a) {
  const d = parseDate(a.lastTriggeredAt);
  if (!d) return '未触发';
  const pad = (n) => (n < 10 ? `0${n}` : `${n}`);
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  // 只按日期算天数差：「昨天 23:50」和「今天 00:10」隔了一天，不能按时间戳整除
  const startOfDay = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.floor((startOfDay(new Date()) - startOfDay(d)) / 86400000);
  if (days === 0) return `今日 ${hm} 触发`;
  if (days === 1) return `昨日 ${hm} 触发`;
  if (days < 30) return `${days} 天前触发`;
  return `${d.getMonth() + 1}月${d.getDate()}日 ${hm} 触发`;
}

// 接口返回的提醒 -> 卡片渲染结构，各段文案在这里算好，WXML 直接取
function decorate(a) {
  const item = Object.assign({}, a, { triggered: !!a.lastTriggeredAt });
  return Object.assign(item, {
    desc: describeAlert(item),
    priceText: priceTextOf(item),
    trendIcon: trendIconOf(item),
    trendColor: trendColorOf(item),
    triggerText: item.triggered ? '已触发' : '未触发',
    triggerTheme: triggerThemeOf(item),
    time: timeTextOf(item)
  });
}

Page({
  data: {
    bottomGap: 20,
    alerts: [],          // 由 GET /api/v1/alerts 填充
    stats: makeStats([]),

    // ===== 新增 / 编辑提醒弹窗状态 =====
    // 同一套表单两种用法：editMode=false 新增（底部「确认添加」），
    // editMode=true 编辑（底部「删除」，字段改动即时保存）
    popupVisible: false,
    editMode: false,
    editingId: 0,
    deleteBtn: { content: '删除', theme: 'danger' },
    searchKeyword: '',
    searchResults: [],
    selectedStock: null,        // { name, code }
    conditionType: 'priceUp',   // priceUp | priceDown | pctUp | pctDown | volume
    conditionOptions: CONDITION_OPTIONS,
    conditionText: CONDITION_OPTIONS[0].label, // 选择器右侧显示的当前项
    conditionPickerValue: [CONDITION_OPTIONS[0].value], // picker 的 value 是数组
    conditionVisible: false,    // 触发条件选择器是否弹出
    // 选择器弹出时不再叠一层遮罩：dialog 已经有一层，叠加后背景会明显变黑。
    // overlay 节点保留（只是透明），点空白仍可关闭；
    // 走 overlayProps.backgroundColor 会写成行内样式，比 CSS 变量更可靠
    pickerPopupProps: { overlayProps: { backgroundColor: 'transparent' } },
    // 清除图标（x）小一档：TDesign 默认约 48rpx，这里显式给 32rpx
    clearIconCfg: { name: 'close-circle-filled', size: 24 },
    threshold: '',              // 阈值（字符串，提交前转 number）
    thresholdPlaceholder: THRESHOLD_CONFIG.priceUp.placeholder,
    thresholdUnit: THRESHOLD_CONFIG.priceUp.unit,
    volumeBase: 20,             // 成交量基准（5 / 10 / 20 日均量）
    volumeBaseOptions: VOLUME_BASE_OPTIONS,
    volumeBaseText: '20日均量',  // 选择器右侧显示的当前项
    volumeBasePickerValue: [20],
    volumeBaseVisible: false,   // 基准周期选择器是否弹出
    checkFrequency: 'realtime', // realtime(盘中即时) | close(日线收盘确认)
    frequencyOptions: FREQUENCY_OPTIONS,
    frequencyText: FREQUENCY_OPTIONS[0].label,
    frequencyPickerValue: [FREQUENCY_OPTIONS[0].value],
    frequencyVisible: false    // 检查频率选择器是否弹出
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

  // 进入页面时加载一次。与自选股页同一套节奏：
  // 只在首次进入时拉数据，从子页面返回时不重复请求
  onShow() {
    if (!this._loaded) {
      this._loaded = true;
      this.loadList();
    }
  },

  // 拉取提醒列表。失败不弹 Toast：页面保持空态，错误留控制台
  loadList() {
    return alertApi
      .fetchList()
      .then((list) => {
        const alerts = (list || []).map(decorate);
        this.setData({ alerts, stats: makeStats(alerts) });
      })
      .catch((err) => {
        console.error('[提醒列表] 加载失败：', err && err.message);
      });
  },

  /* ================= 新增 / 编辑提醒弹窗 ================= */

  // 打开「新增」弹窗（表单不再滚动，高度由 dialog 自己撑）
  onAdd() {
    this.setData({ popupVisible: true, editMode: false, editingId: 0 });
  },

  // 打开「编辑」弹窗：把这条规则回填到表单里，其余与新增完全一致
  onEdit(e) {
    const id = Number((e && e.currentTarget && e.currentTarget.dataset && e.currentTarget.dataset.id) || 0);
    const target = this.data.alerts.filter((a) => a.id === id)[0];
    if (!target) return;

    // 列表里的 code 是带交易所前缀的全码（SH600519），拆成市场 + 6 位代码，
    // 与搜索结果的字段结构保持一致，弹窗里的选中态才能正常显示
    const code = String(target.code || '');
    const market = code.slice(0, 2);
    const digits = code.slice(2);

    const cfg = THRESHOLD_CONFIG[target.conditionType] || THRESHOLD_CONFIG.priceUp;
    const condOpt = CONDITION_OPTIONS.filter((o) => o.value === target.conditionType)[0];
    const baseOpt = VOLUME_BASE_OPTIONS.filter((o) => o.value === (target.volumeBase || 20))[0];
    const freqOpt = FREQUENCY_OPTIONS.filter((o) => o.value === target.checkFrequency)[0];

    this.setData({
      popupVisible: true,
      editMode: true,
      editingId: id,
      searchKeyword: '',
      searchResults: [],
      selectedStock: { name: target.name, code: digits, market: market },
      conditionType: target.conditionType,
      conditionText: condOpt ? condOpt.label : '',
      conditionPickerValue: [target.conditionType],
      threshold: String(target.threshold || ''),
      thresholdPlaceholder: cfg.placeholder,
      thresholdUnit: cfg.unit,
      volumeBase: target.volumeBase || 20,
      volumeBaseText: baseOpt ? baseOpt.label : '',
      volumeBasePickerValue: [target.volumeBase || 20],
      checkFrequency: target.checkFrequency,
      frequencyText: freqOpt ? freqOpt.label : '',
      frequencyPickerValue: [target.checkFrequency]
    });
  },

  // 取消按钮：关闭弹窗不保存
  onPopupCancel() {
    this._closePopup();
  },

  // 关闭弹窗并重置表单（同时退出编辑态）
  _closePopup() {
    this.setData({
      popupVisible: false,
      editMode: false,
      editingId: 0,
      searchKeyword: '',
      searchResults: [],
      selectedStock: null,
      conditionType: 'priceUp',
      conditionText: CONDITION_OPTIONS[0].label,
      conditionPickerValue: [CONDITION_OPTIONS[0].value],
      conditionVisible: false,
      threshold: '',
      thresholdPlaceholder: THRESHOLD_CONFIG.priceUp.placeholder,
      thresholdUnit: THRESHOLD_CONFIG.priceUp.unit,
      volumeBase: 20,
      volumeBaseText: '20日均量',
      volumeBasePickerValue: [20],
      volumeBaseVisible: false,
      checkFrequency: 'realtime',
      frequencyText: FREQUENCY_OPTIONS[0].label,
      frequencyPickerValue: [FREQUENCY_OPTIONS[0].value],
      frequencyVisible: false
    });
  },

  // ----- 选股搜索（复用自选股的搜索接口，带 300ms 防抖） -----
  onSearchInput(e) {
    const keyword = (e && e.detail && e.detail.value) || '';
    this.setData({ searchKeyword: keyword });
    this.scheduleSearch(keyword);
  },

  // 防抖 300ms：与自选股页同一套节奏，打字过程中不发请求
  scheduleSearch(value) {
    if (this._searchTimer) clearTimeout(this._searchTimer);

    const kw = String(value || '').trim();
    if (!kw) {
      this.setData({ searchResults: [] });
      return;
    }

    this._searchTimer = setTimeout(() => this.doSearch(kw), 300);
  },

  // 调用 services/stock.js 的搜索接口（支持代码/名称/拼音模糊匹配）
  doSearch(kw) {
    stockApi
      .search(kw, SEARCH_LIMIT)
      .then((list) => {
        this.setData({ searchResults: list || [] });
      })
      .catch(() => {
        this.setData({ searchResults: [] });
      });
  },

  // 清空搜索
  onSearchClear() {
    this.setData({ searchKeyword: '', searchResults: [] });
  },

  // 选中股票：保存 name、code、market（与自选股页一致）
  onSelectStock(e) {
    const { code, name, market } = e.currentTarget.dataset;
    this.setData({
      selectedStock: { name, code, market },
      searchKeyword: '',
      searchResults: []
    });
  },

  // 清除已选股票。编辑模式下不允许换股（后端 PUT 不支持改股票代码），
  // 点了也不清空，只提示一声，避免表单变成「没选股票」的非法状态
  onClearStock() {
    if (this.data.editMode) {
      Toast({ context: this, message: '编辑提醒时不能更换股票，可删除后重新添加' });
      return;
    }
    this.setData({ selectedStock: null });
  },

  // ----- 触发条件：带标题的选择器（t-cell 触发 + t-picker 选择） -----
  onConditionPickerOpen() {
    this.setData({ conditionVisible: true });
  },

  // 关闭（取消 / 点遮罩 / 确认都会走到这里）：
  // picker 内部会自己 setData visible=false，不同步这里的话下次点 cell 就打不开了
  onConditionPickerClose() {
    this.setData({ conditionVisible: false });
  },

  onConditionPickerChange(e) {
    const detail = (e && e.detail) || {};
    this.setData({ conditionVisible: false });

    // 单列 picker：value 是数组，取第一个
    const val = (detail.value || [])[0];
    if (!val || val === this.data.conditionType) return;

    const cfg = THRESHOLD_CONFIG[val];
    const opt = CONDITION_OPTIONS.filter(o => o.value === val)[0];
    // 切换条件时清空旧阈值，占位提示（含单位）跟着换
    this.setData({
      conditionType: val,
      conditionText: opt ? opt.label : '',
      conditionPickerValue: [val],
      threshold: '',
      thresholdPlaceholder: cfg.placeholder,
      thresholdUnit: cfg.unit
    });
    // 切到成交量时基准周期才有意义；切走时把残留值清掉
    this._syncEdit();
  },

  // ----- 阈值输入 -----
  onThresholdInput(e) {
    const val = (e && e.detail && e.detail.value) || '';
    this.setData({ threshold: val });
  },

  // 阈值失焦时才保存：输入过程中一路 PUT 会打出大量无效请求。
  // 非空但非法（如涨跌幅 120）明确提示，空着则视为还没输完、静默跳过
  onThresholdBlur(e) {
    const val = (e && e.detail && e.detail.value) || '';
    this.setData({ threshold: val });
    if (!this.data.editMode || !this.data.editingId || !val) return;

    const errMsg = this._validateForm();
    if (errMsg) {
      Toast({ context: this, message: errMsg });
      return;
    }
    this._syncEdit();
  },

  // ----- 基准周期：与触发条件同一套「带标题的选择器」 -----
  onVolumeBasePickerOpen() {
    this.setData({ volumeBaseVisible: true });
  },

  onVolumeBasePickerClose() {
    this.setData({ volumeBaseVisible: false });
  },

  onVolumeBasePickerChange(e) {
    const detail = (e && e.detail) || {};
    this.setData({ volumeBaseVisible: false });

    const val = (detail.value || [])[0];
    if (val === undefined || val === null) return;
    const numVal = Number(val);
    if (numVal === this.data.volumeBase) return;

    const opt = VOLUME_BASE_OPTIONS.filter(o => o.value === numVal)[0];
    this.setData({
      volumeBase: numVal,
      volumeBaseText: opt ? opt.label : '',
      volumeBasePickerValue: [numVal]
    });
    this._syncEdit();
  },

  // ----- 检查频率：同样用带标题的选择器 -----
  onFrequencyPickerOpen() {
    this.setData({ frequencyVisible: true });
  },

  onFrequencyPickerClose() {
    this.setData({ frequencyVisible: false });
  },

  onFrequencyPickerChange(e) {
    const detail = (e && e.detail) || {};
    this.setData({ frequencyVisible: false });

    const val = (detail.value || [])[0];
    if (!val || val === this.data.checkFrequency) return;

    const opt = FREQUENCY_OPTIONS.filter(o => o.value === val)[0];
    this.setData({
      checkFrequency: val,
      frequencyText: opt ? opt.label : '',
      frequencyPickerValue: [val]
    });
    this._syncEdit();
  },

  // ----- 表单校验 -----
  _validateForm() {
    const { selectedStock, threshold, conditionType } = this.data;
    if (!selectedStock) {
      return '请先选择一只股票';
    }
    const numVal = parseFloat(threshold);
    if (!threshold || isNaN(numVal)) {
      return '请输入阈值';
    }
    const cfg = THRESHOLD_CONFIG[conditionType];
    if (numVal <= cfg.min) {
      return `阈值必须大于 ${cfg.min}`;
    }
    if (numVal > cfg.max) {
      return `阈值不能超过 ${cfg.max}`;
    }
    // 价格类保留两位小数校验
    if ((conditionType === 'priceUp' || conditionType === 'priceDown') && !/^\d+(\.\d{1,2})?$/.test(threshold)) {
      return '价格最多保留两位小数';
    }
    return null;
  },

  // ----- 确认按钮 -----
  // 新增模式：确认添加；编辑模式：确认删除
  // （编辑时改字段是即时保存的，所以右侧按钮留给删除这个不可逆操作）
  onPopupConfirm() {
    if (this.data.editMode) {
      this._deleteAlert();
      return;
    }

    // 校验
    const errMsg = this._validateForm();
    if (errMsg) {
      Toast({ context: this, message: errMsg });
      return;
    }

    const payload = this._payload();

    // 连点保护：请求回来之前不再发第二次
    if (this._submitting) return;
    this._submitting = true;

    alertApi
      .create(payload)
      .then(() => {
        Toast({ context: this, message: '提醒添加成功' });
        this._closePopup();
        this.loadList();
      })
      .catch((err) => {
        // 409「该股票的该条件提醒已存在」这类文案后端已写好，直接用它的
        Toast({ context: this, message: (err && err.message) || '添加提醒失败' });
      })
      .then(() => {
        this._submitting = false;
      });
  },

  // 提交载荷：与后端 /api/v1/alerts 的字段一一对应。
  // 股票代码跟添加自选一样带交易所前缀（搜索结果里已拼好）
  _payload() {
    const { selectedStock, conditionType, threshold, volumeBase, checkFrequency } = this.data;
    return {
      stockCode: (selectedStock.market || '') + selectedStock.code,
      stockName: selectedStock.name,
      conditionType: conditionType,
      threshold: parseFloat(threshold),
      // 只有成交量异动需要基准周期，其余传 0（后端会强制归 0）
      volumeBase: conditionType === 'volume' ? volumeBase : 0,
      checkFrequency: checkFrequency
    };
  },

  // 编辑态即时保存：字段一改就 PUT 整条表单，成功刷新列表，失败提示原因。
  // 校验还没过（比如阈值刚清空正在重输）就静默跳过，等下次变更再提交，
  // 避免边打字边弹错误 Toast
  _syncEdit() {
    if (!this.data.editMode || !this.data.editingId || this._syncing) return;
    if (this._validateForm()) return;

    this._syncing = true;
    alertApi
      .update(this.data.editingId, this._payload())
      .then(() => this.loadList())
      .catch((err) => {
        Toast({ context: this, message: (err && err.message) || '保存修改失败' });
      })
      .then(() => {
        this._syncing = false;
      });
  },

  // 编辑态删除：先删再关弹窗，失败保留弹窗让用户可以重试
  _deleteAlert() {
    if (!this.data.editingId || this._submitting) return;
    this._submitting = true;

    alertApi
      .remove(this.data.editingId)
      .then(() => {
        Toast({ context: this, message: '提醒已删除' });
        this._closePopup();
        this.loadList();
      })
      .catch((err) => {
        Toast({ context: this, message: (err && err.message) || '删除提醒失败' });
      })
      .then(() => {
        this._submitting = false;
      });
  },

  goBack() {
    // 立即出栈，不做任何自绘前置动画：
    // 任何「先动画后出栈」的写法都会把等待时间直接变成点击响应延迟
    wx.navigateBack({ fail: () => wx.switchTab && wx.switchTab({ url: '/pages/chat/chat' }) });
  }
});
