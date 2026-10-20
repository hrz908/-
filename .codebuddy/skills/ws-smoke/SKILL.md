---
name: ws-smoke
description: 对 AI 聊天链路（小程序 WS 协议）做一轮端到端冒烟测试，检查事件流、路由是否走错分支、回答是否编造
---

# WS 聊天链路冒烟测试

## 何时用

改动了任一项之后，用来确认聊天还通：

- `server/internal/agent/**`（事件、网关代理、限流、WS 生命周期）
- `agent/**`（图路由、prompt、工具调用）
- 小程序 `chat.js` 的消息渲染 / 发送态

## 前置：起环境

```bash
cd server && docker compose up -d postgres redis agent   # 等 healthy
bash scripts/dev.sh dev   # 或：cd server && set -a && . ./.env && set +a && go run .
```

网关在 `127.0.0.1:8080`，agent 在 `127.0.0.1:8000`。

## 步骤

### 1. 建临时测试文件

把下面内容写到 `server/internal/agent/ws_smoke_test.go`（跑完**必须删除**）：

```go
package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func smokeToken(t *testing.T) string {
	resp, err := http.Post("http://127.0.0.1:8080/api/v1/auth/login",
		"application/json", strings.NewReader(`{"code":"smoke"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct{ Data struct{ Token string `json:"token"` } `json:"data"` }
	if err := json.Unmarshal(raw, &out); err != nil || out.Data.Token == "" {
		t.Fatalf("no token: %v / %s", err, string(raw))
	}
	return out.Data.Token
}

func smokeCollect(t *testing.T, c *websocket.Conn, timeout time.Duration) []Event {
	deadline := time.Now().Add(timeout)
	var events []Event
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Logf("collect: 整体超时 %v", timeout)
			break
		}
		_ = c.SetReadDeadline(time.Now().Add(remain))
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Logf("collect: read stop %v", err)
			break
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		events = append(events, ev)
		if ev.Event == EventDone || ev.Event == EventError {
			break
		}
	}
	return events
}

func smokeText(events []Event) string {
	var s string
	for _, ev := range events {
		if ev.Event == EventToken {
			var d struct{ Delta string `json:"delta"` }
			raw, _ := json.Marshal(ev.Data)
			_ = json.Unmarshal(raw, &d)
			s += d.Delta
		}
	}
	return strings.TrimSpace(s)
}

func TestSmokeQueries(t *testing.T) {
	queries := []string{
		"贵州茅台现在多少钱",
		"查询 SH600519 贵州茅台 的实时报价",
		"帮我看看 000568 最近10天日K",
		"你好，你能做什么",
	}
	token := smokeToken(t)
	for i, q := range queries {
		t.Run(q, func(t *testing.T) {
			c, _, err := websocket.DefaultDialer.Dial(
				"ws://127.0.0.1:8080/api/v1/ws/agent?token="+token+
					"&session_id=smoke_"+string(rune('a'+i)), nil)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			start := time.Now()
			if err := c.WriteJSON(map[string]string{"query": q}); err != nil {
				t.Fatalf("send: %v", err)
			}
			events := smokeCollect(t, c, 150*time.Second)
			counts := map[string]int{}
			for _, ev := range events {
				counts[ev.Event]++
			}
			t.Logf("耗时=%v 事件=%v", time.Since(start).Round(time.Millisecond), counts)
			t.Logf("回答: %s", smokeText(events))
			if counts[EventDone] == 0 {
				t.Errorf("没有 done —— 前端 sending 会卡死，之后发不出消息")
			}
			if strings.ContainsAny(q, "钱价K线行情报价") &&
				counts[EventToolStart] == 0 && counts[EventCard] == 0 && counts[EventTable] == 0 {
				t.Errorf("问句明显要行情数据却没调工具 —— 很可能被判成 RAG 分支在编造")
			}
		})
	}
}
```

### 2. 跑

```bash
cd server && go test ./internal/agent/ -run TestSmokeQueries -v -timeout 900s
```

### 3. 判定标准

| 检查项 | 不通过意味着 |
|---|---|
| 每条都有 `done` | 前端 `sending` 卡死，之后所有发送被静默吞掉 |
| 要数据的问句有 `tool_start` / `card` / `table` | 路由判成 RAG 分支，在用无关文档编造 |
| 回答里没有虚构公司名/细节 | RAG 检索被污染（先查 `documents`/`chunks` 表） |
| 耗时 < 120s | 会撞 `runTimeout`，用户看到 `context deadline exceeded` |

### 4. 收尾

**必须删除 `server/internal/agent/ws_smoke_test.go`**，再 `go build ./...` 确认干净。
这个用例依赖在跑的服务，留在仓库里会让 CI 挂掉。

## 补充：直连 agent 看事件时间线（排查慢/卡）

```bash
curl -N -m 150 -X POST http://127.0.0.1:8000/run/stream \
  -H 'Content-Type: application/json' -H 'Accept: application/x-ndjson' \
  -d '{"query":"贵州茅台现在多少钱","thread_id":"smoke:1","user_id":"1"}'
```

拿到的 ndjson 可以直接还原回答全文，用来确认有没有编造数值。
