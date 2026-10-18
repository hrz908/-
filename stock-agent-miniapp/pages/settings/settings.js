import Toast from '../../miniprogram_npm/tdesign-miniprogram/toast/index';

Page({
  data: {
    darkMode: false
  },

  onLoad() {
    this.setData({ darkMode: !!wx.getStorageSync('sa_dark_mode') });
  },

  goBack() {
    // 立即出栈，不做任何自绘前置动画
    wx.navigateBack();
  },

  // TDesign switch 的 change 事件：e.detail.value 为切换后的布尔值
  onToggleDark(e) {
    const detail = e && e.detail;
    const darkMode =
      detail && typeof detail.value === 'boolean' ? detail.value : !this.data.darkMode;
    this.setData({ darkMode });
    wx.setStorageSync('sa_dark_mode', darkMode);
    Toast({
      context: this,
      message: darkMode ? '深色模式已开启' : '深色模式已关闭'
    });
  },

  onFontSize() {
    Toast({ context: this, message: '字体大小（演示）' });
  },

  goAbout() {
    wx.navigateTo({ url: '/pages/about/about' });
  }
});
