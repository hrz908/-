// 取色工具：把 CSS 变量名解析成具体色值。
// 背景：SVG data-URI（图标 / 迷你走势线）在编码时颜色已固化，
// 里面的 var(--xx) 不会被页面样式解析，必须先在这里映射成 hex。
// WXML 里可以继续写 var(--xx)（t-icon 时代的写法），由本工具统一翻译。
const COLOR_VARS = {
  'var(--ink)': '#E8EDF2',
  'var(--ink-2)': '#9AA9B8',
  'var(--ink-3)': '#6B7A8A',
  'var(--up)': '#F2555A',
  'var(--down)': '#1FB87A',
  'var(--flat)': '#8A97A5',
  'var(--accent)': '#E9B949',
  'var(--line)': '#26333F',
  'var(--bg)': '#0B1219',
  'var(--surface)': '#141E29',
  'var(--surface-2)': '#1B2634'
};

// 涨跌方向 -> 色值（国内红涨绿跌）
const TREND_COLORS = {
  up: '#F2555A',
  down: '#1FB87A',
  flat: '#8A97A5'
};

// 入参可以是 var(--xx)（仅支持上表里的名字）、trend 名或直接 hex
function resolveColor(color, fallback) {
  const raw = color || fallback || '#9AA9B8';
  if (COLOR_VARS[raw]) return COLOR_VARS[raw];
  if (TREND_COLORS[raw]) return TREND_COLORS[raw];
  return raw;
}

module.exports = {
  COLOR_VARS: COLOR_VARS,
  TREND_COLORS: TREND_COLORS,
  resolveColor: resolveColor
};
