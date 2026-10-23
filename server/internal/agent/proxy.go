package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"stock-agent-server/internal/httpclient"
)

// AgentProxy 代理到 Python Agent 服务：把一次运行请求转成流式事件序列。
type AgentProxy struct {
	baseURL string
	client  *http.Client
	sem     chan struct{} // 有界信号量，限制打到上游的并发

	// runTimeout 单次运行的整体超时。Agent 推理 + 工具调用可能较久，
	// 默认 120s；到点未结束则取消上游请求，避免 goroutine 与连接长期悬挂。
	runTimeout time.Duration
	// ttftTimeout 首字节（首行 ndjson）等待上限。
	// 超过则先推一个 status 事件安抚前端（「还在思考」），避免界面长时间无反馈像卡死。
	ttftTimeout time.Duration
	// maxLineSize 单行 ndjson 上限。Agent 可能一次性吐出较大的 card/table 结构，
	// 默认 4MB；超出说明上游异常，直接报错而非撑爆内存。
	maxLineSize int
}

// NewAgentProxy 创建代理。
// 不设置 http.Client.Timeout：流式响应的读取时间不可预估，
// 超时统一交给每次请求的 context（runTimeout）控制，避免 Client.Timeout 把长流掐断。
// 连接池用共享 Transport（MaxIdleConnsPerHost=64）：并发打到同一上游，
// 空闲连接要足够复用，否则每次运行都重走 TCP/TLS 握手。
func NewAgentProxy(opts Options) *AgentProxy {
	return &AgentProxy{
		baseURL:     strings.TrimRight(opts.BaseURL, "/"),
		client:      httpclient.New(0),
		sem:         make(chan struct{}, opts.ProxyConcurrency),
		runTimeout:  opts.RunTimeout,
		ttftTimeout: opts.TTFTTimeout,
		maxLineSize: opts.MaxLineBytes,
	}
}

// StreamRun 发起一次流式运行，返回事件通道与错误通道。
// 上游按 ndjson 返回（每行一个 Event JSON），本函数逐行解析后送入 events。
// 两个通道在 goroutine 结束时都会关闭；errs 至多含一个错误值。
// 调用方通过取消 ctx 来提前终止（客户端断开 / 超时），上游请求会随之取消。
func (p *AgentProxy) StreamRun(ctx context.Context, threadID, query, userID string) (<-chan Event, <-chan error) {
	events := make(chan Event, cap(p.sem))
	errs := make(chan error, 1)

	go func() {
		// 关闭顺序（LIFO）：先关 errs 再关 events。
		// 消费方读到 events 关闭后再去取 errs，此时 errs 必已关闭且错误值已就位。
		defer close(events)
		defer close(errs)

		// 占用一个上游并发槽；拿不到槽且 ctx 已取消则直接退出
		select {
		case p.sem <- struct{}{}:
			defer func() { <-p.sem }()
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}

		runCtx, cancel := context.WithTimeout(ctx, p.runTimeout)
		defer cancel()

		payload, err := json.Marshal(map[string]string{
			"query":     query,
			"thread_id": threadID,
			"user_id":   userID,
		})
		if err != nil {
			errs <- err
			return
		}

		req, err := http.NewRequestWithContext(runCtx, http.MethodPost, p.baseURL+"/run/stream", bytes.NewReader(payload))
		if err != nil {
			errs <- err
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/x-ndjson")

		resp, err := p.client.Do(req)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			// 尽量带出上游的错误正文，便于定位；限制读取长度防止被超大响应拖累
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			errs <- fmt.Errorf("agent service returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			return
		}

		p.pumpNDJSON(runCtx, resp.Body, events, errs)
	}()

	return events, errs
}

// pumpNDJSON 逐行读取 ndjson 响应并转发为 Event。
// 扫描放在独立 goroutine，主循环才能同时 select「行数据 / TTFT 定时器 / ctx 取消」，
// 实现「8s 内无首字节先推 status」的监控，而不会被阻塞的 Scan 卡住。
func (p *AgentProxy) pumpNDJSON(runCtx context.Context, body io.Reader, events chan<- Event, errs chan<- error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), p.maxLineSize)

	lines := make(chan []byte)
	scanErr := make(chan error, 1)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			// Scanner.Bytes 复用底层数组，必须拷贝后再投递，否则下一行会覆盖
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-runCtx.Done():
				scanErr <- runCtx.Err()
				return
			}
		}
		scanErr <- scanner.Err()
	}()

	ttft := time.NewTimer(p.ttftTimeout)
	defer ttft.Stop()
	firstReceived := false

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				// 扫描结束：取回扫描错误（EOF 时为 nil，不算错误）
				if err := <-scanErr; err != nil && !errors.Is(err, context.Canceled) {
					errs <- err
				}
				return
			}
			if !firstReceived {
				firstReceived = true
				ttft.Stop() // 首字节已到，撤销 TTFT 提示
			}
			if len(bytes.TrimSpace(line)) == 0 {
				continue // 跳过空行（ndjson 允许行间空行）
			}
			var ev Event
			if err := json.Unmarshal(line, &ev); err != nil {
				// 单行损坏不该中断整个流，记录后跳过，保证健壮性
				log.Printf("agent proxy: malformed ndjson line skipped: %v", err)
				continue
			}
			select {
			case events <- ev:
			case <-runCtx.Done():
				errs <- runCtx.Err()
				return
			}

		case <-ttft.C:
			// 8s 内没有首字节：推一个 status 事件提示前端「仍在处理」。
			// 定时器只触发一次，因此最多发一条，不会刷屏。
			if !firstReceived {
				select {
				case events <- Event{Event: EventStatus, Data: map[string]string{"stage": "thinking", "label": "正在思考…"}}:
				case <-runCtx.Done():
					errs <- runCtx.Err()
					return
				}
			}

		case <-runCtx.Done():
			// 客户端断开或整体超时：上游请求随 runCtx 取消，扫描 goroutine 也会退出
			errs <- runCtx.Err()
			return
		}
	}
}
