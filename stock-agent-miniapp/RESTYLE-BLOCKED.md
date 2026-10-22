# RESTYLE-BLOCKED.md — 纯样式改不动的阻塞项

> 军规第 7 条：需要改逻辑 / 结构 / JS 才能实现的视觉效果，一律跳过并写在这里，
> 禁止硬闯。本文件由阶段 0 建立，后续阶段持续追加。

## 阶段 0 识别（第 1~6 项已于当日获用户授权并完成）

> **2026-10-22 状态：上表 6 项全部已同步完成**（用户授权破例改 JS / 配置）。
> 已改：`utils/color.js` 取色表 + `TREND_COLORS`；`pages/kline/index.js` 与
> `components/finance/kline-card/kline-card.js` 的 grid/axis/up/down；
> `components/chart/sparkline/sparkline.js` 默认涨色；`components/base/empty/empty.js`
> 空状态图标灰；`app.json` + chat/alerts/settings 三个页面 json 的窗口底色 → `#ffffff`。
> 保留未动：均线三色（ma5 #2559EB / ma10 #F5A623 / ma20 #8B5CF6），属数据系列标识色，
> 不属于 UI 强调色，收敛会降低可读性。
> **后续若要再改 token 色值，以上这些文件必须同步，不能只改 wxss。**


| # | 位置 | 期望效果 | 所需改动 | 为什么阻塞 |
|---|---|---|---|---|
| 1 | `utils/color.js`（15 处 hex） | SVG data-URI 图标与迷你走势线跟随新 shadcn 色板 | 改 JS 常量表（`COLOR_VARS` / `TREND_COLORS`） | 文件是 `.js`，不在白名单；但**这是最高优先级项**——不改动则页面上所有 t-icon 时代的 SVG 图标仍是旧色（#181A1C / #F04A4A / #E8EBF0），与新 token 并列出现会明显脱色 |
| 2 | `pages/kline/index.js`（8 处） | K 线蜡烛图 / 均线 / 网格跟随新色 | 改 uCharts 配色常量 | canvas 不吃 CSS 变量，配色在 JS 里另有硬编码 |
| 3 | `components/finance/kline-card/kline-card.js`（4 处） | 卡片内迷你 K 线同上 | 同上 | 同上 |
| 4 | `components/chart/sparkline/sparkline.js`（1 处） | 走势线同上 | 同上 | 同上 |
| 5 | `components/base/empty/empty.js`（1 处） | 空状态图标同上 | 图标 SVG 颜色常量 | 同上 |
| 6 | `app.json` + 4 个页面 `.json` 的 window 底色（#F5F6F8） | 下拉/窗口背景与 `--bg:#ffffff` 一致 | 改配置字段 | 属配置非逻辑，但白名单只放开 `app.json` 的 tabBar 颜色字段，需授权 |

## 阶段 1A 追加（需改 wxml / 组件配置，详见 LAYOUT-ISSUES.md）

| # | 位置 | 期望效果 | 所需改动 | 阻塞原因 |
|---|---|---|---|---|
| 7 | `sparkline.js:70` + `quote-card.wxml:26` / `fundflow-card.wxml:30` | 外部类生效，让 `.spark`(88rpx) 与 `.flow-spark`(64rpx+24rpx 上间距) 真正控制住迷你走势线尺寸 | wxml 改传 `spark-class="spark"` / `spark-class="flow-spark"`（组件只声明了 `externalClasses: ['spark-class']`，写 `class=` 不生效） | 改 `.wxml` 属性，不在白名单 |
| 8 | `navbar.wxml:19` 内联 `max-width` + `navbar.js` 的 `titleMaxWidth` / `statusBarHeight` | 标题视口居中（当前被钉在左边界，见 LAYOUT-ISSUES P0 根因①） | 纯 CSS 版：`.nav-title` 改 `left:50%`+`translateX(-50%)`（白名单内，1B 可做）；彻底版需移除内联 `max-width` | 彻底版要改 wxml+js |

**建议**：第 8 项 1B 先只做 CSS 版（不改结构即可覆盖 5 个页面），效果确认后再决定是否动 wxml。
第 7 项体量极小但收益明确，若同意可并入 1B 一次性处理。

> 样式隔离类需求（组件内想用 `.flex-center` / `.num` 等全局工具类）：
> `_card.wxss` 的 `class="num"` 在 7 张卡共 33 处失效（全局类不透传），
> 在组件自身 wxss 内复写即可，属白名单内能解决的，不在此列。

## 阶段 1B 追加（确认不可行，需结构改动）

| # | 位置 | 期望效果 | 所需改动 | 阻塞原因 |
|---|---|---|---|---|
| 9 | `settings.wxss` `.pref-switch` | 开关热区 ≥88rpx | 原生 switch 组件高度固定 56rpx，`transform:scale()` 只改视觉不改热区 | 需在 wxml 外层包可点容器，属结构改动 |
| 10 | `alerts.wxss` `.input-unit` | 用 flex 居中替代 `absolute`+`translateY(-50%)` | 需改 wxml 结构 | P2，当前渲染正常，暂缓 |
| 11 | `_card.wxss` `.fgrid-row` 列数 3 vs 4 | 栅格列数节奏统一 | 各卡 wxml 的 `.fcell` 数量不同 | 需改 wxml |
| 12 | `navbar.wxml` 内联 `max-width` + `navbar.js` | 标题视口居中（彻底版） | CSS 版已在 1B 修复（`.nav-title` 改 `left:50%`+`translateX(-50%)`），彻底版需移除内联 `max-width` | 需改 wxml+js，待真机确认 CSS 版效果后再定 |

## 历史裁决记录

- 阶段 0 的 6 项：用户已授权并全部完成（详见文件开头状态说明）。
- 阶段 1A 的第 7、8 项：第 8 项的 CSS 版已随 1B 完成；第 7 项仍未处理（等授权）。

## 建议裁决（等用户拍板）

- **建议一次性授权** 上述 6 项中的第 1、6 项：它们零逻辑风险（常量表替换 + 配置改值），
  但收益最大 —— SVG 图标与窗口底色是「一眼看得出的新旧混用」。
- 第 2~5 项属 canvas 主题同步，建议与 `#1` 同批处理，否则「页面换肤、图表没换」。
- 样式隔离类需求（组件内想用 `.flex-center` 等全局工具类）暂不需要，
  阶段 1A 巡检后若出现，追加到本文件。
