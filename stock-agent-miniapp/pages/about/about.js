import Toast from '../../miniprogram_npm/tdesign-miniprogram/toast/index';

Page({

  goBack() {
    // 立即出栈，不做任何自绘前置动画
    wx.navigateBack();
  },

  onCheckUpdate() {
    Toast({ context: this, message: '当前已是最新版本' });
  }
});
