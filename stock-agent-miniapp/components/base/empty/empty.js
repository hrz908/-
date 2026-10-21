// 空状态（替代 t-empty）：内联 SVG 插画 + 文案，零本地图片依赖
Component({
  externalClasses: ['empty-class'],
  properties: {
    text: { type: String, value: '暂无数据' },
    // 插画下的一行补充说明（可选）
    sub: { type: String, value: '' }
  },
  data: {
    src: ''
  },
  lifetimes: {
    attached() {
      this.setData({
        src:
          'data:image/svg+xml,' +
          encodeURIComponent(
            "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 48 48' fill='none' stroke='#9AA1AC' stroke-width='1.5' stroke-linecap='round' stroke-linejoin='round'><rect x='8' y='13' width='32' height='27' rx='3'/><path d='M8 23h32'/><path d='M20 13l-2.5-5h13L28 13'/><path d='M18 31h12'/><path d='M18 35h7'/></svg>"
          )
      );
    }
  }
});
