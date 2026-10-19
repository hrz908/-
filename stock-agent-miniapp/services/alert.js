// 接口层：提醒列表与新增提醒。字段与「新增提醒」弹窗一一对应
// （选股 / 触发条件 / 阈值 / 基准周期 / 检查频率）。
// 选股的模糊搜索直接复用 services/stock.js，与添加自选股是同一套接口和结果
import { get, post, put, del } from '../utils/request';

const API = '/api/v1';

// 条件类型：页面用驼峰（与弹窗选项的 value 一致），后端存下划线
const CONDITION_TO_API = {
  priceUp: 'price_up',
  priceDown: 'price_down',
  pctUp: 'pct_up',
  pctDown: 'pct_down',
  volume: 'volume'
};

const CONDITION_FROM_API = {
  price_up: 'priceUp',
  price_down: 'priceDown',
  pct_up: 'pctUp',
  pct_down: 'pctDown',
  volume: 'volume'
};

// 后端列表项 -> 页面渲染字段（下划线转驼峰，缺字段给默认值）
function toViewItem(item) {
  return {
    id: item.id,
    name: item.stock_name || '',
    code: item.stock_code || '',
    conditionType: CONDITION_FROM_API[item.condition_type] || item.condition_type,
    threshold: item.threshold,
    volumeBase: item.volume_base || 0,
    checkFrequency: item.check_frequency || 'realtime',
    enabled: !!item.enabled,
    // 有没有触发过，页面据此显示「已触发 / 未触发」和时间文案
    lastTriggeredAt: item.last_triggered_at || '',
    createdAt: item.created_at || ''
  };
}

// 查询提醒列表；传 stockCode 时只返回该股票的规则
export function fetchList(stockCode) {
  const data = {};
  if (stockCode) data.stock_code = stockCode;
  return get(`${API}/alerts`, data).then((list) => (list || []).map(toViewItem));
}

// 新增提醒。payload 就是弹窗里的字段：
// { stockCode, stockName, conditionType, threshold, volumeBase, checkFrequency }
export function create(payload) {
  const body = {
    stock_code: payload.stockCode,
    stock_name: payload.stockName,
    condition_type: CONDITION_TO_API[payload.conditionType] || payload.conditionType,
    threshold: String(payload.threshold),
    volume_base: payload.volumeBase || 0,
    check_frequency: payload.checkFrequency || 'realtime'
  };
  return post(`${API}/alerts`, body).then(toViewItem);
}

// 局部更新：只传要改的字段（条件类型 / 阈值 / 基准周期 / 检查频率 / 是否启用）
export function update(id, patch) {
  const body = {};
  if (patch.conditionType) {
    body.condition_type = CONDITION_TO_API[patch.conditionType] || patch.conditionType;
  }
  if (patch.threshold !== undefined) body.threshold = String(patch.threshold);
  if (patch.volumeBase !== undefined) body.volume_base = patch.volumeBase;
  if (patch.checkFrequency) body.check_frequency = patch.checkFrequency;
  if (patch.enabled !== undefined) body.enabled = !!patch.enabled;
  return put(`${API}/alerts/${id}`, body).then(toViewItem);
}

// 删除提醒规则（硬删）
export function remove(id) {
  return del(`${API}/alerts/${id}`);
}
