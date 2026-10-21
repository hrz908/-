// 周期/筛选切换 chips（替代 t-tabs）：横向 scroll-view + 等宽 chips。
// 激活项用 scroll-into-view 拉回可视区（执行指南 4.2）。
Component({
  externalClasses: ['tabs-class'],
  properties: {
    // [{ key, label }]
    items: { type: Array, value: [] },
    // 当前选中 key（受控）
    value: { type: String, value: '' }
  },
  data: {
    activeIndex: -1,
    scrollInto: ''
  },
  observers: {
    'items, value'(items, value) {
      const index = (items || []).findIndex((it) => it.key === value);
      this.setData({
        activeIndex: index,
        // 空串表示不定位；给激活项 id 让 scroll-view 把它滚进可视区
        scrollInto: index >= 0 ? 'tab-chip-' + index : ''
      });
    }
  },
  methods: {
    onChipTap(e) {
      const index = Number(e.currentTarget.dataset.index);
      const item = this.data.items[index];
      if (!item || index === this.data.activeIndex) return;
      this.triggerEvent('change', { value: item.key, index: index, label: item.label });
    }
  }
});
