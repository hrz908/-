package quote

import (
	"context"
	"testing"
)

// TestParseSmartBox 用腾讯 smartbox 的真实返回验证解析。
//
// 样本形如：
//
//	v_hint="sh~600600~\u9752\u5c9b\u5564\u9152~qdpj~GP-A^sz~000600~...~jtny~GP-A^hk~...~GP^jj~...~KJ-HB";
//
// 关键点（与行情接口 qt.gtimg.cn 完全不同，容易踩坑，故用真实样本固化）：
//  1. 字段顺序是 market / code / name / pinyin / type，不是 code 打头；
//  2. 中文以 \uXXXX 形式转义，整体是 ASCII 文本，不是 GBK 字节，
//     所以绝不能套 GBK 解码器，否则拿不到中文名；
//  3. 会混入港股(hk)、基金(jj) 等，必须按 market 过滤，只留 A 股
func TestParseSmartBox(t *testing.T) {
	raw := `v_hint="sh~600600~\u9752\u5c9b\u5564\u9152~qdpj~GP-A^sz~000600~\u5efa\u6295\u80fd\u6e90~jtny~GP-A^hk~00600~\u7231\u82af\u5143\u667a~axyz~GP^jj~000600~\u6c47\u6dfb\u5bcc\u548c\u805a\u5b9d\u8d27\u5e01A~htfhjbhba~KJ-HB^sh~600519~\u8d35\u5dde\u8305\u53f0~gzmt~GP-A";`

	got := parseSmartBox(raw, 5)

	// 样本共 5 段，含 1 条港股(hk)、1 条基金(jj) → 过滤后应剩 3 条 A 股
	if len(got) != 3 {
		t.Fatalf("期望 3 条 A 股，实际 %d 条：%+v", len(got), got)
	}
	if got[0].Code != "600600" || got[0].Market != "SH" {
		t.Fatalf("第一条解析错误：%+v", got[0])
	}
	if got[0].Name != "青岛啤酒" {
		t.Fatalf("中文名解码失败，实际 %q", got[0].Name)
	}
	for _, r := range got {
		if r.Market != "SH" && r.Market != "SZ" && r.Market != "BJ" {
			t.Fatalf("非 A 股未被过滤：%+v", r)
		}
	}
}

// TestParseSmartBoxEdge 边界输入不应 panic，且都返回空
func TestParseSmartBoxEdge(t *testing.T) {
	cases := []string{"", `v_hint="";`, `v_hint=;`, "garbage"}
	for _, c := range cases {
		if got := parseSmartBox(c, 5); len(got) != 0 {
			t.Fatalf("输入 %q 期望空，实际 %+v", c, got)
		}
	}
}

// TestSearchLive 真实联网验证「请求腾讯 → 解析」整条链路。
// 日常 `go test ./...` 会跳过（依赖外网），要跑真链路上加 -short=false：
//
//	go test ./internal/quote -run TestSearchLive -short=false
func TestSearchLive(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过联网测试：日常跑 go test -short 时忽略，显式加 -short=false 才执行")
	}
	cases := []struct {
		q    string
		want int
	}{
		{q: "600", want: 1},
		{q: "茅台", want: 1},
		{q: "maotai", want: 1},
		{q: "平安", want: 1},
	}
	for _, c := range cases {
		got := Search(context.Background(), c.q, 5)
		t.Logf("q=%-8s -> %d 条: %+v", c.q, len(got), got)
		if len(got) < c.want {
			t.Errorf("q=%s 期望至少 %d 条，实际 %d", c.q, c.want, len(got))
		}
	}
}
