// 接口层：股票搜索（添加自选时的下拉联想）。
// 与 services/watchlist.js 同一套约定：页面只调这里，不直接碰 wx.request
import { get } from '../utils/request';

const API = '/api/v1';

// 模糊搜索股票：支持代码前缀、汉字名称、拼音缩写（"600" / "茅台" / "maotai"）
// 返回 [{ code, market, name }]，最多 limit 条。
// 结果由后端缓存 1 小时，前端无需再存一份
export function search(keyword, limit = 5) {
  return get(`${API}/stocks/search`, { q: keyword, limit });
}
