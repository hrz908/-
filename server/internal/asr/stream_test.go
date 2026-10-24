package asr

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/url"
	"testing"
	"time"
)

func TestBuildStreamURL(t *testing.T) {
	now := time.Unix(1700000000, 0)
	rawURL := buildStreamURL("125922", "sid", "skey", "16k_zh", "vid", now, 42)

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if u.Scheme != "wss" || u.Host != "asr.cloud.tencent.com" || u.Path != "/asr/v2/125922" {
		t.Fatalf("unexpected url: %s", rawURL)
	}

	q := u.Query()
	want := map[string]string{
		"secretid":          "sid",
		"timestamp":         "1700000000",
		"expired":           "1700003600",
		"nonce":             "42",
		"engine_model_type": "16k_zh",
		"voice_id":          "vid",
		"voice_format":      "8",
		"needvad":           "1",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("param %s = %q, want %q", k, got, v)
		}
	}

	// 签名原文必须按参数名升序拼接，且不包含协议部分
	raw := "asr.cloud.tencent.com/asr/v2/125922?" +
		"engine_model_type=16k_zh&expired=1700003600&needvad=1&nonce=42&secretid=sid&timestamp=1700000000" +
		"&voice_format=8&voice_id=vid"
	mac := hmac.New(sha1.New, []byte("skey"))
	mac.Write([]byte(raw))
	wantSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got := q.Get("signature"); got != wantSig {
		t.Errorf("signature = %q, want %q", got, wantSig)
	}
}

func TestTranscript(t *testing.T) {
	tr := &transcript{finals: map[int]string{}}

	tr.apply(&tencentSentence{SliceType: 1, Index: 0, VoiceTextStr: "你好"})
	if got := tr.text(); got != "你好" {
		t.Fatalf("partial = %q, want 你好", got)
	}

	tr.apply(&tencentSentence{SliceType: 2, Index: 0, VoiceTextStr: "你好。"})
	tr.apply(&tencentSentence{SliceType: 1, Index: 1, VoiceTextStr: "世界"})
	if got := tr.text(); got != "你好。世界" {
		t.Fatalf("partial with committed = %q, want 你好。世界", got)
	}

	// 中间结果会被覆盖，不会累积重复
	tr.apply(&tencentSentence{SliceType: 1, Index: 1, VoiceTextStr: "世界啊"})
	if got := tr.text(); got != "你好。世界啊" {
		t.Fatalf("overwrite = %q, want 你好。世界啊", got)
	}

	tr.apply(&tencentSentence{SliceType: 2, Index: 1, VoiceTextStr: "世界。"})
	if got := tr.text(); got != "你好。世界。" {
		t.Fatalf("final = %q, want 你好。世界。", got)
	}
}
