// 实时语音识别：小程序按住说话时把录音分片（mp3 帧）经 WebSocket 直传网关，
// 网关转发腾讯云实时识别（wss://asr.cloud.tencent.com）并回传中间结果。
//
// 选腾讯云是因为它支持 mp3 编码（voice_format=8），而微信 onFrameRecorded 官方只保证 mp3 分片可用；
// 换成只收 PCM 的上游就得在服务端解码，复杂度与延迟都更高。密钥只留在服务端。
package asr

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"stock-agent-server/internal/resp"
)

const (
	tencentASRHost = "asr.cloud.tencent.com"
	tencentASRPath = "/asr/v2/"

	// 单条连接音频上限：60s mp3（48kbps）约 360KB，留足余量
	streamMaxBytes = 4 << 20
	// 单条连接最长存活时间，与小程序录音上限一致，略留收尾时间
	streamMaxSeconds = 90
	// 单次写超时，防慢客户端挂死转发 goroutine
	streamWriteWait = 5 * time.Second
	// 上游连接建立超时
	streamDialTimeout = 10 * time.Second
)

var (
	streamAppID     string
	streamSecretID  string
	streamSecretKey string
	streamEngine    string

	// 进程内连接闸门：全局并发上限 + 每用户仅一条（一次按住对应一条）
	streamGlobalSem chan struct{}
	streamUserMu    sync.Mutex
	streamUsers     = map[int64]struct{}{}

	streamUpgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// 小程序握手不带 Origin；鉴权已在路由中间件完成
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

// InitStream 注入腾讯云实时识别配置，必须在 router.New 之前调用。
// 未配置凭据时路由返回 503，小程序回退为整段上传识别。
func InitStream(appID, secretID, secretKey, engine string, maxConns int) {
	streamAppID = strings.TrimSpace(appID)
	streamSecretID = strings.TrimSpace(secretID)
	streamSecretKey = strings.TrimSpace(secretKey)
	streamEngine = strings.TrimSpace(engine)
	if streamEngine == "" {
		streamEngine = "16k_zh"
	}
	if maxConns <= 0 {
		maxConns = 64
	}
	streamGlobalSem = make(chan struct{}, maxConns)
}

func streamConfigured() bool {
	return streamAppID != "" && streamSecretID != "" && streamSecretKey != ""
}

// ServeStream 处理 GET /api/v1/ws/asr?token=。
// 上行：二进制帧为 mp3 分片；文本帧 {"type":"end"} 结束、{"type":"cancel"} 取消。
// 下行：{"type":"partial"|"final","text":...} 与 {"type":"error","message":...}。
func ServeStream(c *gin.Context) {
	if !streamConfigured() {
		c.JSON(http.StatusServiceUnavailable,
			resp.Body{Code: http.StatusServiceUnavailable, Message: "服务端未配置实时语音识别（TENCENT_ASR）"})
		return
	}

	userID := c.GetInt64("user_id")

	select {
	case streamGlobalSem <- struct{}{}:
		defer func() { <-streamGlobalSem }()
	default:
		c.JSON(http.StatusServiceUnavailable,
			resp.Body{Code: http.StatusServiceUnavailable, Message: "语音识别繁忙，请稍后再试"})
		return
	}

	streamUserMu.Lock()
	if _, busy := streamUsers[userID]; busy {
		streamUserMu.Unlock()
		c.JSON(http.StatusTooManyRequests,
			resp.Body{Code: http.StatusTooManyRequests, Message: "已有一条语音识别在进行"})
		return
	}
	streamUsers[userID] = struct{}{}
	streamUserMu.Unlock()
	defer func() {
		streamUserMu.Lock()
		delete(streamUsers, userID)
		streamUserMu.Unlock()
	}()

	client, err := streamUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("asr stream upgrade failed: user=%d err=%v", userID, err)
		return
	}
	defer client.Close()

	// 每次连接都用新 voice_id：腾讯云要求断开后不得复用
	voiceID := uuid.NewString()
	upCtx, cancelDial := context.WithTimeout(context.Background(), streamDialTimeout)
	upstream, _, err := websocket.DefaultDialer.DialContext(upCtx,
		buildStreamURL(streamAppID, streamSecretID, streamSecretKey, streamEngine, voiceID, time.Now(), rand.Int63n(1_000_000_000)+1), nil)
	cancelDial()
	if err != nil {
		log.Printf("tencent asr dial failed: %v", err)
		writeStreamError(client, "实时识别服务连接失败")
		return
	}
	defer upstream.Close()

	// 硬时限：无论是否还有数据，连接最多存活 streamMaxSeconds
	_ = client.SetReadDeadline(time.Now().Add(streamMaxSeconds * time.Second))
	_ = upstream.SetReadDeadline(time.Now().Add(streamMaxSeconds * time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 两条泵各自只写一条连接，规避 gorilla「同一连接并发写」限制
	errc := make(chan error, 2)
	go func() { errc <- pumpAudio(ctx, client, upstream) }()
	go func() { errc <- pumpResults(ctx, upstream, client) }()

	err = <-errc
	cancel()
	// 关连接唤醒另一条泵，避免其阻塞在 ReadMessage / WriteMessage
	_ = client.Close()
	_ = upstream.Close()
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("asr stream finished: user=%d err=%v", userID, err)
	}
}

// pumpAudio 把小程序分片原样转发给腾讯云，收到 end 后补发 {"type":"end"}。
func pumpAudio(ctx context.Context, client, upstream *websocket.Conn) error {
	total := 0
	ended := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		mt, data, err := client.ReadMessage()
		if err != nil {
			return err
		}
		switch mt {
		case websocket.BinaryMessage:
			if ended {
				continue
			}
			total += len(data)
			if total > streamMaxBytes {
				writeStreamError(client, "录音过长")
				return errors.New("audio too large")
			}
			_ = upstream.SetWriteDeadline(time.Now().Add(streamWriteWait))
			if err := upstream.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return err
			}
		case websocket.TextMessage:
			var msg struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "end":
				if ended {
					continue
				}
				ended = true
				_ = upstream.SetWriteDeadline(time.Now().Add(streamWriteWait))
				if err := upstream.WriteMessage(websocket.TextMessage, []byte(`{"type":"end"}`)); err != nil {
					return err
				}
			case "cancel":
				return context.Canceled
			}
		}
	}
}

// pumpResults 读腾讯云识别结果，按「已定稿句 + 当前句」拼成完整文本回传。
func pumpResults(ctx context.Context, upstream, client *websocket.Conn) error {
	tr := &transcript{finals: map[int]string{}}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		mt, data, err := upstream.ReadMessage()
		if err != nil {
			// 上游异常断开要告知客户端，让它回退整段识别，别静默丢结果
			writeStreamError(client, "实时识别连接中断")
			return err
		}
		if mt != websocket.TextMessage {
			continue
		}

		var msg tencentResult
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Code != 0 {
			writeStreamError(client, "语音识别服务返回错误")
			return fmt.Errorf("tencent asr code=%d message=%s", msg.Code, msg.Message)
		}

		if msg.Result != nil {
			tr.apply(msg.Result)
			_ = client.SetWriteDeadline(time.Now().Add(streamWriteWait))
			if err := client.WriteJSON(map[string]any{"type": "partial", "text": tr.text()}); err != nil {
				return err
			}
		}

		if msg.Final == 1 {
			_ = client.SetWriteDeadline(time.Now().Add(streamWriteWait))
			_ = client.WriteJSON(map[string]any{"type": "final", "text": tr.text()})
			return nil
		}
	}
}

func writeStreamError(conn *websocket.Conn, message string) {
	_ = conn.SetWriteDeadline(time.Now().Add(streamWriteWait))
	_ = conn.WriteJSON(map[string]any{"type": "error", "message": message})
}

// tencentResult 腾讯云返回体（只取用到的字段）。
type tencentResult struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Final   int              `json:"final"`
	Result  *tencentSentence `json:"result"`
}

type tencentSentence struct {
	SliceType    int    `json:"slice_type"`
	Index        int    `json:"index"`
	VoiceTextStr string `json:"voice_text_str"`
}

// transcript 逐句拼接：slice_type=1 为当前句非稳态结果（会被覆盖），2 为定稿。
type transcript struct {
	finals  map[int]string
	current string
}

func (t *transcript) apply(s *tencentSentence) {
	if s.SliceType == 2 {
		if s.VoiceTextStr != "" {
			t.finals[s.Index] = s.VoiceTextStr
		}
		t.current = ""
		return
	}
	t.current = s.VoiceTextStr
}

func (t *transcript) text() string {
	idx := make([]int, 0, len(t.finals))
	for i := range t.finals {
		idx = append(idx, i)
	}
	sort.Ints(idx)

	var b strings.Builder
	for _, i := range idx {
		b.WriteString(t.finals[i])
	}
	b.WriteString(t.current)
	return b.String()
}

// buildStreamURL 生成带签名的腾讯云实时识别地址：
// 签名原文为 "asr.cloud.tencent.com/asr/v2/<appid>?<除 signature 外参数按字典序拼接>"，
// 再做 HmacSHA1(secretKey) + base64，最终 URL 参数统一编码。
func buildStreamURL(appID, secretID, secretKey, engine, voiceID string, now time.Time, nonce int64) string {
	params := map[string]string{
		"secretid":          secretID,
		"timestamp":         strconv.FormatInt(now.Unix(), 10),
		"expired":           strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
		"nonce":             strconv.FormatInt(nonce, 10),
		"engine_model_type": engine,
		"voice_id":          voiceID,
		"voice_format":      "8", // mp3
		"needvad":           "1",
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	raw := tencentASRHost + tencentASRPath + appID + "?" + strings.Join(pairs, "&")

	mac := hmac.New(sha1.New, []byte(secretKey))
	mac.Write([]byte(raw))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	q.Set("signature", signature)

	u := url.URL{Scheme: "wss", Host: tencentASRHost, Path: tencentASRPath + appID, RawQuery: q.Encode()}
	return u.String()
}
