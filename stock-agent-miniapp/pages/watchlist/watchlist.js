import Toast from '../../miniprogram_npm/tdesign-miniprogram/toast/index';
import * as api from '../../services/watchlist';
import * as stockApi from '../../services/stock';

const FILTERS = [
  { key: 'all', label: '全部' },
  { key: 'up', label: '涨幅榜' },
  { key: 'down', label: '跌幅榜' }
];

// 只输入代码即可：交易所由代码前缀反推，不需要再单独选择所属市场
// label 只用作弹窗里的提示文案，prefix 参与展示代码拼接
const EXCHANGES = [
  { label: '沪市', prefix: 'SH', codes: ['600', '601', '603', '605', '688', '689'] },
  { label: '深市', prefix: 'SZ', codes: ['000', '001', '002', '003', '300', '301'] },
  { label: '北交所', prefix: 'BJ', codes: ['43', '83', '87', '88', '920'] }
];

// 下拉最多展示几条联想结果：5 条刚好占满手机一屏，再多就要滚动了
const SEARCH_LIMIT = 5;

function marketOf(code) {
  const hit = EXCHANGES.find((e) => e.codes.some((p) => code.indexOf(p) === 0));
  return hit ? hit.prefix : '';
}

// 后端返回的列表项 → 页面渲染字段。
// 价格与涨跌幅直接用后端格式化好的 *_text（千分位 / 正负号），
// 前端不再自行拼装，避免两套格式对不上
function toViewItem(item) {
  return {
    id: item.id,
    stockCode: item.stock_code,
    name: item.name,
    code: item.code,
    price: item.price_text,
    pct: item.pct_text,
    // 排序专用数值：pct 是带正负号和 % 的展示串，不能直接比大小
    pctValue: Number(item.change_percent) || 0,
    up: !!item.up,
    cost: item.cost_price,
    qty: item.quantity,
    profit: item.profit_amount,
    profitRate: item.profit_rate,
    pending: !!item.pending,
    stale: !!item.stale
  };
}

// 盈亏为正显示红色、为负显示绿色（A 股习惯：红涨绿跌）
function profitClass(totalProfit) {
  if (totalProfit && String(totalProfit).indexOf('-') === 0) return 'is-down';
  return 'is-up';
}

function nowHM() {
  const d = new Date();
  const pad = (n) => (n < 10 ? `0${n}` : `${n}`);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// TDesign swipe-cell 右侧操作项
const SWIPE_ACTIONS = [{ text: '删除', className: 'delete-action' }];

Page({
  data: {
    bottomGap: 20,
    filter: 'all',
    filters: FILTERS,
    stocks: [],
    filtered: [],
    upCount: 0,
    downCount: 0,
    // 总浮动盈亏：后端算好并格式化，前端只负责渲染
    totalProfitText: '0.00',
    totalRateText: '0.00%',
    profitClass: 'is-up',
    syncedAt: '--:--',
    loading: false,
    refreshing: false,
    errorMsg: '',
    swipeActions: SWIPE_ACTIONS,
    // 列表是否可滚动：默认关，测量后只有内容真超出容器才打开
    scrollable: false,

    // 删除确认弹窗（TDesign dialog）
    confirmVisible: false,
    pendingIndex: -1,
    pendingName: '',

    // 添加自选表单：股票代码（支持模糊搜索）+ 成本价（元）+ 持股数量（股）
    addVisible: false,
    addCode: '',
    addCost: '',
    addQty: '',
    codeError: '',
    // 下拉联想：searchResults 是后端给的候选，searchVisible 控制显隐，
    // addMarket 记下选中项的交易所——点确定时直接用它，
    // 不必再用代码前缀反推市场（后端返回的才是权威值）
    searchResults: [],
    searchVisible: false,
    addMarket: '',
    // 弹窗里的交易所提示：一行一个交易所，文案直接来自 EXCHANGES
    exchanges: EXCHANGES.map((e) => ({ label: e.label, codes: e.codes.join(' / ') })),
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

  // 进入页面时加载一次。只在首次进入时拉数据，
  // 从子页面返回时不再重复请求，列表滚动位置也不会被冲掉
  onShow() {
    if (!this._loaded) {
      this._loaded = true;
      this.loadList();
    }
  },

  /* ================= 筛选（TDesign tabs 受控模式） ================= */
  // tabs 的 change 事件：detail = { value, label }
  onTabsChange(e) {
    const key = e.detail && e.detail.value;
    if (!key || key === this.data.filter) return;
    this.setData({ filter: key, filtered: this.filterList(key) }, () => this.updateScrollable());
  },

  // 筛选 + 排序。
  // 列表接口固定按 filter=all 拉全量（这样切 tab 不用重复请求），
  // 所以涨跌榜的「按幅度大小排」统一在这里做，
  // 刷新、切 tab、增删之后的顺序都由这一处保证
  filterList(key, stocks) {
    const list = (stocks || this.data.stocks).filter((s) => {
      // 尚无行情的自选只在「全部」出现，不参与涨跌榜
      if (s.pending) return key === 'all';
      if (key === 'up') return s.up;
      if (key === 'down') return !s.up;
      return true;
    });

    // 涨幅榜：涨得越多越靠前 → 降序
    // 跌幅榜：跌得越多越靠前 → 涨跌幅数值越小越靠前 → 升序
    // （e.g. -5.23% 排在 -1.02% 前面）
    if (key === 'up' || key === 'down') {
      const asc = key === 'down';
      return list.sort((a, b) => (asc ? a.pctValue - b.pctValue : b.pctValue - a.pctValue));
    }
    return list;
  },

  /* ================= 滚动控制 ================= */
  // 只有内容真的超出容器（会被底部按钮遮挡）时才允许滚动。
  //
  // 不能只靠 CSS：scroll-view 本身就是原生滚动容器，只要内容比容器高就能拖；
  // iOS 上还有回弹，内容不足时手指一划也会有位移。
  // 所以这里量出真实内容高度，再决定 scroll-y 是开是关。
  //
  // 必须在渲染完成后测：调用方要放在 setData 的回调里，
  // 否则量到的是上一帧的尺寸
  updateScrollable() {
    wx.createSelectorQuery()
      .in(this)
      .select('.stock-scroll')
      .boundingClientRect()
      .select('.stock-inner')
      .boundingClientRect()
      .exec((res) => {
        const box = res && res[0];
        const inner = res && res[1];
        if (!box) return;

        // 空态时 .stock-inner 里只有 t-empty，高度很小，自然不会开滚动
        const contentH = inner ? inner.height : 0;
        // 留 1px 容差：避免浮点误差让两者刚好相等时来回抖动
        const scrollable = contentH > box.height + 1;
        if (scrollable !== this.data.scrollable) this.setData({ scrollable });
      });
  },

  /* ================= 左滑删除 ================= */
  // swipe-cell 右侧按钮点击：detail 含 action 信息，行号从 data-index 取
  onDelete(e) {
    const index = Number(e.currentTarget.dataset.index);
    const item = this.data.filtered[index];
    if (!item) return;
    this.setData({
      confirmVisible: true,
      pendingIndex: index,
      pendingName: item.name
    });
  },

  cancelDelete() {
    this.setData({ confirmVisible: false, pendingIndex: -1, pendingName: '' });
  },

  confirmDelete() {
    const index = this.data.pendingIndex;
    if (index < 0) return;

    const item = this.data.filtered[index];
    if (!item) return;

    this.setData({ confirmVisible: false, pendingIndex: -1, pendingName: '', loading: true });
    api
      .remove(item.stockCode)
      .then(() => this.loadList())
      .then(() => Toast({ context: this, message: '已移出自选', theme: 'success' }))
      .catch((err) => {
        this.setData({ loading: false, errorMsg: (err && err.message) || '删除失败' });
      });
  },

  /* ================= 数据同步 ================= */
  // 手动刷新：后端会并发拉行情 → 写缓存 → 检查提醒并推送，
  // 返回的全量列表（filter=all）直接替换本地数据
  onRefresh() {
    if (this.data.refreshing) return;
    this.setData({ refreshing: true, errorMsg: '' });

    api
      .refresh()
      .then((data) => {
        this.setData({ refreshing: false });
        this.applyData(data);
        Toast({ context: this, message: '行情已刷新', theme: 'success' });
      })
      .catch((err) => {
        this.setData({ refreshing: false, errorMsg: (err && err.message) || '刷新失败' });
      });
  },

  // 统一入口：列表与刷新接口返回结构一致，都走这里更新列表与统计
  applyData(data) {
    const d = data || {};
    const stocks = (d.list || []).map(toViewItem);

    this.setData({
      stocks,
      filtered: this.filterList(this.data.filter, stocks),
      upCount: d.up_count || 0,
      downCount: d.down_count || 0,
      totalProfitText: d.total_profit || '0.00',
      totalRateText: `${d.total_profit_rate || '0.00'}%`,
      profitClass: profitClass(d.total_profit),
      syncedAt: nowHM()
    }, () => this.updateScrollable());
  },

  loadList() {
    this.setData({ loading: true, errorMsg: '' });
    return api
      .fetchList()
      .then((data) => {
        this.setData({ loading: false });
        this.applyData(data);
      })
      .catch((err) => {
        this.setData({ loading: false, errorMsg: (err && err.message) || '加载失败' });
      });
  },

  /* ================= 添加自选（dialog + input） ================= */
  onAdd() {
    this.setData({
      addVisible: true,
      addCode: '',
      addCost: '',
      addQty: '',
      codeError: '',
      searchResults: [],
      searchVisible: false,
      addMarket: ''
    });
  },

  onAddCancel() {
    this.setData({ addVisible: false, searchResults: [], searchVisible: false });
  },

  onAddCode(e) {
    const value = (e.detail && e.detail.value) || '';
    // 输入时清掉旧的报错，避免边改边红
    this.setData({ addCode: value, codeError: '' });
    this.scheduleSearch(value);
  },

  // 防抖 300ms：打字过程中不发请求，停手后再发。
  // 不防抖的话每敲一个字符就打一次后端，既慢又容易被第三方限流
  scheduleSearch(value) {
    if (this._searchTimer) clearTimeout(this._searchTimer);

    const kw = String(value || '').trim();
    if (!kw) {
      this.setData({ searchResults: [], searchVisible: false });
      return;
    }

    // 切走页面时清掉待发请求，避免在已销毁的页面上 setData
    this._searchTimer = setTimeout(() => this.doSearch(kw), 300);
  },

  // 拉联想候选。失败就静默收起下拉：搜索是体验加分项，
  // 挂了也不影响手输代码添加自选
  doSearch(kw) {
    stockApi
      .search(kw, SEARCH_LIMIT)
      .then((list) => {
        const results = list || [];
        this.setData({ searchResults: results, searchVisible: results.length > 0 });
      })
      .catch(() => {
        this.setData({ searchResults: [], searchVisible: false });
      });
  },

  // 选中下拉项：填回代码，并记住它的交易所。
  // 之后就算用户再手动改动代码，也会走 onAddConfirm 里的兜底反推
  onPickSearch(e) {
    const { code, market } = e.currentTarget.dataset;
    this.setData({
      addCode: code || '',
      addMarket: market || '',
      searchResults: [],
      searchVisible: false,
      codeError: ''
    });
  },

  onAddCost(e) {
    const value = (e.detail && e.detail.value) || '';
    this.setData({ addCost: value });
  },

  onAddQty(e) {
    const value = (e.detail && e.detail.value) || '';
    this.setData({ addQty: value });
  },

  onAddConfirm() {
    const code = String(this.data.addCode || '').trim();

    let codeError = '';
    if (!code) codeError = '请输入股票代码';
    else if (!/^\d{6}$/.test(code)) codeError = '股票代码为 6 位数字';

    // 优先用下拉选中时记下的交易所（后端给的是权威值）；
    // 手输没选过的，再按代码前缀反推
    const market = codeError ? '' : this.data.addMarket || marketOf(code);
    if (!codeError && !market) codeError = '暂不支持该交易所，请核对代码';
    if (!codeError && this.data.stocks.some((s) => s.code === `${market} ${code}`)) {
      codeError = '该股票已在自选中';
    }

    if (codeError) {
      this.setData({ codeError });
      return;
    }

    // 只把填了的字段发过去：没填的成本/数量让后端保持原值，
    // 这样重复添加同一只股票时才不会把之前的建仓信息冲掉
    const payload = { stock_code: `${market}${code}` };
    const cost = String(this.data.addCost || '').trim();
    const qty = String(this.data.addQty || '').trim();
    if (cost) payload.cost_price = cost;
    if (qty) payload.quantity = Number(qty) || 0;

    this.setData({ addVisible: false, loading: true });
    api
      .add(payload)
      .then(() => this.loadList())
      .then(() => Toast({ context: this, message: `已添加 ${market} ${code}`, theme: 'success' }))
      .catch((err) => {
        this.setData({ loading: false, errorMsg: (err && err.message) || '添加失败' });
      });
  },

  goBack() {
    // 立即出栈，不做任何自绘前置动画
    wx.navigateBack({ fail: () => wx.switchTab && wx.switchTab({ url: '/pages/chat/chat' }) });
  }
});
