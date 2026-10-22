---
description: 小程序 UI 纯视觉重构规则（shadcn 风），重构进行期间对所有样式改动生效
alwaysApply: true
---

1. 文件白名单：只允许修改 `.wxss`、`styles/token.wxss`、DESIGN.md、
   `app.json` 的 tabBar 颜色字段。禁止改 `.wxml` 结构、class 重命名、
   js/ts 逻辑、事件绑定、data 字段、接口。
2. wxml 唯一例外：页面根节点追加 `class="{{theme}}"`；以及阶段 1 排版修复
   所需的既有节点 class 追加 —— 均仅限追加 class，不许增删改节点。
3. token 收口：颜色 / 字号 / 间距 / 圆角必须 `var(--*)`；出现硬编码 hex、
   rgba 或非五档间距视为违规。
4. 规格唯一性：以 DESIGN.md 为准（shadcn：中性灰、发丝边框、16rpx 圆角、
   默认无阴影）。禁止自创颜色 / 渐变 / 重阴影 / 大圆角 / emoji 图标。
5. 组件不动刀：第三方库（本项目已无 TDesign/Vant，若有同名情况参照）只覆盖
   主题变量；禁止改组件属性、替换组件、引入新 UI 库。
6. 一页一闭环：每页改完必须给出 `git diff --stat` + 自检清单 + 编译通过证据，
   等确认再下一页；一页一 commit `style(页面名): shadcn restyle`。
   **截图通道已恢复**：改完可自行 `mp_ensureConnection` + `mp_screenshot` 截图自检
   （端口 9420 没开就跑 `cli.bat auto --project <工程> --auto-port 9420`，
   或重连 weapp-dev MCP 让它 autoLaunch 自拉）。
   禁止口头声称「已截图验证」却没有真实截图产物；视觉最终确认仍由用户目视。
7. 阻塞上报不硬闯：需改逻辑 / 结构才能实现的视觉效果，跳过并写入
   `RESTYLE-BLOCKED.md`（页面 / 效果 / 所需改动），禁止擅自改逻辑层。

## 本项目增补（原手册七条之外，来自实际踩坑）

8. canvas（uCharts / K 线 / sparkline）配色在各自 js 里另有一份硬编码，
   改主题时**两处都要动**，否则图表与页面脱色。
9. `styles/token.wxss` 的自定义属性对组件透传、**工具类 class 不透传**；
   组件内要用 `.flex-center` 之类的效果，属结构调整 → 走第 7 条上报，不要顺手改组件 json。
10. `--shadow-card` 已置 none。新写卡片不要再加 `box-shadow`；
    确有浮层需求显式写 `var(--shadow-pop)`。
