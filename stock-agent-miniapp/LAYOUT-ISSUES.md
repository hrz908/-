# LAYOUT-ISSUES.md — 阶段 1A 排版体检清单

## 〇、1B 执行结果（2026-10-22，已修 88 / 91，跳过 3 条入 BLOCKED）

| 批次 | 内容 | 数量 |
|---|---|---|
| 渲染缺陷 | 补 11 个状态语义 token（`--warn*`/`--danger*`/`--accent-deep/soft`/`--press`）；event 圆点改实色蓝；report 条形去渐变改纯色；settings 退出登录按压改白字 | 4 |
| P0 根因① | `navbar` 标题改 `left:50%`+`translateX(-50%)`；`nav-title-text` 补 `min-width:0` | 惠及 5 页 |
| P0 根因② | `_card` 的 `.fcard-name`/`.fcard-code`/`.ftag` 补三件套；`_card` 内复写 `.num`（组件隔离致 33 处失效） | 惠及 8 张卡 |
| P1 热区 | chat bar-btn/bar-hold/bar-send/nav-left、tabs chip、kline-card chip、alerts input-clear/alert-card-edit、watchlist overview-refresh/search-item、settings nickname-link、event-card evt-src、research-card src-link | 12 |
| P1 边框 | 53 处 `1px/1rpx/2rpx solid|dashed` 收口到 `var(--hairline)` / `--hairline-dashed` | 53 |
| P1 截断 | 22 处补 `ellipsis` / `clamp` / `min-width:0`（组件内按隔离规则复写） | 22 |
| P1 数字列 | chat market-val/market-pct、watchlist price-note(200rpx)、kline stat-v(180rpx)、fundflow lhb-vals(320rpx) 定宽右对齐 | 6 |
| P1 行高 | watchlist stock-row-main 112rpx、alerts alert-card / settings pref-row 96rpx、kline stat 改 gap、popup header/body 间距取 max | 5 |
| P2 | 负 margin、重复 text-align、84px 裸值、border-color 硬编码、empty 图标 96rpx+gap、toast/flow-hd/price-row 例外注释、4rpx 引用线注释 | 9 |
| 硬编码清零 | 8 处 `#FFFFFF`/`#ffffff`/`#5B9BEF` 改 token（项目源码 hex 现仅存于 `token.wxss`） | 8 |

**截图证据**（`.restyle-shots/`）：05-chat、10-watchlist、11-alerts、12-settings、13-kline —— 五页均无编译错误条。

**过程中两次自查抓到的回归**（均已修复并复验）：
1. PowerShell 批处理的替换串吞掉分组 `}`，致 5 个文件样式块未闭合 → 已用括号扫描器补回，并对全部 168 个 wxss 复验括号/注释/游离声明三项。
2. `.market-val` 插入时 `.market-pct {` 被吞，产生游离属性 → 已重建该规则块。
3. 附带发现：`node_modules` 与 `miniprogram_npm` 里的 TDesign 产物被行尾归一化扫到（均被 gitignore，无版本库影响）；项目源码 24 个 wxss 已统一 CRLF。

> 下方为 1A 原始清单，保留作为对照。

---

> 巡检日期：2026-10-22 ｜ 范围：4 个主包页 + 1 个分包页 + 16 个组件
> 依据：`DESIGN.md`「排版八条」｜ 状态：**只列不改**，修复在 1B 阶段执行
> 严重度：**P0** 视觉明显错位 ｜ **P1** 间距节奏乱/热区不足/行高不统一 ｜ **P2** 写法不合规但当前渲染暂正常

---

## 一、总览

| 范围 | 条数 | P0 | P1 | P2 |
|---|---|---|---|---|
| pages/chat | 15 | 2 | 7 | 6 |
| pages/watchlist | 10 | 2 | 3 | 5 |
| pages/alerts | 12 | 1 | 7 | 4 |
| pages/settings | 9 | 1 | 4 | 4 |
| pages/kline | 6 | 1 | 3 | 2 |
| components/base | 11 | 0 | 5 | 6 |
| components/finance（含共用 `_card.wxss` 8 条） | 28 | 1 | 12 | 15 |
| components/chart | 0 | 0 | 0 | 0 |
| **合计** | **91** | **8** | **41** | **42** |

### P0 去重后只有 2 个根因

| 根因 | 影响范围 | 说明 |
|---|---|---|
| ① `navbar.wxss:35` `.nav-title` 不是视口居中 | 5 个页面（chat / watchlist / alerts / settings / kline）+ navbar 自身 | `position:absolute; left:0; right:0;` 叠加 wxml 内联 `max-width`，ltr 下 `right` 被忽略 → 标题块被钉在左边界，内部 flex 居中只能相对该块居中，**标题整体偏左**，有返回箭头时更明显 |
| ② `_card.wxss:30` `.fcard-name` 缺截断三件套 | 8 张金融卡 | 卡片主标题（股票名）无 `overflow/ellipsis/nowrap` → 长名换行撑高卡头 |

其余 P0 计数均为这两个根因在不同页面的表现，不重复修。

---

## 二、⚠️ 渲染缺陷（不在八条内，但优先于所有排版项）

已用 grep 确认：这些变量在**全仓库无任何定义处**（`-notfound-defined`），当前就是失效状态。

| 位置 | 引用的未定义变量 | 实际后果 |
|---|---|---|
| `pages/settings/settings.wxss:33,162-168` | `--press` `--danger` `--danger-border` `--danger-tint` `--danger-strong` | 「退出登录」按钮的描边/文字色与按压态全部失效 |
| `components/finance/event-card/event-card.wxss:22,23,27` | `--warn` `--warn-tint` `--accent-soft` | 时间轴圆点与光晕透明 → **圆点不可见** |
| `components/finance/report-card/report-card.wxss:37,41` | `--accent-deep` `--accent-soft` `--warn` `--warn-soft` | 条形图填充透明 → **条形看不见**；且用 `linear-gradient` 违反 DESIGN.md 禁止清单 |

另一条（需改 wxml，已进 `RESTYLE-BLOCKED.md`）：

`sparkline.js:70` 声明 `externalClasses: ['spark-class']`，但
`quote-card.wxml:26` / `fundflow-card.wxml:30` 传的是 `class="spark"` / `class="flow-spark"`
→ 外部类不生效，`.spark`(88rpx 高)、`.flow-spark`(64rpx 高 + 24rpx 上间距) **形同未写**，迷你走势线在两张卡里尺寸失控。

---

## 三、pages/chat

```
components/base/navbar/navbar.wxss:35   | .nav-title            | 第3条 | P0 | left:0;right:0+内联max-width，标题偏左 | 改用 .navbar__title 写法
components/base/navbar/navbar.wxml:19   | .nav-title 内联 max-width | 第3条 | P0 | 宽度按胶囊裁，非视口居中 | max-width 下移到 .nav-title-text
components/base/navbar/navbar.wxss:13   | .navbar-inner         | 第3条 | P2 | height:44px 非 88rpx/env | 现渲染正确，改 88rpx+env 才跨机型等比
components/base/navbar/navbar.wxml:1    | .navbar 内联 padding-top | 第3条 | P2 | 用 JS statusBarHeight 代替 env | 安全区交给 CSS 常量
chat.wxss:691                            | .market-note          | 第5条 | P1 | 指数值无定宽非右对齐 | 用 .list-row__price
chat.wxml:329                            | .market-val           | 第5条 | P1 | 只有 num 无列容器 | 与 .market-pct 同列定宽
chat.wxss:700                            | .market-pct           | 第5条 | P1 | min-width:100rpx 非统一定宽 | 改 .list-row__change(140rpx)
chat.wxss:481                            | .bar-btn              | 第7条 | P1 | 76×76rpx 热区不足 | 补到 88rpx
chat.wxss:521                            | .bar-hold             | 第7条 | P1 | height:76rpx | 同上
chat.wxss:542                            | .bar-send             | 第7条 | P1 | width/height:76rpx | 同上
chat.wxss:15                             | .nav-left             | 第7条 | P1 | 64×64rpx 菜单按钮 | 补到 88rpx
chat.wxss:657                            | .drawer-item-text     | 第4条 | P2 | flex:1 无 min-width:0/ellipsis | 加 .min-w0+.ellipsis
chat.wxss:610,616                        | .drawer-title/-sub    | 第4条 | P2 | 标题类无三件套 | 加 .ellipsis
chat.wxss:685                            | .market-name          | 第4条 | P2 | 名称字段无三件套 | 加 .ellipsis（父需 .min-w0）
chat.wxss:590                            | .drawer-head          | 第6条 | P2 | margin-top:-12rpx 负间距 hack | 用五档间距
chat.wxss:141,191,202,356,434,650,682    | .md-code/.md-table 等 | 第8条 | P2 | 1px/1rpx/2rpx 三套并存 | 统一 var(--hairline)
chat.wxss:676                            | .market-row           | 第8条 | P2 | min-height:88rpx 非行情 112rpx | 改 .list-row(112rpx)
```

**合规**：第 1 条（垂直居中全为 flex+align-items:center，`chat.wxss:516` 的 line-height 是 textarea 打底而非居中手段）、第 2 条（无 `margin:auto`）。

---

## 四、pages/watchlist

```
components/base/navbar/navbar.wxss:35   | .nav-title            | 第3条 | P0 | 同 chat，标题偏左 | 改用 .navbar__title
watchlist.wxml:5                         | base-navbar(left-arrow) | 第3条 | P0 | 有返回键时偏移更明显 | 同上
components/base/navbar/navbar.wxss:13   | .navbar-inner         | 第3条 | P2 | height:44px + JS 安全区 | 改 88rpx+env
watchlist.wxss:165                       | .stock-row-main       | 第8条 | P1 | padding 撑行高≈142rpx，非 112rpx | 改 .list-row 固定 112rpx
watchlist.wxss:98                        | .overview-refresh     | 第7条 | P1 | 整行可点但高≈51rpx | padding 撑到 88rpx
watchlist.wxss:341                       | .search-item          | 第7条 | P1 | padding+字≈74rpx | 补 min-height:88rpx
watchlist.wxss:212                       | .price-note           | 第5条 | P2 | flex:none 无统一定宽 | 改 .list-row__price/__change
watchlist.wxss:217,222                   | .p1 / .p2             | 第5条 | P2 | 与 flex:none 共存无 nowrap | 补 white-space:nowrap
watchlist.wxss:35                        | .overview-title       | 第4条 | P2 | 标题类无三件套 | 加 .min-w0+.ellipsis
watchlist.wxss:353,358                   | .search-item-name/-code | 第4条 | P2 | 无 ellipsis/无 min-width:0 | 加 .ellipsis + nowrap
watchlist.wxss:24,104,153,189,334,346,368 | .overview-card 等     | 第8条 | P2 | 1px/1rpx 混用未用 hairline | 统一 var(--hairline)
```

**合规**：第 1、2、6 条。**正面样例**：`.stock-name`(197) 已带三件套、`.stock-info`(192) 已 `flex:1;min-width:0` —— 这是本项目最规范的行，应作为其余列表的模板。

---

## 五、pages/alerts

```
components/base/navbar/navbar.wxss:35-46 | .nav-title            | 第3条 | P0 | left:0;right:0+max-width:189px，框贴左 | 改用 .navbar__title
alerts.wxss:89-93                        | .alert-card-name      | 第4条 | P1 | 名称无 overflow/ellipsis/nowrap | 加 .ellipsis（父加 .min-w0）
alerts.wxss:298-301                      | .selected-stock-text  | 第4条 | P1 | 名称(代码)无截断 | 加 .ellipsis+.min-w0
alerts.wxss:379-382                      | .search-item-name     | 第4条 | P1 | 下拉名称无截断会换行溢出 | 加 .ellipsis+.min-w0
alerts.wxss:130-135                      | .alert-card-desc      | 第4条 | P2 | 多行未用 line-clamp | 加 .clamp-2
alerts.wxss:253-261                      | .input-unit           | 第1条 | P2 | absolute+translateY(-50%) 居中 | 外层 flex-center 的 88rpx 容器
alerts.wxss:263-274                      | .input-clear          | 第7条 | P1 | 56×56rpx 圆形热区 | 撑到 88rpx
alerts.wxss:103-109                      | .alert-card-edit      | 第7条 | P1 | padding+图标≈51rpx | 补到 88rpx
alerts.wxss:75-80                        | .alert-card           | 第8条 | P1 | 无 min-height，desc 换行行高不一 | 统一 min-height:96rpx
alerts.wxss:30,78,140,248,294,312,360,372 | .overview-cell 等     | 第8条 | P1 | 1px/1rpx 与 hairline(2rpx) 混用 | 统一 var(--hairline)
alerts.wxss:317,328                      | .option-row--hover    | 第8条 | P2 | border-color:#3A4A5A 硬编码 | 回到 token
alerts.wxss:64-65                        | .list-scroll          | 第6条 | P2 | margin-bottom:84px 裸值非五档 | 收敛到 var(--sp-*)
```

**合规**：第 2、5 条（本页无价格列，阈值已挂 `class="num"` 等宽）。

---

## 六、pages/settings

```
components/base/navbar/navbar.wxss:35-46 | .nav-title            | 第3条 | P0 | 标题非视口中心 | 改用 .navbar__title
settings.wxss:137-141                    | .pref-switch          | 第7条 | P1 | scale(0.9) 后高约 56rpx | 去缩放或外扩 padding 至 88rpx
settings.wxss:254-259                    | .nickname-link        | 第7条 | P1 | padding+22rpx 字≈51rpx | min-height:88rpx + .flex-center
settings.wxss:94-99                      | .pref-row             | 第8条 | P1 | 无 min-height，实测约 136rpx 非 96 | min-height:96rpx
settings.wxss:27,49,88,110,163,207,231   | .profile-card 等      | 第8条 | P1 | border:1px solid var(--line) | 改 border:var(--hairline)
settings.wxss:124-128                    | .pref-title           | 第4条 | P2 | 标题缺三件套 | 加 .ellipsis（父 .pref-text 已有 min-w0）
settings.wxss:130-134                    | .pref-desc            | 第4条 | P2 | 说明多行无 clamp | .ellipsis 或 .clamp-2
settings.wxss:94-99 vs 17                | .pref-row vs .content | 第6条 | P2 | 行左右 28rpx ≠ 页边 32rpx | 统一同一档
settings.wxss:86-92                      | .pref-card            | 第8条 | P2 | 多行时 .pref-row 无发丝分割线 | 补 border-bottom:var(--hairline)
```

**合规**：第 1、2、5 条。
**附**：见第二节，`settings.wxss` 有 5 个未定义变量导致退出登录按钮样式失效。

---

## 七、pages/kline

```
components/base/navbar/navbar.wxss:35-46 | .nav-title            | 第3条 | P0 | 标题非视口中心 | 改用 .navbar__title（超长已有 ellipsis）
components/base/tabs/tabs.wxss:14-26     | .chip                 | 第7条 | P1 | height:64rpx < 88rpx（注释自相矛盾） | min-height:88rpx
kline/index.wxss:80-84                   | .stat-v               | 第5条 | P1 | 无定宽无右对齐 | 参照 .list-row__price/__change 定宽右对齐
kline/index.wxss:24,54                   | .chart-card/.stats-card | 第8条 | P1 | border:1px solid var(--line) | 改 var(--hairline)
kline/index.wxss:64-72                   | .stat                 | 第6条 | P2 | padding-right 当间距 | 父级用 gap 并删 last-child 特例
kline/index.wxss:75-84                   | .stat-k/.stat-v       | 第4条 | P2 | 无 nowrap/ellipsis，超长换行撑高 | 加 .ellipsis
```

**合规**：第 1、2 条。

---

## 八、components/base

```
base/empty/empty.wxss:19,25     | .empty-text/-sub            | 第6条 | P2 | 子项互加 margin-top 而非容器 gap | 容器 column+gap，去子项 margin
base/empty/empty.wxss:10-11     | .empty-img                  | 第8条 | P2 | 160rpx 与 DESIGN.md「空状态图标 96rpx」不符 | 收敛到 96rpx
base/navbar/navbar.wxss:35-46   | .nav-title                  | 第3条 | P1 | 非 left:50%+translateX(-50%)，over-constrained 钉在 left:0 | 改用 .navbar__title
base/navbar/navbar.wxss:49-58   | .nav-title-text             | 第4条 | P1 | 三件套齐但父是 flex 且缺 min-width:0 → 截断不生效 | 加 .min-w0
base/navbar/navbar.wxss:13-14   | .navbar-inner / .navbar 内联 | 第3条 | P2 | height:44px、内联 px 高 | 改 88rpx+env()
base/popup/popup.wxss:85-90     | .popup-title                | 第4条 | P1 | 标题无三件套，长标题顶开 dialog | 复写 .ellipsis
base/popup/popup.wxss:82,106    | .popup-header/body-inner    | 第6条 | P1 | header padding-bottom + body padding-top 取了 sum | 只留一处，body 用 gap
base/popup/popup.wxss:37,69,117,134 | .popup-panel 等         | 第8条 | P2 | border:1px solid var(--line) 自写边框 | 统一 var(--hairline)
base/tabs/tabs.wxss:14-26       | .chip                       | 第7条 | P1 | height:64rpx 且无 padding 撑 | min-height:88rpx
base/tabs/tabs.wxss:28-31       | .chip-text                  | 第4条 | P2 | 无 nowrap/ellipsis，长 label 内换行 | 复写 .ellipsis+max-width
base/tabs/tabs.wxss:24          | .chip                       | 第8条 | P2 | border:1px solid var(--line) | 改 var(--hairline)
base/toast/toast.wxss:4-16      | .toast                      | 第2条 | P2 | left:50%+translate 非 flex/text-align 居中 | 定位需要，注释豁免或外层包 .flex-center
```

**完全合规**：`base/icon`（inline-flex 双轴居中，按压态由父级 hover-class 提供）。

---

## 九、components/finance

### 共用外壳 `_card.wxss`（8 张卡各继承一份，不重复计数）

```
_card.wxss:30-35      | .fcard-name        | 第4条 | P0 | 卡主标题无 nowrap/ellipsis，长名换行撑高卡头 | 复写 .ellipsis（影响 8 张卡）
_card.wxss:37-41      | .fcard-code        | 第4条 | P1 | 副标题无三件套 | 复写 .ellipsis
_card.wxss:44-50,53-61 | .fcard-tags/.ftag | 第4条 | P1 | flex:none+nowrap 但无 overflow/ellipsis，长标签溢出卡外 | .ftag 加 max-width+.ellipsis
_card.wxss:4          | class="num"        | 第5条 | P1 | .num 只在全局 base.wxss，组件隔离下不透传 → 7 张卡 33 处失效 | 在 _card.wxss 内复写 .num
_card.wxss:92-99      | .fgrid/--gap       | 第6条 | P2 | flex column 容器却用 row 的 margin-top 拉间距 | 改用 gap
_card.wxss:101-109    | .fcell             | 第6条 | P2 | padding-right 模拟列间距而非父级 gap | .fgrid-row 用 gap
_card.wxss:10,83,126  | .fcard/.fgrid/.fnote | 第8条 | P2 | 1px / 1rpx solid / 1rpx dashed 三种自写边框 | 统一 var(--hairline)，虚线显式豁免
_card.wxss:92-94      | .fgrid-row         | 第8条 | P2 | quote/research 3 列 vs report/kline/valuation 4 列 | 统一列数节奏
```

### 各卡

```
compare-card.wxss:40-46   | .cmp-cell--label | 第4条 | P1 | 128rpx 定宽无 nowrap/ellipsis，长指标名换行致行高不齐 | 复写 .ellipsis
compare-card.wxss:15-27   | .cmp-row/--sep   | 第8条 | P2 | 无统一行高 + 1rpx 边框 | 定 96rpx + var(--hairline)
compare-card.wxss:30-38   | .cmp-cell        | 第4条 | P2 | 数值 cell 无 nowrap，长数值换行破坏右对齐 | 加 nowrap
event-card.wxss:80-84     | .evt-src         | 第7条 | P1 | 20rpx 文字直接 bindtap，热区远小于 88rpx | 外层撑 padding
event-card.wxss:48-58     | .evt-type        | 第6条 | P2 | margin-right 而非 gap | .evt-t 改 inline-flex+gap
fundflow-card.wxss:53-56  | .flow-spark      | 第6条 | P1 | sparkline 外部类是 spark-class，class= 无效 → 高度/间距全丢 | 见 RESTYLE-BLOCKED.md
fundflow-card.wxss:86-90  | .lhb-vals        | 第5条 | P1 | 买/卖两组数字无定宽右对齐，跨行不成列 | 各定宽 + right（参照 .bar-v）
fundflow-card.wxss:30-43  | .flow-item       | 第8条 | P2 | 行高由 padding 决定非 96rpx；1rpx 边框 | 套 .list-row + var(--hairline)
fundflow-card.wxss:4-10   | .flow-hd         | 第1条 | P2 | align-items:baseline 非 center | 如需居中改 .flex-center，基线对齐请注释留存
kline-card.wxss:11-22     | .chip            | 第7条 | P1 | height:56rpx，仅热区下限的 64% | min-height:88rpx
kline-card.wxss:20        | .chip            | 第8条 | P2 | border:1px solid var(--line) | 改 var(--hairline)
quote-card.wxss:27-30     | .spark           | 第6条 | P1 | 同外部类问题，88rpx 高与 24rpx 上间距未生效 | 见 RESTYLE-BLOCKED.md
quote-card.wxss:4-9       | .price-row       | 第1条 | P2 | align-items:baseline 非 center | 同上
report-card.wxss:15-20    | .bar-lb          | 第4条 | P2 | 128rpx 定宽标签无 nowrap/ellipsis | 复写 .ellipsis
research-card.wxss:24-31  | .quote           | 第4条 | P1 | 长摘要无 line-clamp，卡高不可控 | 复写 .clamp-2
research-card.wxss:34-38  | .src-link        | 第7条 | P1 | 20rpx 文字 bindtap，热区 <88rpx | 外层撑 padding
research-card.wxss:4-12   | .rating          | 第4条 | P2 | nowrap 但无 overflow/ellipsis 且 flex:none，过长溢出卡外 | 加 .ellipsis+max-width
research-card.wxss:27     | .quote           | 第8条 | P2 | border-left:4rpx 自写边框 | 对齐 hairline 或显式豁免
valuation-card.wxml:7     | .fcard-code      | 第4条 | P1 | 26 字口径文案≈560rpx 对可用宽≈314rpx，必换行撑高 | 复写 .ellipsis 或下移到 .fnote
valuation-card.wxss:11-16 | .pct-label       | 第4条 | P2 | 96rpx 定宽无 nowrap/ellipsis | 复写 .ellipsis
```

**完全合规**：`chart/sparkline`（纯背景盒，无文字/热区/对齐负担）。

**正面样例（作为 1B 修法的参照）**：`.fcard-title-col` / `.evt-body` / `.lhb-seat` 已 `flex:1 + min-width:0 + 三件套`；`.bar-v`、`report-card.wxss:48-54` 已「定宽 220rpx + text-align:right」；`.bar-row` / `.evt-foot` / `.fcard-hd` / `.tabs-track` 已用 gap；`.nav-btn`(44px=88rpx) 与 `.popup-btn`(96rpx) 热区达标。

---

## 十、1B 修复建议顺序

1. **先修渲染缺陷**（第二节）：未定义变量导致的「元素不可见」比排版错位严重，且改动最小 —— 纯 wxss 常量补全即可。
2. **再修 P0 两个根因**：navbar 标题居中（1 处改动惠及 5 页）+ `.fcard-name` 截断（1 处惠及 8 张卡）。
3. **然后 P1**，按「同类批量」而不是「逐页推进」做：热区 <88rpx（12 处）、边框非 hairline（约 30 处）、缺截断三件套（约 15 处）各自一次性扫完。
4. **P2 最后**，其中 `_card.wxss` 复写 `.num`（组件隔离导致的等宽数字失效）值得提前处理。

> 需要动 wxml/js 的项（sparkline 外部类、navbar 内联最大宽度）不在白名单内，已进 `RESTYLE-BLOCKED.md`。
