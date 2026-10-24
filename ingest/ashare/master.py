"""A股主数据同步：股票清单 / 行业 / 概念（迁移 008）。

和文本语料入库是两条线：这里只写 stock_basic / stock_industry / stock_concept，
不碰 documents / chunks，也不做向量化。

三个约定：
- 代码一律 6 位裸码；market / board 由库里的生成列推导，本模块不判交易所；
- 退市只改 status，绝不 DELETE——外键是 CASCADE，删行会连带行业与概念；
- 行业 / 概念按「本轮先 upsert、再删本轮没写到的旧行」整批替换，不做 TRUNCATE。
"""
import logging
import time
from datetime import UTC, datetime

import psycopg
import psycopg.errors

from ashare import config
from ashare.universe import load_universe
from ashare.util import cell, parse_datetime

logger = logging.getLogger(__name__)

# 概念/行业板块接口在 akshare 里走内部 requests，不受 net.HttpClient 限速，这里自己控节奏
_QPS = max(0.2, float(config.EASTMONEY_QPS))

_TABLES = ("stock_basic", "stock_industry", "stock_concept")


def ensure_master_schema(store) -> None:
    """校验 008 的表是否已建；缺表时给出可执行的提示，而不是让插入报 UndefinedTable。"""
    for table in _TABLES:
        try:
            store.conn.execute(f"SELECT 1 FROM {table} LIMIT 1")
            store.conn.commit()
        except psycopg.errors.UndefinedTable as exc:
            store.conn.rollback()
            raise RuntimeError(
                f"缺少表 {table}：先执行 "
                "python -m ashare.cli init-db --file 008_stock_master.sql"
            ) from exc


def _ak():
    import akshare as ak

    return ak


def _throttle() -> None:
    time.sleep(1.0 / _QPS)


def _valid_code(code: str) -> bool:
    return len(code) == 6 and code.isdigit()


def _is_st(name: str) -> bool:
    """名称前缀即 ST 口径（*ST / ST / SST / S*ST），与行情源展示一致。"""
    return name.upper()[:4].startswith(("ST", "*ST", "SST", "S*ST"))


def _listing_dates(ak) -> dict[str, object]:
    """交易所清单里的上市日期。单点失败只跳过该市场，不影响主流程。"""
    calls = (
        (ak.stock_info_sh_name_code, {"symbol": "主板A股"}),
        (ak.stock_info_sh_name_code, {"symbol": "科创板"}),
        (ak.stock_info_sz_name_code, {"symbol": "A股列表"}),
        (ak.stock_info_bj_name_code, {}),
    )
    out: dict[str, object] = {}
    for fn, kwargs in calls:
        try:
            df = fn(**kwargs)
        except Exception as exc:  # noqa: BLE001 - 上市日期是增强项，缺了不阻塞
            logger.warning("上市日期取数失败(%s %s)：%s", getattr(fn, "__name__", fn), kwargs, exc)
            continue
        if df is None or df.empty:
            continue
        for _, row in df.iterrows():
            code = cell(row, "证券代码", "A股代码", "代码")
            if not _valid_code(code):
                continue
            dt = parse_datetime(cell(row, "上市日期", "上市时间"))
            if dt:
                out[code] = dt.date()
    return out


def _board_list(ak, kind: str) -> list[tuple[str, str]]:
    """东财板块清单 -> [(板块名, 板块代码)]；板块名是成分接口的参数。"""
    fn = ak.stock_board_industry_name_em if kind == "industry" else ak.stock_board_concept_name_em
    df = fn()
    if df is None or df.empty:
        return []
    out = []
    for _, row in df.iterrows():
        name = cell(row, "板块名称", "名称", "概念名称")
        if name:
            out.append((name, cell(row, "板块代码", "代码")))
    return out


def _board_members(ak, kind: str, board: str) -> list[str]:
    """板块成分 -> 6 位代码列表。"""
    fn = (ak.stock_board_industry_cons_em if kind == "industry"
          else ak.stock_board_concept_cons_em)
    df = fn(symbol=board)
    if df is None or df.empty:
        return []
    codes = []
    for _, row in df.iterrows():
        code = cell(row, "代码", "证券代码", "股票代码")
        if _valid_code(code):
            codes.append(code)
    return codes


def _known_codes(store) -> set[str]:
    rows = store.conn.execute("SELECT code FROM stock_basic").fetchall()
    store.conn.commit()
    return {r["code"] for r in rows}


def sync_basic(store) -> dict:
    """股票清单：code/name/上市日/ST 标记，并把消失的代码标成退市。"""
    codes = load_universe(force=True)
    if not codes:
        raise RuntimeError("A股代码表为空，终止同步")
    dates = _listing_dates(_ak())
    rows = [(code, name, dates.get(code), _is_st(name)) for code, name in codes.items()]

    with store.conn.cursor() as cur:
        cur.executemany(
            """
            INSERT INTO stock_basic (code, name, list_date, is_st, source, updated_at)
            VALUES (%s, %s, %s, %s, 'akshare', now())
            ON CONFLICT (code) DO UPDATE
               SET name       = EXCLUDED.name,
                   list_date  = COALESCE(EXCLUDED.list_date, stock_basic.list_date),
                   is_st      = EXCLUDED.is_st,
                   status     = 'listed',
                   delist_date = NULL,
                   source     = EXCLUDED.source,
                   updated_at = now()
            """,
            rows,
        )
        delisted = 0
        # 上游偶发只返回部分市场，条数异常少时不做退市判定，否则会把半个市场标死
        if len(codes) < config.MASTER_MIN_CODES:
            logger.warning("代码表仅 %d 条（阈值 %d），跳过退市判定",
                           len(codes), config.MASTER_MIN_CODES)
        else:
            cur.execute(
                """
                UPDATE stock_basic
                   SET status = 'delisted',
                       delist_date = COALESCE(delist_date, now()::date),
                       updated_at = now()
                 WHERE status = 'listed' AND NOT (code = ANY(%s::text[]))
                """,
                (list(codes),),
            )
            delisted = cur.rowcount or 0
    store.conn.commit()
    logger.info("股票清单同步完成：%d 只，新增退市 %d 只，含上市日 %d 只",
                len(codes), delisted, len(dates))
    return {"codes": len(codes), "with_list_date": len(dates), "delisted": delisted}


def sync_industry(store, limit: int = 0) -> dict:
    """东财行业（一级）：一只股票一行，l2/l3 留空。"""
    ak = _ak()
    run_start = datetime.now(UTC)
    boards = _board_list(ak, "industry")
    if limit > 0:
        boards = boards[:limit]
    if not boards:
        raise RuntimeError("东财行业板块列表为空，终止同步")

    known = _known_codes(store)
    rows: list[tuple[str, str]] = []
    failed = 0
    for index, (name, _code) in enumerate(boards, 1):
        _throttle()
        try:
            members = _board_members(ak, "industry", name)
        except Exception as exc:  # noqa: BLE001 - 单个板块失败不拖垮整轮
            failed += 1
            logger.warning("行业板块取数失败(%s)：%s", name, exc)
            continue
        rows.extend((code, name) for code in members if code in known)
        if index % 20 == 0:
            logger.info("行业进度 %d/%d", index, len(boards))

    deleted = _replace_industry(store, rows, run_start, failed)
    logger.info("行业同步完成：板块 %d，成分 %d，失败 %d，清理旧行 %d",
                len(boards), len(rows), failed, deleted)
    return {"boards": len(boards), "rows": len(rows), "failed": failed, "deleted": deleted}


def _replace_industry(store, rows: list[tuple[str, str]], run_start, failed: int) -> int:
    with store.conn.cursor() as cur:
        cur.executemany(
            """
            INSERT INTO stock_industry (code, std, l1, updated_at)
            VALUES (%s, 'em', %s, now())
            ON CONFLICT (code, std) DO UPDATE
               SET l1 = EXCLUDED.l1, l2 = NULL, l3 = NULL, updated_at = now()
            """,
            rows,
        )
        # 有板块失败时不清旧行：那一批成分本轮没写进来，删了等于凭空丢数据
        if failed:
            logger.warning("本轮有 %d 个板块失败，跳过旧行清理", failed)
            store.conn.commit()
            return 0
        cur.execute(
            "DELETE FROM stock_industry WHERE std = 'em' AND updated_at < %s",
            (run_start,),
        )
        deleted = cur.rowcount or 0
    store.conn.commit()
    return deleted


def sync_concept(store, limit: int = 0) -> dict:
    """东财概念板块：多对多，一只股票可挂多个概念。"""
    ak = _ak()
    run_start = datetime.now(UTC)
    boards = _board_list(ak, "concept")
    if limit > 0:
        boards = boards[:limit]
    if not boards:
        raise RuntimeError("东财概念板块列表为空，终止同步")

    known = _known_codes(store)
    rows: list[tuple[str, str, str]] = []
    failed = 0
    for index, (name, board_code) in enumerate(boards, 1):
        _throttle()
        try:
            members = _board_members(ak, "concept", name)
        except Exception as exc:  # noqa: BLE001
            failed += 1
            logger.warning("概念板块取数失败(%s)：%s", name, exc)
            continue
        rows.extend((name, code, board_code) for code in members if code in known)
        if index % 50 == 0:
            logger.info("概念进度 %d/%d", index, len(boards))

    deleted = _replace_concept(store, rows, run_start, failed)
    logger.info("概念同步完成：板块 %d，成分 %d，失败 %d，清理旧行 %d",
                len(boards), len(rows), failed, deleted)
    return {"boards": len(boards), "rows": len(rows), "failed": failed, "deleted": deleted}


def _replace_concept(store, rows: list[tuple[str, str, str]], run_start, failed: int) -> int:
    with store.conn.cursor() as cur:
        cur.executemany(
            """
            INSERT INTO stock_concept (concept, code, board_code, source, updated_at)
            VALUES (%s, %s, %s, 'em', now())
            ON CONFLICT (concept, code) DO UPDATE
               SET board_code = EXCLUDED.board_code, updated_at = now()
            """,
            rows,
        )
        if failed:
            logger.warning("本轮有 %d 个板块失败，跳过旧行清理", failed)
            store.conn.commit()
            return 0
        cur.execute(
            "DELETE FROM stock_concept WHERE source = 'em' AND updated_at < %s",
            (run_start,),
        )
        deleted = cur.rowcount or 0
    store.conn.commit()
    return deleted


def sync_master(store, only: list[str] | None = None,
                concept_limit: int = 0, industry_limit: int = 0) -> dict:
    """按需同步三类主数据；顺序固定（行业/概念依赖 stock_basic 的外键）。"""
    ensure_master_schema(store)
    wanted = [x for x in (only or ["basic", "industry", "concept"])]
    stats: dict = {}
    if "basic" in wanted:
        stats["basic"] = sync_basic(store)
    if "industry" in wanted:
        stats["industry"] = sync_industry(store, limit=industry_limit)
    if "concept" in wanted:
        stats["concept"] = sync_concept(store, limit=concept_limit)
    return stats
