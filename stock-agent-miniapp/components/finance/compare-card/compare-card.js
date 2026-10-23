// 同业对比卡：回答「茅台和五粮液哪个好」「白酒板块谁估值低」
// 字段口径见设计文档第 2 节⑦。只对比两家同行（本股 + 2 家，共 3 列），
// 标题形如「A vs B vs C」。着色规则：近1月按正负（加号红 / 减号绿），
// ROE / 股息率「更高者优」（红），PE「更低者优」（绿）——与 HTML 视觉稿一致。
// 卡内只陈述客观事实，不写买卖推荐（合规红线）。
const { fmtNum, fmtPct, fmtPrice, direction } = require('../../../utils/format');

// 最多三个标的：本股 + 两家同行（需求：只对比两家同行，标题 A vs B vs C）
const MAX_ITEMS = 3;

// 对比维度配置：better = 'high' 越高越优 / 'low' 越低越优 / 'none' 不比较高低；
// sign = true 的指标（近1月）不比高低，按正负着色：加号红 / 减号绿
const METRICS = [
  { key: 'price', label: '最新价', better: 'none', fmt: fmtPrice },
  { key: 'periodPct', label: '近1月', better: 'none', sign: true, fmt: (v) => fmtPct(v) },
  { key: 'pe', label: 'PE(TTM)', better: 'low', fmt: (v) => fmtNum(v) },
  { key: 'roe', label: 'ROE', better: 'high', fmt: (v) => fmtPct(v) },
  { key: 'dividendYield', label: '股息率', better: 'high', fmt: (v) => fmtPct(v) }
];

// 赢家 tone：越高越优的指标赢家标红（up），越低越优的标绿（down）
const WINNER_TONE = { high: 'up', low: 'down' };

Component({
  externalClasses: ['card-class'],
  properties: {
    // 对比标的 [{ name, price, periodPct, pe, roe, dividendYield }]，
    // 只取前 3 个（本股 + 两家同行）
    items: { type: Array, value: [] },
    note: { type: String, value: '' }
  },
  data: {
    title: '',
    subtitle: '',
    // 表头列：与 rows 同一批标的（已截断），避免表头列数多于数据列
    cols: [],
    rows: []
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
      // 只对比两家同行：本股 + 两家同行 = 3 列
      const items = (this.data.items || []).filter((it) => it && it.name).slice(0, MAX_ITEMS);
      if (items.length < 2) {
        this.setData({ title: '', subtitle: '', cols: [], rows: [] });
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
            // 近1月这类带方向的指标按正负着色：加号红 / 减号绿；其余指标标赢家
            tone: m.sign ? direction(it[m.key]) : i === winner ? WINNER_TONE[m.better] : ''
          }))
        };
      });

      // 标题：A vs B vs C（最多三个标的）
      const title = items.map((it) => it.name).join(' vs ');

      this.setData({
        title: title,
        subtitle: '同业对比',
        cols: items,
        rows: rows
      });
    }
  }
});
