// 研报观点卡：回答「机构怎么看」「目标价多少」
// 字段口径见设计文档第 2 节⑤。右上角评级 + 目标价是卡片「结论位」，
// 左竖线引用核心观点。合规：必须带来源机构名 / 原文链接（设计文档四-4）；
// 卡内不出现任何买卖建议措辞。
// 上行空间由调用方算好传入（组件内不做业务计算）。
const { fmtPrice, fmtPct, direction } = require('../../../utils/format');

// 评级 -> 色调（视觉稿：买入为红；中性灰；减持/卖出绿）。
// 只影响呈现，不构成建议
const RATING_TONES = {
  买入: 'up',
  增持: 'up',
  中性: 'flat',
  减持: 'down',
  卖出: 'down'
};

Component({
  externalClasses: ['card-class'],
  properties: {
    org: { type: String, value: '' },
    rating: { type: String, value: '' },
    targetPrice: { type: Number, value: null },
    publishDate: { type: String, value: '' },
    reportType: { type: String, value: '' },
    // 核心观点摘要（1-2 句）
    summary: { type: String, value: '' },
    // 盈利预测调整方向：上调 / 维持 / 下调
    forecastDir: { type: String, value: '' },
    // 上行空间（% 数值，调用方按现价算好）
    upside: { type: Number, value: null },
    // 近 90 天评级分布 [{ rating, count }]
    ratingDist: { type: Array, value: [] },
    // 来源（合规必填）：机构名 + 原文链接
    sourceName: { type: String, value: '' },
    sourceUrl: { type: String, value: '' },
    note: { type: String, value: '' }
  },
  data: {
    targetPriceText: '--',
    upsideText: '--',
    upsideDir: 'flat',
    ratingTone: 'flat',
    distText: ''
  },
  observers: {
    'rating, targetPrice, upside, ratingDist'() {
      const d = this.data;
      const dist = (d.ratingDist || [])
        .filter((r) => r && r.count)
        .map((r) => r.rating + ' ' + r.count)
        .join(' / ');
      this.setData({
        targetPriceText: fmtPrice(d.targetPrice),
        upsideText: d.upside == null ? '--' : fmtPct(d.upside),
        upsideDir: direction(d.upside),
        ratingTone: RATING_TONES[d.rating] || 'flat',
        distText: dist
      });
    }
  },
  methods: {
    // 原文链接：小程序不能直接打开外链，点击复制（与 chat 页链接行为一致）
    onSourceTap() {
      const url = this.data.sourceUrl;
      if (!url) return;
      wx.setClipboardData({
        data: url,
        success: () => this.triggerEvent('copylink', { url: url })
      });
    }
  }
});
