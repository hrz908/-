---
description: 微信小程序端（stock-agent-miniapp）的踩坑清单，改输入栏、弹窗、手势、WS 消息渲染前必读
globs:
  - stock-agent-miniapp/**/*.js
  - stock-agent-miniapp/**/*.wxml
  - stock-agent-miniapp/**/*.wxss
---

# 小程序端踩过的坑

## 1. `catch` 事件会让子元素点击全部失效

微信的已知行为：**父元素绑定 `catch:touchstart` 后，内部所有子元素的 `bind:tap` 都不再触发**。
本项目踩过：给输入栏容器 `.sender-wrap` 加了 4 个 `catchtouch*` 想拦住手势冒泡，结果 textarea 点不动、
模式键和发送键全失效 —— 表现为"输入框点不了、字打不进去"。

规则：
- 只有**内部没有可点击子元素**的节点才能用 `catch`（如语音态的 `.bar-hold`）
- 容器层要拦手势，改用 `bindtouchstart` + 状态标记（`_inSender`），再在页面的 `onSwipeStart` 里判断后锁掉
- 改完必须回归：输入框能聚焦 / 发送键能点 / 按住说话时右滑不拉出抽屉

## 2. 受控 `focus` 必须镜像"真实焦点"

`t-input` 内部是 `<input focus="{{focus}}">`。把 `focus` 当**命令**用（置 false 去收键盘）会让已聚焦的输入框
立刻失焦 —— 症状是"删除一个字符键盘就收起，没法连续编辑"。

- 正确做法：`focus` 事件置 true、`blur` 事件置 false，变量永远等于真实状态
- 要主动收键盘用 `wx.hideKeyboard()`，它会触发 blur，变量自然归位
- 已聚焦时想重新唤起微信昵称面板，需要"先失焦 → 延迟再聚焦"造一次真实焦点变化

## 3. `type="nickname"` 的生命周期

它一聚焦就弹微信昵称面板，所以只允许存在于「点了使用微信昵称」→「取到值」之间，用完立刻切回 `text`。
残留下来会让用户每次手动点输入框都被面板打断。

## 4. 构建链两步，少一步白屏

1. `npm install`
2. 微信开发者工具 → 工具 → 构建 npm（marked 仍在小程序端使用：`utils/markdown.js`
   走 `miniprogram_npm/marked`；marked@4 是 ES2017 语法，PC 端微信可直跑）

历史包袱已清除：改造前第 3 步 `npm run fix:npm`（用根级 marked@4 覆盖 TDesign 内嵌的
ES2020+ 副本）随 TDesign 移除一并删除，`scripts/fix-marked.js` 已不存在。
PC 端微信仍不支持 ES2020+ 语法，报 `module 'app.js' is not defined` 白屏时，
先查是不是又引入了带现代语法的新依赖。

`miniprogram_npm/` 是构建产物、不入库，换机器必须重走。

## 5. 发送态 `sending` 只有两个复位点

`done` / `error`。任何"等结果"的新入口都必须能走到这两个事件之一，否则 `sending` 永久为 true，
之后每次发送都被 `sendText` 开头那句静默吞掉（表现为"点了发送没反应"）。
现有兜底是 45s 看门狗 `_armSendWatchdog()`，收到第一个 token 即撤销。

## 6. WS 断连不等于这一轮结束

`services/agent.js` 的 `pendingMessages` 只缓存**连接建立前**的消息。回答途中断连：
- 这一轮不会重发（已 send 出去，不在队列里）
- `done`/`error` 永远不来 → `sending` 卡住
- `disconnected` 事件目前只改了 `statusText`，没有复位发送态

## 7. `sources` 事件前端还没接

后端会发引用来源事件，`chat.js` 只监听了 token/card/table/status/tool_start/tool_end/error/done。
回答里的 `[1][6]` 角标目前无处可点。

## 8. 布局常量集中在 `chat.js` 顶部

`SENDER_BAR_RPX` / `SENDER_PAD_TOP_RPX` / `SENDER_BAR_MARGIN_BOTTOM_RPX` / `KEYBOARD_GAP_RPX` 等。
改胶囊尺寸或间距要同步：首帧估算（这几个常量）+ `onReady` 里的 `measureSender()` 实测值。
容器是 `bottom: 0` 底部锚定的，加大 `padding-top` 只会挤短对话区，**不会让胶囊上移**。

## 9. WXML 标签的属性区里不能放注释

`<!-- -->` 只能出现在**标签外**的元素内容区。写进标签的属性列表中间（多行属性之间）会被 WXML 编译器
当成属性文本解析：

```
[transwxmltojs] CustomError: ./pages/chat/chat.wxml:250:11: unexpected character `<`
```

**症状极具迷惑性**：模拟器里 `simulator_open_page` 照样返回 `success: true`、控制台也没有 error，
但**点「预览」/「上传」失败**，开发者工具只弹一个没有信息量的 `错误 undefined (code 10)`
（code 10 = 上传任务执行失败，真实原因被吞掉）。真实报错只在开发者工具日志里：
`%LOCALAPPDATA%\微信开发者工具\User Data\<hash>\WeappLog\logs\*.log`，搜 `transwxmltojs`。

**How to apply**：
- 要解释某个属性，注释放到**标签之前**（作为上一个兄弟节点的注释），别塞进属性列表中间
- 改完 WXML 想确认能上传，**必须走一次真上传/预览**（`create_preview_qrcode` / `upload`）——
  `simulator_open_page` 的 success 不算数（它不校验 WXML 编译错误，也不校验 JS 语法）
- 全量自查：把 wxml 的注释先替换成等长空白，再扫 `<tag ...>` 的属性区里有没有残留 `<!--`
- 同一批改的其它文件要一起查：`textarea` / `switch` / `input` 这类多行属性标签最容易踩
