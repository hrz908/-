// K 线详情页（分包 pages/kline）：uCharts 蜡烛图 + MA + 成交量。
// 选型依据改造指南第 2 节：完整 K 线交互图用 uCharts 并放分包（约 170KB，
// 不进主包）；对话流里的 K 线概览卡是 SVG 手绘，与此页无关。
// 数据：GET /api/v1/stock/kline（前复权，period 仅 day/week/month）。
// 入口：watchlist 股票行点击、chat 页行情卡点击，均带 code / name 参数。
import { kline } from '../../services/stock';
import uCharts from '@qiun/ucharts';
import { fmtPrice, fmtPct, direction } from '../../utils/format';

// 周期 chips：与后端 period 白名单一致；分时/分钟线没有数据源，不画饼
const PERIODS = [
  { key: 'day', label: '日K' },
  { key: 'week', label: '周K' },
  { key: 'month', label: '月K' }
];
const PAGE_SIZE = 60; // 一次拉 60 根：一屏 40 根，多出来的供左右拖动回看
const ITEM_COUNT = 40; // 一屏最多 40 根，超出靠 enableScroll 拖动

// 深色主题图表色：canvas 不吃 CSS 变量，这里与 styles/token.wxss 保持同值
const C = {
  bg: '#141E29',
  grid: '#26333F',
  axis: '#6B7A8A',
  up: '#F2555A',
  down: '#1FB87A',
  vol: '#4E5B68',
  ma5: '#E9B949',
  ma10: '#5B9BEF',
  ma20: '#9AA9B8'
};

function num(v) {
  const n = parseFloat(v);
  return isNaN(n) ? null : n;
}

Page({
  data: {
    code: '',
    name: '',
    periods: PERIODS,
    activePeriod: 'day',
    loading: true,
    errorMsg: '',
    empty: false,
    stats: { pctText: '--', pctDir: 'flat', ampText: '--', highText: '--', lowText: '--' }
  },

  onLoad(query) {
    const code = (query && query.code) || '';
    const name = (query && query.name) || '';
    this.setData({ code: code, name: name });
    wx.setNavigationBarTitle({ title: (name || code || 'K线') + ' K线' });
  },

  onReady() {
    this.loadKline();
  },

  onUnload() {
    this._charts = null;
  },

  // 周期切换：受控 chips，点了就换数据
  onPeriodTap(e) {
    const index = Number(e.currentTarget.dataset.index);
    const item = this.data.periods[index];
    if (!item || item.key === this.data.activePeriod) return;
    this.setData({ activePeriod: item.key });
    this.loadKline();
  },

  loadKline() {
    if (!this.data.code) {
      this.setData({ loading: false, errorMsg: '缺少股票代码' });
      return;
    }
    this.setData({ loading: true, errorMsg: '', empty: false });
    kline(this.data.code, this.data.activePeriod, PAGE_SIZE)
      .then((data) => {
        const rows = (data && data.rows) || [];
        if (!rows.length) {
          this.setData({ loading: false, empty: true });
          return;
        }
        this.setData({ loading: false });
        this.renderChart(rows);
        this.setData({ stats: this.calcStats(rows) });
      })
      .catch(() => {
        this.setData({ loading: false, errorMsg: 'K线获取失败，请稍后重试' });
      });
  },

  // 区间统计：全部由当前区间的真实数据算出，不做任何预测性表述
  calcStats(rows) {
    const closes = rows.map((r) => num(r.close)).filter((v) => v !== null);
    const highs = rows.map((r) => num(r.high)).filter((v) => v !== null);
    const lows = rows.map((r) => num(r.low)).filter((v) => v !== null);
    if (closes.length < 2) return this.data.stats;
    const first = closes[0];
    const last = closes[closes.length - 1];
    const pct = ((last - first) / first) * 100;
    const hi = highs.length ? Math.max.apply(null, highs) : null;
    const lo = lows.length ? Math.min.apply(null, lows) : null;
    return {
      pctText: fmtPct(pct),
      pctDir: direction(pct),
      ampText: lo ? fmtPct(((hi - lo) / first) * 100) : '--',
      highText: fmtPrice(hi),
      lowText: fmtPrice(lo)
    };
  },

  renderChart(rows) {
    const query = wx.createSelectorQuery().in(this);
    query.select('#kline-canvas').fields({ node: true, size: true }).exec((res) => {
      const node = res && res[0] && res[0].node;
      if (!node) return;
      const ctx = node.getContext('2d');
      const info = wx.getWindowInfo ? wx.getWindowInfo() : wx.getSystemInfoSync();
      const dpr = info.pixelRatio || 1;
      const width = res[0].width;
      const height = res[0].height;
      // 高分屏：画布按物理像素开（重设 width 会顺带清空画布），
      // 上下文再缩放回来，图表布局全程用 CSS 像素
      node.width = width * dpr;
      node.height = height * dpr;
      ctx.scale(dpr, dpr);

      // 后端 rows 按日期升序（旧 -> 新），直接对应图表左 -> 右
      const categories = rows.map((r) => String(r.date || '').slice(5)); // MM-DD
      // uCharts 蜡烛数据格式：[开, 收, 低, 高]（库内 calCandleMA 取 [1] 收盘）
      const kdata = rows.map((r) => [num(r.open), num(r.close), num(r.low), num(r.high)]);
      const volumes = rows.map((r) => num(r.volume));

      this._charts = new uCharts({
        type: 'candle',
        context: ctx,
        width: width,
        height: height,
        pixelRatio: dpr,
        background: C.bg,
        fontSize: 11,
        fontColor: C.axis,
        animation: false,
        enableScroll: true,
        dataLabel: false,
        legend: { show: true, position: 'top', float: 'left', fontSize: 10, lineHeight: 14, fontColor: C.axis, padding: 5, margin: 0, itemGap: 12 },
        categories: categories,
        series: [
          { name: 'K线', data: kdata },
          // 成交量柱绑第二个 Y 轴（index: 1），只借它的 range 定高度，不显示刻度
          { name: '成交量', type: 'bar', data: volumes, index: 1, color: C.vol }
        ],
        xAxis: {
          disableGrid: true,
          labelCount: 4,
          itemCount: ITEM_COUNT,
          scrollShow: false,
          axisLineColor: C.grid,
          fontColor: C.axis
        },
        yAxis: [
          {
            disableGrid: false,
            gridType: 'dash',
            gridColor: C.grid,
            splitNumber: 4,
            fontColor: C.axis
          },
          { disabled: true, disableGrid: true }
        ],
        extra: {
          candle: {
            // 国内习惯：涨红跌绿
            color: { upLine: C.up, upFill: C.up, downLine: C.down, downFill: C.down },
            // 均线由库内按收盘价计算（calCandleMA），这里只配周期与配色
            average: {
              show: true,
              name: ['MA5', 'MA10', 'MA20'],
              day: [5, 10, 20],
              color: [C.ma5, C.ma10, C.ma20]
            }
          }
        }
      });
    });
  },

  // 触摸：scroll 系列负责左右拖动，showToolTip 出当日 OHLC + 均线浮层
  onTouchStart(e) {
    if (!this._charts) return;
    this._charts.scrollStart(e);
    this._charts.showToolTip(e);
  },

  onTouchMove(e) {
    if (!this._charts) return;
    this._charts.scroll(e);
    this._charts.showToolTip(e);
  },

  onTouchEnd(e) {
    if (!this._charts) return;
    this._charts.scrollEnd(e);
  }
});
