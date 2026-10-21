// 自研图标组件：SVG data-URI 背景图（替代 t-icon）
// 选型理由（执行指南 4.1）：WXSS 禁用 url('./x.png') 本地背景图，
// SVG 内联进 data URI 零文件依赖；小程序 JSVM 的 btoa 可用性不稳，
// 故用 encodeURIComponent 编码（等效内联，不依赖任何运行时能力）。
//
// 图标来源：Lucide（https://lucide.dev/），ISC 许可，可商用、无需署名。
// 几何数据取自官方包 lucide-static v1.61.0 的 icons/*.svg，只提取 <svg> 内部
// 图元（path/circle/rect），外层的 viewBox / stroke 由本组件统一注入，
// 因此颜色仍可随 color 属性动态变化（涨跌红绿、蓝底反白都依赖这点）。
// lucide-static 只是 devDependency，用来取源数据，不进小程序包体。
//
// 命名沿用项目内部语义名（页面不需要改），与 Lucide 官方名的对应关系见下方注释。
// 注意：背景图里的颜色在编码时已固化，因此 color 只接受具体色值；
// 为保持页面写法顺手，var(--xx) 名称由 utils/color.js 统一翻译成 hex
const { resolveColor } = require('../../../utils/color');

// 描边图标：统一 stroke-width 2 / round 端点（Lucide 默认规格）
const ICONS_STROKE = {
  // arrow-left
  'arrow-left': "<path d='m12 19-7-7 7-7'/><path d='M19 12H5'/>",
  // arrow-up
  'arrow-up': "<path d='m5 12 7-7 7 7'/><path d='M12 19V5'/>",
  // arrow-down
  'arrow-down': "<path d='M12 5v14'/><path d='m19 12-7 7-7-7'/>",
  // menu
  menu: "<path d='M4 5h16'/><path d='M4 12h16'/><path d='M4 19h16'/>",
  // 发送键用 arrow-up 而不是 Lucide 的 send（纸飞机）：视觉稿里发送键是向上箭头
  send: "<path d='m5 12 7-7 7 7'/><path d='M12 19V5'/>",
  // keyboard
  keyboard:
    "<path d='M10 8h.01'/><path d='M12 12h.01'/><path d='M14 8h.01'/><path d='M16 12h.01'/><path d='M18 8h.01'/><path d='M6 8h.01'/><path d='M7 16h10'/><path d='M8 12h.01'/><rect width='20' height='16' x='2' y='4' rx='2'/>",
  // audio-lines（波形，与原来的语音图标语义一致；Lucide 的 mic 是话筒）
  voice:
    "<path d='M2 10v3'/><path d='M6 6v11'/><path d='M10 3v18'/><path d='M14 8v7'/><path d='M18 5v13'/><path d='M22 10v3'/>",
  // star
  star:
    "<path d='M11.525 2.295a.53.53 0 0 1 .95 0l2.31 4.679a2.123 2.123 0 0 0 1.595 1.16l5.166.756a.53.53 0 0 1 .294.904l-3.736 3.638a2.123 2.123 0 0 0-.611 1.878l.882 5.14a.53.53 0 0 1-.771.56l-4.618-2.428a2.122 2.122 0 0 0-1.973 0L6.396 21.01a.53.53 0 0 1-.77-.56l.881-5.139a2.122 2.122 0 0 0-.611-1.879L2.16 9.795a.53.53 0 0 1 .294-.906l5.165-.755a2.122 2.122 0 0 0 1.597-1.16z'/>",
  // user
  user: "<path d='M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2'/><circle cx='12' cy='7' r='4'/>",
  // bell
  notification:
    "<path d='M10.268 21a2 2 0 0 0 3.464 0'/><path d='M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326'/>",
  // refresh-cw
  refresh:
    "<path d='M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8'/><path d='M21 3v5h-5'/><path d='M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16'/><path d='M8 16H3v5'/>",
  // plus
  add: "<path d='M5 12h14'/><path d='M12 5v14'/>",
  // x
  close: "<path d='M18 6 6 18'/><path d='m6 6 12 12'/>",
  // trash-2
  delete:
    "<path d='M10 11v6'/><path d='M14 11v6'/><path d='M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6'/><path d='M3 6h18'/><path d='M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2'/>",
  // clock
  time: "<circle cx='12' cy='12' r='10'/><path d='M12 6v6l4 2'/>",
  // chevron-right
  'chevron-right': "<path d='m9 18 6-6-6-6'/>",
  // search
  search: "<path d='m21 21-4.34-4.34'/><circle cx='11' cy='11' r='8'/>",
  // chart-no-axes-column（三根裸柱，比 chart-column 少坐标轴，小尺寸更清楚）
  'chart-bar': "<path d='M5 21v-6'/><path d='M12 21V3'/><path d='M19 21V9'/>",
  // link
  link:
    "<path d='M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71'/><path d='M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71'/>",
  // info
  info: "<circle cx='12' cy='12' r='10'/><path d='M12 16v-4'/><path d='M12 8h.01'/>",
  // check
  check: "<path d='M20 6 9 17l-5-5'/>"
};

// 实心图标（播放 / 暂停）：填充色 = color。
// Lucide 官方只发描边版，这里沿用它的几何但改填充呈现——
// 语音气泡上的播放/暂停在 44rpx 尺寸下描边会显得太轻，实心更清楚
const ICONS_FILL = {
  play: "<path d='M5 5a2 2 0 0 1 3.008-1.728l11.997 6.998a2 2 0 0 1 .003 3.458l-12 7A2 2 0 0 1 5 19z'/>",
  pause: "<rect x='14' y='3' width='5' height='18' rx='1'/><rect x='5' y='3' width='5' height='18' rx='1'/>"
};

function buildSrc(name, color) {
  const c = resolveColor(color);
  const stroke = ICONS_STROKE[name];
  if (stroke) {
    return (
      'data:image/svg+xml,' +
      encodeURIComponent(
        "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='" +
          c +
          "' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'>" +
          stroke +
          '</svg>'
      )
    );
  }
  const fill = ICONS_FILL[name];
  if (fill) {
    return (
      'data:image/svg+xml,' +
      encodeURIComponent(
        "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='" + c + "'>" + fill + '</svg>'
      )
    );
  }
  return '';
}

Component({
  options: {
    // 图标是纯展示件，允许外部用 externalClasses 追加定位/尺寸类
    multipleSlots: false
  },
  externalClasses: ['icon-class'],
  properties: {
    // 图标名，见 ICONS_STROKE / ICONS_FILL
    name: { type: String, value: '' },
    // 尺寸：默认 44rpx（22px），与触摸目标匹配
    size: { type: String, value: '44rpx' },
    // 颜色：具体色值或 var(--xx)（var 仅支持 COLOR_VARS 里的常用名）
    color: { type: String, value: 'var(--ink-2)' }
  },
  data: {
    src: ''
  },
  observers: {
    'name, color'(name, color) {
      this.setData({ src: buildSrc(name, color) });
    }
  }
});
