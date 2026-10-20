package asr

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newRouter：只挂 asr.Transcribe 的裸路由。
// handler 不读 user_id，所以这里不需要塞鉴权中间件，测的是转发本身
func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/asr/transcribe", Transcribe)
	return r
}

// postFile 造一个 multipart 上传，字段名与小程序 wx.uploadFile 一致（file）
func postFile(r *gin.Engine, body []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "audio.mp3")
	if err != nil {
		panic(err)
	}
	if _, err := part.Write(body); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/asr/transcribe", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// fakeUpstream：模拟 OpenAI 兼容的 /audio/transcriptions。
// 校验鉴权头与 model 字段，再把收到的音频原样回给断言用
func fakeUpstream(t *testing.T, status int, text string, got *string, gotAuth *string, gotModel *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("upstream path = %s", r.URL.Path)
		}
		*gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(4 << 20); err == nil {
			*gotModel = r.FormValue("model")
			f, _, err := r.FormFile("file")
			if err == nil {
				b := make([]byte, 64)
				n, _ := f.Read(b)
				*got = string(b[:n])
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
	}))
}

func TestTranscribeOK(t *testing.T) {
	var got, auth, model string
	up := fakeUpstream(t, http.StatusOK, "泸州老窖今天的股价", &got, &auth, &model)
	defer up.Close()
	Init(up.URL, "sk-test", "FunAudioLLM/SenseVoiceSmall")

	rec := postFile(newRouter(), []byte("FAKE-MP3-BYTES"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != 0 || body.Data.Text != "泸州老窖今天的股价" {
		t.Fatalf("resp = %s", rec.Body.String())
	}
	// 转发要点：音频内容原样透传、Bearer 鉴权带上、model 字段正确
	if got != "FAKE-MP3-BYTES" {
		t.Errorf("upstream received audio = %q", got)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("upstream auth = %q", auth)
	}
	if model != "FunAudioLLM/SenseVoiceSmall" {
		t.Errorf("upstream model = %q", model)
	}
}

func TestTranscribeUpstreamError(t *testing.T) {
	var got, auth, model string
	up := fakeUpstream(t, http.StatusUnauthorized, "", &got, &auth, &model)
	defer up.Close()
	Init(up.URL, "sk-test", "m")

	// 上游失败要落成 502，不能把上游的报错原文直接透给前端
	if rec := postFile(newRouter(), []byte("x")); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestTranscribeEmptyText(t *testing.T) {
	var got, auth, model string
	up := fakeUpstream(t, http.StatusOK, "   ", &got, &auth, &model)
	defer up.Close()
	Init(up.URL, "sk-test", "m")

	// 空识别结果当失败处理：否则前端会发出一条空消息
	if rec := postFile(newRouter(), []byte("x")); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestTranscribeWithoutKey(t *testing.T) {
	Init("http://127.0.0.1:1", "", "m")
	if rec := postFile(newRouter(), []byte("x")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestTranscribeMissingFile(t *testing.T) {
	Init("http://127.0.0.1:1", "sk-test", "m")
	req := httptest.NewRequest(http.MethodPost, "/asr/transcribe", bytes.NewReader(nil))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=zz")
	rec := httptest.NewRecorder()
	newRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}
