#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""AI 对话故障的回归测试（纯函数，不依赖 LLM / Tushare / 网络）。

覆盖 2026-10-22 实测发现的三个问题：
  P1 上下文溢出（301046 token > max_seq_len）导致整条回答为空
  P2 取数类接口返回整段历史，把对话上下文撑爆
  P3 无数据兜底靠关键词判定，漏判后模型自己编失败原因

用法：python tests/test_regression.py
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from langchain_core.messages import AIMessage, HumanMessage, SystemMessage, ToolMessage

from config import MAX_INPUT_TOKENS
from llm import count_message_tokens, is_context_overflow
from subgraphs.mcp import (
    _DATA_ABBR_RE,
    _OVERFLOW_REPLY,
    _asked_for_data,
    _call_rounds,
    _failure_reason,
    _fit_context,
    _got_data,
    _is_concept_question,
    _needs_data_fetch,
    _no_data_fallback,
    _normalize_tool_call,
    _parse_text_tool_call,
    _pending_nudge,
    _recent_history,
    route_after_call,
)
from tools import take_latest

_FAILED = []


def check(name, cond, detail=""):
    if cond:
        print(f"  PASS  {name}")
    else:
        print(f"  FAIL  {name}  {detail}")
        _FAILED.append(name)


def _tool_results(name, ok, error=None, data=None):
    res = {"ok": ok}
    if not ok and error:
        res["error"] = error
    if data is not None:
        res["data"] = data
    return [{"tool": name, "result": res}]


# ===== P1：上下文溢出 =====
def test_overflow_detection():
    print("\n[P1] 上下文溢出识别")
    real = ("OpenAIInvalidRequestError: Error code: 400 - {'code': 20015, 'message': "
            "'number of input tokens (301046) has exceeded max_seq_len (163840) limit.'}")
    check("识别真实上游报错", is_context_overflow(Exception(real)))
    check("普通异常不误判", not is_context_overflow(Exception("connection reset")))
    check("兜底文案非空", bool(_OVERFLOW_REPLY.strip()))


def test_fit_context():
    print("\n[P1] 上下文预算裁剪")
    huge = "x" * 600_000
    msgs = [
        SystemMessage(content=huge),
        HumanMessage(content="上一轮问题"),
        AIMessage(content="上一轮回答"),
        HumanMessage(content="本轮问题"),
    ]
    out = _fit_context(msgs)
    n = count_message_tokens(out)
    check(f"裁剪后 token <= 预算（{n} <= {MAX_INPUT_TOKENS}）", n <= MAX_INPUT_TOKENS)
    check("保留了本轮问题",
          any(isinstance(m, HumanMessage) and m.content == "本轮问题" for m in out))
    check("原列表未被就地修改", msgs[0].content == huge)


def test_recent_history_drops_tool_messages():
    print("\n[P1] 工具结果不进 LLM 上下文")
    big = '{"ok": true, "data": [' + ",".join(['{"pe": 1.2}'] * 5000) + ']}'
    state = {
        "query": "贵州茅台的PE是多少",
        "messages": [
            HumanMessage(content="贵州茅台的PE是多少"),
            AIMessage(content="", tool_calls=[{"name": "get_valuation", "args": {}, "id": "1"}]),
            ToolMessage(content=big, tool_call_id="1", name="get_valuation"),
            AIMessage(content="我查一下"),
        ],
    }
    hist = _recent_history(state)
    check("不含 ToolMessage", not any(isinstance(m, ToolMessage) for m in hist))
    check("不含带 tool_calls 的 AIMessage",
          not any(getattr(m, "tool_calls", None) for m in hist))
    check("不含本轮问题（由 synthesize 显式追加）",
          not any(isinstance(m, HumanMessage) and m.content == state["query"] for m in hist))
    total = sum(len(m.content if isinstance(m.content, str) else "") for m in hist)
    check(f"历史体积可控（{total} 字符）", total < 5000)


# ===== P2：取数接口返回整段历史 =====
def test_take_latest():
    print("\n[P2] 取数结果只保留最近 N 条")
    rows = [
        {"trade_date": "20260101", "pe": 1},
        {"trade_date": "20261020", "pe": 2},
        {"trade_date": "20260901", "pe": 3},
    ]
    out = take_latest(rows, "trade_date", 2)
    check("按日期倒序", [r["trade_date"] for r in out] == ["20261020", "20260901"])
    check("不超过 limit", len(out) == 2)
    check("非 list 输入安全", take_latest(None) == [])
    check("缺日期字段不炸", len(take_latest([{"x": 1}], "trade_date", 5)) == 1)


def test_valuation_window():
    print("\n[P2] 估值工具带日期窗口")
    from tools.fundamental import _date_window, _MAX_VALUATION_ROWS
    s, e = _date_window(30)
    check(f"默认窗口为日期区间（{s}~{e}）", len(s) == 8 and len(e) == 8)
    s2, _ = _date_window(99999)
    check("超长区间被夹住", s2 < s, f"{s2} 应早于 {s}")
    check("估值行数上限存在", _MAX_VALUATION_ROWS <= 50)


# ===== P3：无数据兜底 =====
def test_asked_for_data():
    print("\n[P3] 「用户要数据」判定不再漏判缩写")
    st = {"query": "贵州茅台现在的估值水平如何？它当前的PE和PB大概是多少？",
          "rewritten": "贵州茅台的PE和PB是多少？"}
    check("含中文关键词", _asked_for_data(st))
    check("只有英文缩写也判定为要数据",
          _asked_for_data({"query": "它的PE呢", "rewritten": "贵州茅台的PE呢"}))
    check("缩写不误命中普通英文单词", not _DATA_ABBR_RE.search("OPEN TYPE EXPECTED"))
    check("闲聊不算要数据", not _asked_for_data({"query": "你好呀", "rewritten": "你好呀"}))


def test_failure_reason():
    print("\n[P3] 失败原因如实归一化")
    rate = _failure_reason(
        _tool_results("get_valuation", False, "抱歉，您访问接口(daily_basic)频率超限(1次/分钟)"))
    check("限频原因准确", "频率超限" in rate and "每分钟" in rate, rate)
    perm = _failure_reason(
        _tool_results("get_financial_indicator", False, "抱歉，您没有接口(fina_indicator)访问权限"))
    check("权限原因准确", "权限" in perm, perm)
    tmo = _failure_reason(_tool_results("get_quote", False, "timeout"))
    check("超时原因准确", "超时" in tmo, tmo)
    check("无失败时为空",
          _failure_reason(_tool_results("get_quote", True, data=[{}])) == "")


def test_no_data_fallback():
    print("\n[P3] 无数据兜底：命中且不编造")
    st = {"query": "贵州茅台现在的估值水平如何？它当前的PE和PB大概是多少？",
          "rewritten": "贵州茅台的PE和PB是多少？"}
    tr = (_tool_results("symbol_search", True, data=[{"code": "600519"}])
          + _tool_results("get_valuation", False, "频率超限(1次/分钟)"))
    out = _no_data_fallback(st, tr)
    check("触发兜底", bool(out), repr(out))
    check("兜底里带真实原因", "每分钟" in out, out)
    check("兜底不编造「每小时」", "每小时" not in out, out)

    # 有数据拿到时不能兜底（否则会盖掉正常回答）
    tr_ok = _tool_results("get_quote", True, data=[{"price": "1309.69"}])
    check("拿到数据时不兜底", _no_data_fallback(st, tr_ok) == "")

    # 闲聊不兜底
    tr_none = []
    check("闲聊不兜底", _no_data_fallback({"query": "你好", "rewritten": "你好"}, tr_none) == "")

    # 调了取数工具但全失败 -> 一定要兜底（这是实测漏判的路径）
    tr_fail = _tool_results("get_financial_indicator", False, "没有接口访问权限")
    check("取数工具全失败必兜底",
          bool(_no_data_fallback({"query": "看看宁德时代财报", "rewritten": ""}, tr_fail)))


def test_concept_question_not_blocked():
    print("\n[P3] 知识型问题不被兜底误伤")
    # 实测 Q3：问「PE/PB/PS 是什么意思」，模型一个工具都没调，
    # 却被「关键词命中」当成取数问题，兜底文案把一条好答案替换掉了
    st = {"query": "用通俗的语言解释市盈率(PE)、市净率(PB)和市销率(PS)的含义，以及怎么用它们给股票估值。",
          "rewritten": "解释一下市盈率(PE)、市净率(PB)和市销率(PS)的含义"}
    check("识别为概念问题", _is_concept_question(st))
    check("概念问题不兜底", _no_data_fallback(st, []) == "")

    # 概念词 + 缩写同时出现时仍要判为概念问题
    st2 = {"query": "PE 和 PB 有什么区别", "rewritten": ""}
    check("缩写型概念问题", _is_concept_question(st2))
    check("缩写型概念问题不兜底", _no_data_fallback(st2, []) == "")

    # 取数型问题（要具体数值）不能被当成概念问题
    st3 = {"query": "贵州茅台现在的PE和PB是多少", "rewritten": ""}
    check("取数型问题不是概念问题", not _is_concept_question(st3))
    check("取数型问题仍兜底", bool(_no_data_fallback(st3, [])))


def test_data_question_always_fetches():
    print("\n[P3] 要数据的问题一定会去取数（不靠模型自觉）")
    data_q = {"query": "查询贵州茅台（SH600519）的最新行情", "rewritten": ""}
    concept_q = {"query": "解释一下市盈率的含义", "rewritten": ""}

    check("数据问题判定为需要取数", _needs_data_fetch(data_q))
    check("概念问题不需要取数", not _needs_data_fetch(concept_q))

    # 模型一轮没调工具就作答 -> 必须再回 tool_call 一次
    st = {
        "query": data_q["query"], "rewritten": "", "tool_results": [],
        "messages": [HumanMessage(content=data_q["query"]), AIMessage(content="我查一下")],
    }
    check("没调工具时路由回 tool_call", route_after_call(st) == "tool_call")
    check("轮次计数正确", _call_rounds(st) == 1)
    check("取数提示语已生成", "get_quote" in _pending_nudge(st))

    # 第一轮不加提示：实测加了会让模型把「调用」抄进函数名槽位
    st_first = {
        "query": data_q["query"], "rewritten": "", "tool_results": [],
        "messages": [HumanMessage(content=data_q["query"])],
    }
    check("第一轮不追加提示", _pending_nudge(st_first) == "", repr(_pending_nudge(st_first)))
    check("提示语里不出现「调用」二字", "调用" not in _pending_nudge(st))

    # 概念问题不重试，直接合成
    st2 = {
        "query": concept_q["query"], "rewritten": "", "tool_results": [],
        "messages": [HumanMessage(content=concept_q["query"]), AIMessage(content="我来解释")],
    }
    check("概念问题直接合成", route_after_call(st2) == "synthesize")
    check("概念问题不生成取数提示", _pending_nudge(st2) == "")

    # 已经拿到数据 -> 不再重试
    st3 = {
        "query": data_q["query"], "rewritten": "",
        "tool_results": _tool_results("get_quote", True, data=[{"price": "1309.69"}]),
        "messages": [HumanMessage(content=data_q["query"]), AIMessage(content="取到了")],
    }
    check("已取到数据则不重试", route_after_call(st3) == "synthesize")
    check("已取到数据的判定", _got_data(st3))

    # 轮次用尽 -> 合成（不能死循环）
    st4 = {
        "query": data_q["query"], "rewritten": "", "tool_results": [],
        "messages": [HumanMessage(content=data_q["query"])] + [AIMessage(content="x")] * 3,
    }
    check("轮次用尽后合成", route_after_call(st4) == "synthesize")

    # 历史轮次不计入本轮上限（多轮对话不会被提前吃光）
    st5 = {
        "query": data_q["query"], "rewritten": "", "tool_results": [],
        "messages": [AIMessage(content="旧轮次"), AIMessage(content="旧轮次"),
                     HumanMessage(content=data_q["query"]), AIMessage(content="本轮")],
    }
    check("历史轮次不计入", _call_rounds(st5) == 1)


def test_graph_wiring_and_retry():
    print("\n[P3] 子图接线：重试路由可用（防 KeyError）")
    import asyncio
    from subgraphs import mcp as mcp_mod

    class _FakeLLM:
        """永远不调工具的假模型：逼出「模型没调工具」这条分支。"""

        def __init__(self):
            self.calls = 0

        def bind_tools(self, tools):
            return self

        async def ainvoke(self, msgs):
            self.calls += 1
            return AIMessage(content=f"第{self.calls}轮：我先想想")

    fake = _FakeLLM()
    orig = mcp_mod.get_llm
    mcp_mod.get_llm = lambda **kw: fake
    try:
        g = mcp_mod.build_mcp_subgraph()
        q = "查询贵州茅台（SH600519）的最新行情"
        state = {
            "messages": [HumanMessage(content=q)],
            "query": q,
            "rewritten": "",
            "tool_results": [],
        }
        out = asyncio.run(g.ainvoke(state))
        check("子图能跑完不报 KeyError", True)
        check(f"触发了重试（LLM 被调用 {fake.calls} 次）", fake.calls >= 2, f"calls={fake.calls}")
        check("轮次有上限（不超过 4 次）", fake.calls <= 4, f"calls={fake.calls}")
        check("最终产出了回答", bool(out.get("messages")))
    except Exception as e:  # noqa: BLE001
        check("子图能跑完不报 KeyError", False, f"{type(e).__name__}: {e}")
    finally:
        mcp_mod.get_llm = orig


def test_text_tool_call_restore():
    print("\n[P4] 文本形态的工具调用能被还原成结构化调用")
    txt = 'function\nget_quote\n```json\n{"code":"SH600519"}\n```'
    name, args = _parse_text_tool_call(txt)
    check("解析出工具名", name == "get_quote", str(name))
    check("解析出参数", args == {"code": "SH600519"}, str(args))

    name2, args2 = _parse_text_tool_call('function\nget_money_flow\n{"code":"600519","days":30}')
    check("无 json 围栏也能解析", name2 == "get_money_flow" and args2.get("days") == 30, str(args2))

    name3, _ = _parse_text_tool_call("今天天气不错，我们来聊聊市盈率")
    check("普通文本不误判", name3 is None, str(name3))

    name4, _ = _parse_text_tool_call('function\nnot_a_real_tool\n{"a":1}')
    check("未注册的工具名不认", name4 is None, str(name4))

    # 实测形态：提示里写了「必须调用 get_quote」，模型把「调用」抄进了函数名槽位
    txt2 = 'function调用\n```json\n{"code":"SH600519"}\n```'
    n5, a5 = _parse_text_tool_call(txt2)
    check("函数名被写坏时按参数反推", n5 == "get_quote", str(n5))
    check("反推时参数仍正确", a5 == {"code": "SH600519"}, str(a5))

    n6, a6 = _parse_text_tool_call('function调用\n```json\n{"code":"600519","count":10}\n```')
    check("K 线工具已下线，count 不再反推", n6 is None, str(n6))

    n7, _ = _parse_text_tool_call('```json\n{"query":"贵州茅台"}\n```')
    check("带 query 反推为搜索", n7 == "symbol_search", str(n7))

    out = _normalize_tool_call(AIMessage(content=txt))
    check("归一化后有 tool_calls", len(out.tool_calls) == 1, str(out.tool_calls))
    check("归一化后正文清空", out.content == "")

    ai2 = AIMessage(content="", tool_calls=[{"name": "get_quote", "args": {"code": "1"}, "id": "x"}])
    out2 = _normalize_tool_call(ai2)
    check("已有结构化调用时不改动",
          len(out2.tool_calls) == 1 and out2.tool_calls[0]["id"] == "x")


def main():
    print("=" * 62)
    print("AI 对话故障回归测试")
    print("=" * 62)
    test_overflow_detection()
    test_fit_context()
    test_recent_history_drops_tool_messages()
    test_take_latest()
    test_valuation_window()
    test_asked_for_data()
    test_failure_reason()
    test_no_data_fallback()
    test_concept_question_not_blocked()
    test_data_question_always_fetches()
    test_graph_wiring_and_retry()
    test_text_tool_call_restore()
    print("\n" + "=" * 62)
    if _FAILED:
        print(f"FAILED: {len(_FAILED)} 项 -> {_FAILED}")
        return 1
    print("ALL PASSED")
    return 0


if __name__ == "__main__":
    sys.exit(main())
