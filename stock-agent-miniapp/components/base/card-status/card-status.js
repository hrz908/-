// 卡片占位（loading / 失败态）：八张金融卡在「数据还没回来」与「取数失败」两段时间共用。
//
// 为什么单独做组件而不是塞进各张金融卡里：
// - 加载态没有数据可渲染，金融卡组件拿到空 props 会渲染一堆「--」，观感比骨架屏更差；
// - 八张卡的版式不同（栅格 / 表格 / 时间轴），但加载态只需要表达「这是一张卡，正在出数据」，
//   统一一份骨架即可，不必维护八套；
// - 失败态要有明确文案，同样不能借用「--」表达（那会被读成「数据就是空的」）。
//
// 生命周期由 chat 页驱动（见 pages/chat/chat.js 的 _pushCardSlot / _placeCard / _failCardSlot）：
// tool_start 插一张 loading 占位 -> card/table 事件到了就地替换成真卡 -> 取数失败转 error 态。
Component({
  externalClasses: ['card-class'],
  properties: {
    // loading = 骨架屏 + 转圈 + 文案；error = 警示图标 + 失败文案
    state: { type: String, value: 'loading' },
    // 主文案：loading 下是「正在获取xx…」，error 下是「xx获取失败」
    label: { type: String, value: '正在获取卡片数据…' },
    // 副文案：错误原因（数据源超时 / 连接失败等），可空
    detail: { type: String, value: '' }
  }
});
