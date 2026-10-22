# RESTYLE-SUMMARY.md — shadcn 重构总结

> 分支 `style/shadcn`（基线 8360881）｜ 2026-10-22 完成阶段 0~4
> 视觉规格唯一真相源：`DESIGN.md`；执行军规：`.codebuddy/rules/ui-restyle.md`

## 一、五阶段执行记录

| 阶段 | 内容 | 产物 |
|---|---|---|
| 0 基建 | token 就地升级为 shadcn zinc 色板 + 排版工具类；军规落盘；JS/配置侧配色同步 | `DESIGN.md`、`styles/token.wxss`、`app.wxss`、`.codebuddy/rules/ui-restyle.md` |
| 1A 巡检 | 5 页 + 15 组件对照排版八条，产出 91 条问题（P0 去重后 2 个根因）+ 4 项渲染缺陷 | `LAYOUT-ISSUES.md` |
| 1B 修复 | 修 88/91 条；3 条入 BLOCKED | 见 `LAYOUT-ISSUES.md` 第〇节 |
| 2 试点 | settings 页配色改造：底部按钮并入 shadcn 主按钮范式 | `pages/settings/settings.wxss` |
| 3 批量 | watchlist/alerts 主按钮蓝→近黑；navbar px→rpx；标签 padding、圆角、胶囊收口 token | 各页 wxss + `token.wxss` |
| 4 收尾 | 全局扫遗留清零；本文档 | 本文件 |

## 二、视觉变化总览

- 页面底 `#F5F6F8` 浅灰 → **纯白 #ffffff**；卡片去投影（`--shadow-card: none`），层次靠发丝边框
- 主按钮：主行动蓝 `#2559EB` → **近黑 #09090b + 白字**；蓝色降级为链接/选中态/焦点专用
- 涨跌色 `#F04A4A/#1AA97C` → `#dc2626/#16a34a`（红涨绿跌语义不变）
- 圆角 32rpx → 16rpx（标签 12→8rpx）；新增 `--r-pill` 胶囊档
- 边框统一 `--hairline`（2rpx），53 处手写 1px/1rpx 收口
- navbar 标题从「贴左」改为**视口绝对居中**（惠及 5 页）
- 8 张金融卡主标题补截断；**卡片内 `.num` 等宽数字复写**（组件隔离导致此前 33 处失效）
- SVG 图标 / K 线 / sparkline 配色同步到新 token（此前与页面脱色）

## 三、截图证据

`.restyle-shots/`（gitignore，本地产物）：

| 文件 | 内容 |
|---|---|
| 01-chat-after-sync / 02-watchlist-after-sync | 阶段 0 配色同步后 |
| 05 / 10 / 11 / 12 / 13-*-1b | 阶段 1B 排版修复后（chat/watchlist/alerts/settings/kline） |
| 20 / 21 / 22 / 23 / 24-*-phase3 | 阶段 2~3 后终态（主按钮近黑、navbar 居中） |

## 四、最终自检（2026-10-22）

- 项目源码 24 个 wxss：**硬编码 hex 清零**（仅存于 `token.wxss` 定义处）
- 168 个 wxss（含 node_modules 产物）括号平衡；24 个项目 wxss 注释闭合、无游离声明
- 非 token 间距/圆角仅剩 6 处，均已加「例外」注释并说明原因
- 旧 token 33 个变量：删除 0 个（无失效引用）
- 五页截图无编译错误条；`mp_screenshot` 通道（weapp-dev MCP，connect 9420）验证可用

## 五、BLOCKED 清单（需改 wxml/js，共 12 项）

见 `RESTYLE-BLOCKED.md`。其中影响最大的四项：

1. `utils/color.js` 等 6 处 JS/配置 —— **已获授权并完成**（阶段 0）
2. `sparkline` 外部类（`spark-class` 未传）—— **已修复**（验收后授权）：quote-card / fundflow-card 两处 wxml 改传 `spark-class=`
3. `navbar.wxml` 内联 `max-width` 彻底版 —— CSS 版已修，待真机确认
4. `settings` 原生 switch 热区 56rpx < 88rpx —— 需包可点容器

## 六、深色模式说明

`token.wxss` 已内置 `page.dark` 全套变量（zinc-800/950 暗色阶），
**但尚未启用**：没有任何页面根节点挂 `theme` class。
启用方式（阶段 4 遗留，需产品决策）：`app.js` 里按系统/用户偏好给每页根节点注入
`class="dark"`，或至少提供手动开关。只切一半比不切更糟，故本轮不启用。

## 七、后续建议（按优先级）

1. **真机验收**：navbar 标题居中、88rpx 热区、长股票名截断 —— 模拟器无法覆盖真机差异
2. 授权处理 BLOCKED 第 3 项（sparkline 外部类，一行 wxml，收益明确）
3. 字号层级是否向 shadcn 规格收敛（`DESIGN.md`「字号」节标注为待定）
4. `miniprogram_npm` / `node_modules` 里的 TDesign 残留包仍在（虽无页面引用），
   可考虑从 `package.json` 移除以缩小主包
