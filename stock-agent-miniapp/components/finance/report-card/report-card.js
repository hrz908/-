// 财报速览卡：回答「业绩怎么样」「增长了吗」
// 字段口径见设计文档第 2 节③。营收/净利/现金流用横向条形对比，
// 条形长度按三者最大值归一（纯展示缩放，不是业务计算）。
// 「超预期 / 不及预期」标签由一致预期值得出，文案只陈述事实（合规红线）。
const { fmtAmount, fmtPct, fmtNum, direction } = require('../../../utils/format');

Component({
  externalClasses: ['card-class'],
  properties: {
    // 报告期标签，如「2026 中报」；period 为具体日期
    periodLabel: { type: String, value: '' },
    period: { type: String, value: '' },
    // { value: 金额(元), yoy: 同比(% 数值), qoq: 环比(% 数值) }
    revenue: { type: Object, value: null },
    netProfit: { type: Object, value: null },
    cashFlow: { type: Object, value: null },
    grossMargin: { type: Number, value: null },
    netMargin: { type: Number, value: null },
    roe: { type: Number, value: null },
    eps: { type: Number, value: null },
    // 一致预期对比结论：'超预期' | '不及预期' | ''
    beat: { type: String, value: '' },
    // 一致预期值（用于口径标注）
    consensus: { type: Number, value: null },
    note: { type: String, value: '' }
  },
  data: {
    revenueBar: { value: '--', yoy: '--', yoyDir: 'flat', width: '0%' },
    profitBar: { value: '--', yoy: '--', yoyDir: 'flat', width: '0%' },
    cashBar: { value: '--', yoy: '--', yoyDir: 'flat', width: '0%' },
    grossMarginText: '--',
    netMarginText: '--',
    roeText: '--',
    epsText: '--',
    consensusText: '--'
  },
  observers: {
    'revenue, netProfit, cashFlow, grossMargin, netMargin, roe, eps, consensus'() {
      const d = this.data;
      const rows = [
        { key: 'revenueBar', src: d.revenue },
        { key: 'profitBar', src: d.netProfit },
        { key: 'cashBar', src: d.cashFlow }
      ];
      // 三条数据里的最大值作为条形基准
      const max = Math.max.apply(
        null,
        rows.map((r) => (r.src && !isNaN(parseFloat(r.src.value)) ? parseFloat(r.src.value) : 0))
      );
      const patch = {
        grossMarginText: fmtPct(d.grossMargin),
        netMarginText: fmtPct(d.netMargin),
        roeText: fmtPct(d.roe),
        epsText: fmtNum(d.eps),
        consensusText: fmtAmount(d.consensus)
      };
      rows.forEach((r) => {
        const src = r.src || {};
        const val = parseFloat(src.value);
        const yoy = parseFloat(src.yoy);
        patch[r.key] = {
          value: fmtAmount(src.value),
          yoy: isNaN(yoy) ? '--' : fmtPct(yoy),
          yoyDir: direction(src.yoy),
          width: isNaN(val) || max <= 0 ? '0%' : Math.max(2, (val / max) * 100) + '%'
        };
      });
      this.setData(patch);
    }
  }
});
