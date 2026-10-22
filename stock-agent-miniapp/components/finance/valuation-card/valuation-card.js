// 估值卡：回答「市盈率多少，贵不贵」「分红多少」
// 字段口径见设计文档第 2 节④。四列估值指标 + PE 分位进度条。
// 分位高低判定（<30% 偏低 / >70% 偏高）是设计文档规定的展示规则，
// 属于呈现层判断，不是投资建议——文案只陈述客观事实（合规红线）。
const { fmtNum, fmtPct, fmtAmount } = require('../../../utils/format');

Component({
  externalClasses: ['card-class'],
  properties: {
    peTTM: { type: Number, value: null },
    peStatic: { type: Number, value: null },
    pb: { type: Number, value: null },
    ps: { type: Number, value: null },
    peg: { type: Number, value: null },
    // 股息率（% 数值，如 3.42）
    dividendYield: { type: Number, value: null },
    // 历史分位数（0-100）
    percentile3y: { type: Number, value: null },
    percentile5y: { type: Number, value: null },
    // 行业均值（横向对比用）
    industryAvgPe: { type: Number, value: null },
    industryAvgPb: { type: Number, value: null },
    industryAvgYield: { type: Number, value: null },
    totalCap: { type: Number, value: null },
    note: { type: String, value: '' }
  },
  data: {
    peTTMText: '--',
    peStaticText: '--',
    pbText: '--',
    psText: '--',
    pegText: '--',
    yieldText: '--',
    capText: '--',
    pct3yText: '--',
    pct5yText: '--',
    industryPeText: '--',
    industryPbText: '--',
    industryYieldText: '--',
    // 分位进度条：宽度百分比 + 配色档位 + 文案
    barWidth: '0%',
    barLevel: 'flat',
    barLabel: ''
  },
  lifetimes: {
    // observer 不会在 attached 时对初始值触发：首帧手动算一次
    attached() {
      this.refresh();
    },

    // ready 再兜一次：wx:if 首帧创建时 properties 可能尚未就绪，
    // attached 读到的是初始值（observer 对首值也不触发），ready 时必然已就绪
    ready() {
      this.refresh();
    }
  },
  observers: {
    'peTTM, peStatic, pb, ps, peg, dividendYield, totalCap, percentile3y, percentile5y, industryAvgPe, industryAvgPb, industryAvgYield'() {
      this.refresh();
    }
  },
  methods: {
    refresh() {
      const d = this.data;
      this.setData({
        peTTMText: fmtNum(d.peTTM),
        peStaticText: fmtNum(d.peStatic),
        pbText: fmtNum(d.pb),
        psText: fmtNum(d.ps),
        pegText: fmtNum(d.peg),
        yieldText: fmtPct(d.dividendYield),
        capText: fmtAmount(d.totalCap),
        pct3yText: d.percentile3y == null ? '--' : fmtNum(d.percentile3y, 1) + '%',
        pct5yText: d.percentile5y == null ? '--' : fmtNum(d.percentile5y, 1) + '%',
        industryPeText: fmtNum(d.industryAvgPe),
        industryPbText: fmtNum(d.industryAvgPb),
        industryYieldText: fmtPct(d.industryAvgYield),
        barWidth: d.percentile3y == null ? '0%' : Math.max(0, Math.min(100, d.percentile3y)) + '%',
        barLevel: this.barLevelOf(d.percentile3y),
        barLabel: this.barLabelOf(d.percentile3y)
      });
    },
    // <30% 偏低（绿）、>70% 偏高（红）、中间中性（灰）
    barLevelOf(p) {
      if (p == null || isNaN(p)) return 'flat';
      if (p < 30) return 'down';
      if (p > 70) return 'up';
      return 'flat';
    },
    barLabelOf(p) {
      if (p == null || isNaN(p)) return '暂无分位数据';
      if (p < 30) return fmtNum(p, 1) + '%（偏低）';
      if (p > 70) return fmtNum(p, 1) + '%（偏高）';
      return fmtNum(p, 1) + '%（中位）';
    }
  }
});
