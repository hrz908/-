// 自研图标组件：SVG data-URI 背景图（替代 t-icon）
// 选型理由（执行指南 4.1）：WXSS 禁用 url('./x.png') 本地背景图，
// SVG 内联进 data URI 零文件依赖；小程序 JSVM 的 btoa 可用性不稳，
// 故用 encodeURIComponent 编码（等效内联，不依赖任何运行时能力）。
// 注意：背景图里的颜色在编码时已固化，因此 color 只接受具体色值；
// 为保持页面写法顺手，var(--xx) 名称由 utils/color.js 统一翻译成 hex
const { resolveColor } = require('../../../utils/color');
const ICONS_STROKE = {
  'arrow-left': "<path d='M19 12H5'/><path d='M12 19l-7-7 7-7'/>",
  'arrow-up': "<path d='M12 19V5'/><path d='M5 12l7-7 7 7'/>",
  'arrow-down': "<path d='M12 5v14'/><path d='M19 12l-7 7-7-7'/>",
  menu: "<path d='M4 7h16'/><path d='M4 12h16'/><path d='M4 17h16'/>",
  send: "<path d='M12 19V5'/><path d='M5 12l7-7 7 7'/>",
  keyboard:
    "<rect x='2.5' y='7' width='19' height='10' rx='2'/><path d='M6 11h.01'/><path d='M10 11h.01'/><path d='M14 11h.01'/><path d='M18 11h.01'/><path d='M8 14.5h8'/>",
  voice: "<path d='M4 10v4'/><path d='M8 7v10'/><path d='M12 4v16'/><path d='M16 7v10'/><path d='M20 10v4'/>",
  star: "<path d='M12 3.5l2.6 5.3 5.8.8-4.2 4.1 1 5.8-5.2-2.8-5.2 2.8 1-5.8L3.6 9.6l5.8-.8z'/>",
  user: "<path d='M5 20c0-3.3 3.1-5.5 7-5.5s7 2.2 7 5.5'/><circle cx='12' cy='8' r='3.6'/>",
  notification:
    "<path d='M18 8.5a6 6 0 10-12 0c0 6-2.5 7.5-2.5 7.5h17S18 14.5 18 8.5z'/><path d='M10.2 19.5a2 2 0 003.6 0'/>",
  refresh: "<path d='M20 12a8 8 0 11-2.5-5.8'/><path d='M20 3.5v4h-4'/>",
  add: "<path d='M12 5v14'/><path d='M5 12h14'/>",
  close: "<path d='M6 6l12 12'/><path d='M18 6L6 18'/>",
  delete:
    "<path d='M4 7h16'/><path d='M10 11v6'/><path d='M14 11v6'/><path d='M6 7l1 13a2 2 0 002 2h6a2 2 0 002-2l1-13'/><path d='M9 7V5.5A1.5 1.5 0 0110.5 4h3A1.5 1.5 0 0115 5.5V7'/>",
  time: "<circle cx='12' cy='12' r='8.5'/><path d='M12 7.5V12l3 1.8'/>",
  'chevron-right': "<path d='M9 6l6 6-6 6'/>",
  search: "<circle cx='10.5' cy='10.5' r='6.5'/><path d='M15.5 15.5L21 21'/>",
  'chart-bar': "<path d='M6 20V11'/><path d='M12 20V4'/><path d='M18 20v-6'/>",
  link:
    "<path d='M14 4h6v6'/><path d='M20 4l-8.5 8.5'/><path d='M18 13.5V18a2 2 0 01-2 2H6a2 2 0 01-2-2V8a2 2 0 012-2h4.5'/>",
  info: "<circle cx='12' cy='12' r='8.5'/><path d='M12 11v5'/><path d='M12 8h.01'/>",
  check: "<path d='M5 12.5l4.5 4.5L19 7.5'/>"
};

// 实心图标（播放 / 暂停）：填充色 = color
const ICONS_FILL = {
  play: "<path d='M8 5.2v13.6L19.5 12z'/>",
  pause: "<path d='M7 5h3.2v14H7z'/><path d='M13.8 5H17v14h-3.2z'/>"
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
