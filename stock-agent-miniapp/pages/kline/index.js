// 卡片画廊（原 K 线详情页，2026-10-22 改造）：八张金融卡的样式测试页。
//
// 用途与约定（详见 DESIGN.md「卡片画廊」节）：
// - 不接后端、不读接口，全部用手写假数据渲染八张卡，用于看卡片实例样式；
// - 本页与 chat 主页面**共用同一批组件**（components/finance/*）和同一个
//   卡片外层类 `.msg-card`（已上收到 styles/token.wxss）；
// - 因此调卡片样式只需改组件 wxss / token，两边自动同步，不存在两份样式；
// - 字段口径与 chat.js 的卡片工厂保持一致，方便对照真实链路的数据形状。
//
// 入口：watchlist 股票行点击、chat 页行情卡点击（原 K 线详情入口，现打开本页）。
Page({
  data: {
    titleText: '卡片样式',
    // 与 chat 页消息结构一致：{ role, status, card, content, <cardKey> }
    cards: [
      {
        role: 'assistant',
        status: 'complete',
        card: 'quote',
        content: [],
        quote: {
          name: '贵州茅台',
          code: '600519.SH',
          status: '交易中',
          price: 1523.66,
          change: 23.44,
          changePct: 1.56,
          open: 1502.1,
          high: 1528.88,
          low: 1498.2,
          amount: 4612000000,
          turnoverRate: 0.37,
          totalCap: 1914800000000,
          note: '行情时间 2026-10-22 15:00'
        }
      },
      {
        role: 'assistant',
        status: 'complete',
        card: 'kline',
        content: [],
        kline: [
          { d: '09-22', o: 1410, h: 1432, l: 1405, c: 1428, v: 32100 },
          { d: '09-23', o: 1428, h: 1445, l: 1420, c: 1438, v: 29800 },
          { d: '09-24', o: 1435, h: 1440, l: 1418, c: 1425, v: 27600 },
          { d: '09-25', o: 1425, h: 1432, l: 1402, c: 1405, v: 34200 },
          { d: '09-26', o: 1405, h: 1418, l: 1392, c: 1412, v: 31500 },
          { d: '09-29', o: 1412, h: 1425, l: 1400, c: 1420, v: 28800 },
          { d: '09-30', o: 1420, h: 1438, l: 1415, c: 1435, v: 30100 },
          { d: '10-09', o: 1435, h: 1455, l: 1430, c: 1450, v: 33500 },
          { d: '10-10', o: 1450, h: 1462, l: 1440, c: 1458, v: 28900 },
          { d: '10-11', o: 1458, h: 1470, l: 1452, c: 1466, v: 31200 },
          { d: '10-12', o: 1466, h: 1468, l: 1448, c: 1452, v: 35800 },
          { d: '10-13', o: 1452, h: 1462, l: 1445, c: 1460, v: 27600 },
          { d: '10-14', o: 1460, h: 1478, l: 1455, c: 1475, v: 33100 },
          { d: '10-15', o: 1475, h: 1490, l: 1470, c: 1486, v: 36400 },
          { d: '10-16', o: 1486, h: 1498, l: 1480, c: 1494, v: 29800 },
          { d: '10-17', o: 1494, h: 1502, l: 1482, c: 1488, v: 32100 },
          { d: '10-18', o: 1488, h: 1496, l: 1478, c: 1482, v: 28700 },
          { d: '10-19', o: 1482, h: 1500, l: 1475, c: 1496, v: 34500 },
          { d: '10-20', o: 1496, h: 1512, l: 1490, c: 1508, v: 37200 },
          { d: '10-21', o: 1508, h: 1515, l: 1500, c: 1512, v: 30400 },
          { d: '10-22', o: 1512, h: 1530, l: 1505, c: 1523.66, v: 41300 }
        ],
        klineMa: {
          ma5: [1431, 1434, 1433, 1431, 1429, 1424, 1427, 1435, 1443, 1450, 1455, 1453, 1456, 1462, 1469, 1475, 1478, 1481, 1488, 1495, 1510.33],
          ma10: [1425, 1428, 1430, 1429, 1427, 1424, 1426, 1431, 1438, 1444, 1449, 1450, 1452, 1457, 1463, 1468, 1471, 1474, 1480, 1487, 1494.6],
          ma20: [1418, 1421, 1424, 1425, 1424, 1421, 1422, 1426, 1432, 1437, 1441, 1442, 1444, 1448, 1453, 1457, 1460, 1462, 1466, 1470, 1460.25]
        },
        klineSub: '最近 21 个交易日 · 前复权',
        klinePct: 8.09,
        klineAmplitude: 9.73,
        klineVolumeRatio: 1.36,
        klineNote: '口径：区间涨跌幅按首末收盘价计算'
      },
      {
        role: 'assistant',
        status: 'complete',
        card: 'valuation',
        content: [],
        valuation: {
          peStatic: 28.6,
          peTTM: 24.18,
          pb: 8.42,
          ps: 11.6,
          dividendYield: 3.28,
          totalCap: 1914800000000,
          note: '600519.SH · 数据日期 2026-10-22'
        }
      },
      {
        role: 'assistant',
        status: 'complete',
        card: 'fundflow',
        content: [],
        fundflow: {
          mainNet: 128600000,
          tiers: [
            { label: '超大单', value: 86200000 },
            { label: '大单', value: 52400000 },
            { label: '中单', value: -18600000 },
            { label: '小单', value: -28400000 }
          ],
          note: '600519.SH · 数据日期 2026-10-22'
        }
      },
      {
        role: 'assistant',
        status: 'complete',
        card: 'report',
        content: [],
        report: {
          periodLabel: '2026 三季报',
          period: '2026-09-30',
          revenue: { value: 130246000000, yoy: 16.85 },
          netProfit: { value: 64680000000, yoy: 15.22 },
          cashFlow: { value: 42180000000, yoy: null },
          grossMargin: 91.78,
          netMargin: 52.34,
          roe: 24.68,
          eps: 51.42,
          beat: '',
          consensus: null,
          note: '600519.SH · 报告期 2026-09-30 · 单位：元'
        }
      },
      {
        role: 'assistant',
        status: 'complete',
        card: 'research',
        content: [],
        research: {
          org: '中信证券',
          rating: '买入',
          targetPrice: 1780,
          publishDate: '2026-10-18',
          reportType: '深度报告',
          summary: '公司三季度收入与利润增速符合预期，直销渠道占比提升带动吨价，预收账款维持高位，全年业绩确定性强。',
          forecastDir: '上调',
          upside: 16.82,
          ratingDist: [
            { rating: '买入', count: 24 },
            { rating: '增持', count: 9 },
            { rating: '中性', count: 2 }
          ],
          sourceName: '中信证券研究部',
          sourceUrl: 'https://example.com/research/600519-2026q3',
          note: '观点为机构研究结论，不构成投资建议'
        }
      },
      {
        role: 'assistant',
        status: 'complete',
        card: 'event',
        content: [],
        event: {
          events: [
            { type: '解禁', date: '2026-11-06', title: '限售股解禁 4.15 亿股', desc: '占总股本 3.31%，解禁类型为定向增发机构配售股份', important: true, sourceUrl: 'https://example.com/notice/unlock' },
            { type: '增减持', date: '2026-10-09', title: '控股股东增持计划进展', desc: '集团累计增持 68.3 万股，占公司总股本 0.05%', important: false, sourceUrl: 'https://example.com/notice/increase' },
            { type: '分红', date: '2026-10-15', title: '2026 年中期分红派息实施', desc: '每股派发现金红利 23.882 元，股权登记日 10 月 24 日', important: false, sourceUrl: 'https://example.com/notice/dividend' }
          ],
          range: '近 30 天 · 3 条',
          note: '事件来源：交易所公告'
        }
      },
      {
        role: 'assistant',
        status: 'complete',
        card: 'compare',
        content: [],
        compare: {
          items: [
            { name: '贵州茅台', price: 1523.66, periodPct: 8.62, pe: 24.18, roe: 24.68, dividendYield: 3.28 },
            { name: '五粮液', price: 148.32, periodPct: 5.14, pe: 16.42, roe: 22.15, dividendYield: 3.95 },
            { name: '泸州老窖', price: 132.86, periodPct: -2.35, pe: 15.28, roe: 25.44, dividendYield: 4.12 }
          ],
          conclusion: '近 1 月区间涨幅贵州茅台更高；PE(TTM) 泸州老窖更低。',
          note: '口径：最新价/近1月区间涨跌幅/PE(TTM)/ROE/股息率 · 数据日期 2026-10-22'
        }
      }
    ]
  },

  onLoad(query) {
    // 兼容旧入口：watchlist / chat 行情卡点击会带 code、name。
    // 本页是样式测试页，不按代码取数，仅把股票名带到标题上（有则用，无则默认）
    if (query && query.name) {
      this.setData({ titleText: decodeURIComponent(query.name) });
    }
  },

  onBack() {
    // 返回上一页（chat 行情卡入口 或 watchlist 行点击入口）。
    // 兜底：navigateBack 失败时（无上一页）用 reLaunch 回到主页，
    // 避免从分包子页卡死。chat 不是 tabBar 页，switchTab 会失败，故用 reLaunch。
    wx.navigateBack({ fail: () => wx.reLaunch({ url: '/pages/chat/chat' }) });
  }
});
