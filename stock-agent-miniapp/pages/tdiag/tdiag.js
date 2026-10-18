Page({
  data: {
    sw: true,
    result: ''
  },

  onLoad() {
    this.setData({ result: '页面 JS 已执行' });
  },

  goBack() {
    wx.navigateBack();
  },

  onSwitch(e) {
    const v = e && e.detail && typeof e.detail.value === 'boolean' ? e.detail.value : !this.data.sw;
    this.setData({ sw: v, result: 'switch 事件正常：' + v });
  },

  onClick() {
    this.setData({ result: 'click 事件正常' });
  }
});
