// 资金流向卡：回答「主力资金流入还是流出」「北向加仓了吗」
// 字段口径见设计文档第 2 节⑥。主数字放大（22px 级），四档拆分逐行展开，
// 正负值自动着色（涨跌方向由数值正负推导，不手写 class）。
const { fmtAmount, fmtSign, fmtPct, fmtNum, direction } = require('../../../utils/format');

Component({
  externalClasses: ['card-class'],
  properties: {
    // 主力净流入额（元）
    mainNet: { type: Number, value: null },
    // 占成交额比（% 数值）
    mainNetPct: { type: Number, value: null },
    // 四档拆分 [{ label, value }]（元）
    tiers: { type: Array, value: [] },
    // 北向持股比例（% 数值）及变动（pct）
    northPct: { type: Number, value: null },
    northChangePct: { type: Number, value: null },
    // 连续流入 / 流出天数：正数=流入，负数=流出
    streakDays: { type: Number, value: null },
    // 近 5 日流向趋势（数值数组，画迷你线）
    days5: { type: Array, value: [] },
    // 龙虎榜 [{ seat, buy, sell }]（元）
    lhb: { type: Array, value: [] },
    // 融资融券余额（元）
    marginBalance: { type: Number, value: null },
    note: { type: String, value: '' }
  },
  data: {
    mainNetText: '--',
    mainNetDir: 'flat',
    mainNetPctText: '--',
    tierRows: [],
    northText: '',
    streakText: '',
    days5Dir: 'flat',
    lhbRows: [],
    marginText: '--'
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
    'mainNet, mainNetPct, tiers, northPct, northChangePct, streakDays, days5, lhb, marginBalance'() {
      this.refresh();
    }
  },
  methods: {
    refresh() {
      const d = this.data;
      const streak = d.streakDays;
      const streakAbs = streak == null || isNaN(streak) ? 0 : Math.abs(Math.round(streak));
      this.setData({
        mainNetText: fmtAmount(d.mainNet),
        mainNetDir: direction(d.mainNet),
        mainNetPctText: d.mainNetPct == null ? '' : '占成交额 ' + fmtPct(d.mainNetPct),
        tierRows: (d.tiers || []).map((t) => ({
          label: t.label,
          value: fmtAmount(t.value),
          dir: direction(t.value)
        })),
        northText:
          d.northPct == null
            ? ''
            : fmtPct(d.northPct) + (d.northChangePct == null ? '' : '（' + fmtSign(d.northChangePct) + 'pct）'),
        streakText: streakAbs ? '连续' + streakAbs + '日净' + (streak > 0 ? '流入' : '流出') : '',
        days5Dir: direction(d.days5 && d.days5.length ? d.days5[d.days5.length - 1] : null),
        lhbRows: (d.lhb || []).map((r) => ({
          seat: r.seat,
          buy: fmtAmount(r.buy),
          sell: fmtAmount(r.sell)
        })),
        marginText: fmtAmount(d.marginBalance)
      });
    }
  }
});
