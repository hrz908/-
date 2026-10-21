// 取色工具：把 CSS 变量名解析成具体色值。
// 背景：SVG data-URI（图标 / 迷你走势线）在编码时颜色已固化，
// 里面的 var(--xx) 不会被页面样式解析，必须先在这里映射成 hex。
// WXML 里可以继续写 var(--xx)（t-icon 时代的写法），由本工具统一翻译。
// 2026-10-22：随浅色主题整体切换，取值与 styles/token.wxss 保持一致。
const COLOR_VARS = {
  'var(--ink)': '#181A1C',
  'var(--ink-2)': '#5F6875',
  'var(--ink-3)': '#9AA1AC',
  'var(--up)': '#F04A4A',
  'var(--down)': '#1AA97C',
  'var(--flat)': '#8A97A5',
  'var(--accent)': '#2559EB',
  'var(--line)': '#E8EBF0',
  'var(--bg)': '#F5F6F8',
  'var(--surface)': '#FFFFFF',
  'var(--surface-2)': '#F2F4F7'
};

// 涨跌方向 -> 色值（国内红涨绿跌）
const TREND_COLORS = {
  up: '#F04A4A',
  down: '#1AA97C',
  flat: '#8A97A5'
};

// 入参可以是 var(--xx)（仅支持上表里的名字）、trend 名或直接 hex
function resolveColor(color, fallback) {
  const raw = color || fallback || '#5F6875';
  if (COLOR_VARS[raw]) return COLOR_VARS[raw];
  if (TREND_COLORS[raw]) return TREND_COLORS[raw];
  return raw;
}

module.exports = {
  COLOR_VARS: COLOR_VARS,
  TREND_COLORS: TREND_COLORS,
  resolveColor: resolveColor
};
