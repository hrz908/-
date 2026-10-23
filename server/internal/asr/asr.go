// Package asr 语音转文字：接收小程序上传的录音，转发给上游 ASR 服务。
//
// 之所以放在网关而不是小程序本地做：微信的「同声传译」插件要在公众平台后台
// 添加、且真机才稳定，本地联调自证不了；而项目已经有一套 OpenAI 兼容的
// 上游（硅基流动），它的 /audio/transcriptions 同样走 Bearer 鉴权，
// 复用现有网关转发即可，密钥也只留在服务端，不会下发到小程序包里。
package asr

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/httpclient"
	"stock-agent-server/internal/resp"
)

const (
	// 录音大小上限：60 秒 mp3 约 1MB，留四倍余量
	maxAudioBytes = 4 << 20
	// 上游返回体的读取上限：识别结果只是一句话，512KB 足够，防止异常响应打爆内存
	maxRespBytes = 1 << 19
)

// upstreamTimeout 上游超时：几十秒的音频识别通常 1~3 秒，20 秒足够兜住网络抖动。
// 用 Client.Timeout 而不是 context，避免整条请求被客户端断开后卡住连接
const upstreamTimeout = 20 * time.Second

var (
	baseURL string
	apiKey  string
	model   string
	// 复用连接池：识别是高频短请求，每次新建连接会白白多一轮 TLS 握手
	client = httpclient.New(upstreamTimeout)
)

// Init 注入上游配置，必须在 router.New 之前调用，
// 否则 handler 里读到的还是空值（与 agent.Init 同样的约束）。
// timeout 可选：不传用 20s 默认档（测试场景传更短值加速失败）
func Init(base, key, m string, timeout ...time.Duration) {
	baseURL = strings.TrimRight(base, "/")
	apiKey = key
	model = m
	if len(timeout) > 0 && timeout[0] > 0 {
		client = httpclient.New(timeout[0])
	}
}

// Transcribe POST /api/v1/asr/transcribe
// 入参：multipart，字段名 file（小程序 wx.uploadFile 的默认字段名）
// 出参：{ code: 0, data: { text: "识别结果" } }
func Transcribe(c *gin.Context) {
	if apiKey == "" {
		// 未配置密钥时明确报错，别让前端拿到一个空的识别结果还以为成功了
		resp.Fail(c, resp.CodeInternal, "服务端未配置 ASR_API_KEY，语音转文字不可用")
		return
	}

	// file.Size 是客户端可控字段，真正限流要靠读的时候不超量
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAudioBytes+1024)
	file, err := c.FormFile("file")
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "缺少录音文件")
		return
	}
	if file.Size > maxAudioBytes {
		resp.Fail(c, resp.CodeBadRequest, "录音过长，请控制在 60 秒内")
		return
	}

	f, err := file.Open()
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "录音读取失败")
		return
	}
	defer f.Close()

	// 重新打包成上游要的 multipart：字段名固定 file / model，
	// 文件名写死 audio.mp3 —— 小程序给的是临时文件名，上游按后缀判断音频格式
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("model", model); err != nil {
		resp.Fail(c, resp.CodeInternal, "请求组装失败")
		return
	}
	part, err := w.CreateFormFile("file", "audio.mp3")
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "请求组装失败")
		return
	}
	if _, err := io.Copy(part, io.LimitReader(f, maxAudioBytes)); err != nil {
		resp.Fail(c, resp.CodeInternal, "请求组装失败")
		return
	}
	if err := w.Close(); err != nil {
		resp.Fail(c, resp.CodeInternal, "请求组装失败")
		return
	}

	req, err := http.NewRequestWithContext(
		c.Request.Context(), http.MethodPost, baseURL+"/audio/transcriptions", &buf)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "请求组装失败")
		return
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)

	up, err := client.Do(req)
	if err != nil {
		log.Printf("asr upstream error: %v", err)
		resp.Fail(c, resp.CodeBadGateway, "语音识别服务不可用")
		return
	}
	defer up.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(up.Body, maxRespBytes))
	if up.StatusCode != http.StatusOK {
		// 上游的报错信息对排查有用，但可能很长，只留前 256 字节
		preview := string(body)
		if len(preview) > 256 {
			preview = preview[:256]
		}
		log.Printf("asr upstream status=%d body=%s", up.StatusCode, preview)
		resp.Fail(c, resp.CodeBadGateway, "语音识别失败，请重试")
		return
	}

	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		log.Printf("asr decode failed: %v", err)
		resp.Fail(c, resp.CodeBadGateway, "语音识别结果解析失败")
		return
	}

	text := strings.TrimSpace(out.Text)
	if text == "" {
		// 空结果不等于成功：让前端提示「没听清」，而不是发出一条空消息
		resp.Fail(c, resp.CodeBadRequest, "没有识别到内容")
		return
	}
	resp.OK(c, gin.H{"text": text})
}
