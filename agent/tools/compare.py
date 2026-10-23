"""同业对比工具（数据源：Go 网关行情快照，腾讯当日数据）。

产品约定：同业对比只取当日数据。五个对比维度全部来自一次
`GET /api/v1/stock/quote`（腾讯当日快照）：最新价、当日涨跌幅、
市盈率 PE(TTM)、市净率 PB、总市值——不碰任何历史序列与财务接口，
不消耗 Tushare 配额，快照在网关还有 30s Redis 缓存。

peers 由模型按行业常识给代码（不知道代码时先调 symbol_search），
最多 2 家同行（前端对比卡固定「本股 + 2 家」三列）。
"""
import asyncio

from langchain_core.tools import tool

from tools import call_tool_safe, go_get, to_ts_code

# 前端 compare 卡固定三列：本股 + 最多两家同行
_MAX_PEERS = 2


def _split_peers(peers: str) -> list:
    """"000858,600809" -> ["000858", "600809"]，去空去重、截到上限。"""
    out = []
    for p in (peers or "").replace("，", ",").split(","):
        p = p.strip()
        if p and p not in out:
            out.append(p)
    return out[:_MAX_PEERS]


async def _fetch_one(code: str) -> dict:
    """拉一只股票的当日快照并折成对比卡的 item 结构；失败降级为带 error 的项。"""
    res = await call_tool_safe(go_get, "/api/v1/stock/quote", {"code": code})
    if not res.get("ok"):
        return {"code": code, "error": str(res.get("error") or "quote failed")}
    d = res.get("data") or {}
    def _f(v):
        try:
            return round(float(v), 4) if v not in (None, "") else None
        except (TypeError, ValueError):
            return None
    return {
        "code": d.get("code") or code,
        "name": d.get("name") or code,
        # 前端 compare 卡的五个维度（详见 components/finance/compare-card）
        "price": _f(d.get("price")),
        "changePct": _f(d.get("change_percent")),
        "pe": _f(d.get("pe")),
        "pb": _f(d.get("pb")),
        "totalMv": _f(d.get("total_mv")),
    }


@tool
async def get_peer_compare(code: str, peers: str = "") -> dict:
    """同业对比：本股 + 最多 2 家同行，仅取当日行情快照对比
    最新价、涨跌幅、市盈率 PE(TTM)、市净率 PB、总市值。

    Args:
        code: 本股代码，如 "600519"。
        peers: 同行代码，逗号分隔（如 "000858,600809"），最多 2 家；
               只有名称不知道代码时，先调 symbol_search 查代码。

    Returns:
        {"ok": True, "data": {items: [{name, price, changePct, pe, pb, totalMv}...]}}
        成功（items 首位是本股）；单只失败时该项带 error，不影响其余标的。
    """
    codes = [code] + _split_peers(peers)
    items = await asyncio.gather(*[_fetch_one(c) for c in codes])
    ok_items = [it for it in items if "error" not in it]
    if not ok_items:
        return {"ok": False, "error": "行情快照全部获取失败，无法对比"}
    return {"ok": True, "data": {"items": items, "ts_code": to_ts_code(code)}}
