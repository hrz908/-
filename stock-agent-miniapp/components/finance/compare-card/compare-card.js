// 同业对比卡：回答「茅台和五粮液哪个好」「白酒板块谁估值低」
// 字段口径见设计文档第 2 节⑦。统一口径对比维度 + 差异高亮（标注谁更优），
// 高亮规则：区间涨幅/ROE/股息率「更高者优」（红），PE「更低者优」（绿）——
// 与 HTML 视觉稿一致。结论只能陈述事实，不得写成买卖推荐（合规红线）。
const { fmtNum, fmtPct, fmtPrice } = require('../../../utils/format');

// 对比维度配置：better = 'high' 越高越优 / 'low' 越低越优 / 'none' 不比较高低
const METRICS = [
  { key: 'price', label: '最新价', better: 'none', fmt: fmtPrice },
  { key: 'periodPct', label: '近1月', better: 'high', fmt: (v) => fmtPct(v) },
  { key: 'pe', label: 'PE(TTM)', better: 'low', fmt: (v) => fmtNum(v) },
  { key: 'roe', label: 'ROE', better: 'high', fmt: (v) => fmtPct(v) },
  { key: 'dividendYield', label: '股息率', better: 'high', fmt: (v) => fmtPct(v) }
];

// 赢家 tone：越高越优的指标赢家标红（up），越低越优的标绿（down）
const WINNER_TONE = { high: 'up', low: 'down' };

Component({
  externalClasses: ['card-class'],
  properties: {
    // 2-4 个对比标的 [{ name, price, periodPct, pe, roe, dividendYield }]
    items: { type: Array, value: [] },
    // 一句话事实性结论（调用方给出，只陈述客观事实）
    conclusion: { type: String, value: '' },
    note: { type: String, value: '' }
  },
  data: {
    title: '',
    subtitle: '',
    rows: [],
    tag: ''
  },
  observers: {
    items() {
      this.render();
    }
  },
  lifetimes: {
    attached() {
      this.render();
    },

    // ready 再兜一次：wx:if 首帧创建时 properties 可能尚未就绪，
    // attached 读到的是初始值（observer 对首值也不触发），ready 时必然已就绪
    ready() {
      this.render();
    }
  },
  methods: {
    render() {
      const items = (this.data.items || []).filter((it) => it && it.name);
      if (items.length < 2) {
        this.setData({ title: '', subtitle: '', rows: [], tag: '' });
        return;
      }

      const rows = METRICS.map((m) => {
        const values = items.map((it) => parseFloat(it[m.key]));
        let winner = -1;
        if (m.better !== 'none' && values.some((v) => !isNaN(v))) {
          const valid = values.map((v, i) => ({ v: isNaN(v) ? null : v, i: i })).filter((x) => x.v !== null);
          if (valid.length === items.length) {
            // 只有一个有效值时没有「对比」意义，不标赢家
            const best = valid.reduce((acc, cur) =>
              m.better === 'high' ? (cur.v > acc.v ? cur : acc) : cur.v < acc.v ? cur : acc
            );
            winner = best.i;
          }
        }
        return {
          label: m.label,
          cells: items.map((it, i) => ({
            text: m.fmt(it[m.key]),
            tone: i === winner ? WINNER_TONE[m.better] : ''
          }))
        };
      });

      // 标题：A vs B（超过两个标的时取前两个 + 等 N 个）
      const names = items.map((it) => it.name);
      const title = names.slice(0, 2).join(' vs ') + (names.length > 2 ? ' 等' + names.length + '个' : '');

      // 头部标签：PE 更低者（事实陈述，不带建议）
      const peIdx = this.winnerIndexOf(items, 'pe', 'low');
      const tag = peIdx >= 0 ? items[peIdx].name + ' PE 更低' : '';

      this.setData({
        title: title,
        subtitle: '同业对比',
        rows: rows,
        tag: tag
      });
    },
    winnerIndexOf(items, key, better) {
      const valid = items
        .map((it, i) => ({ v: parseFloat(it[key]), i: i }))
        .filter((x) => !isNaN(x.v));
      if (valid.length !== items.length || !valid.length) return -1;
      return valid.reduce((acc, cur) => (better === 'low' ? (cur.v < acc.v ? cur : acc) : cur.v > acc.v ? cur : acc)).i;
    }
  }
});
