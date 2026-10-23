package quote

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"stock-agent-server/internal/httpclient"
)

// smartBoxURL 腾讯 smartbox 搜索接口
const smartBoxURL = "https://smartbox.gtimg.cn/s3/"

// maxBodyBytes 第三方响应体读取上限，与行情接口一致
const maxSearchBodyBytes = 1 << 20

// unicodeEscape 匹配腾讯返回的 \uXXXX 转义序列
var unicodeEscape = regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)

// varAssignRe 匹配 `v_hint="..."` 这类赋值片段。
// 用正则而不是 Index(`="`) + LastIndex(`"`)：后者在响应里出现多余引号时
// 会截错范围
var varAssignRe = regexp.MustCompile(`v_[a-z0-9]+="([^"]*)"`)

// searchHTTP 复用连接（与行情接口分开，因为超时不同，见 quote.Init）。
// 搜索是高频操作，若每次都新建 client，会重复走 DNS + TLS 握手，
// 首次请求特别慢，正是之前偶发超时的根源。
// 超时比行情的 3 秒略宽：首次请求要额外算上 DNS + TLS 握手，
// 搜索是体验加分项，宁可多等 2 秒，也不要因为假超时返回空列表
var searchHTTP = httpclient.New(5 * time.Second)

// SearchResult 单条股票搜索结果。
// 只给前端真正需要的字段：这是哪只股票 + 该用哪个交易所前缀。
// 带上 market 是为了让前端不必再用代码前缀反推交易所
type SearchResult struct {
	Code   string `json:"code"`   // 6 位代码
	Market string `json:"market"` // SH / SZ / BJ
	Name   string `json:"name"`   // 中文名
}

// Search 模糊搜索股票。
//
// 代理腾讯 smartbox：https://smartbox.gtimg.cn/s3/?q=<query>&t=all
// 支持：代码前缀、汉字名称、拼音缩写（"maotai" / "gzmt" → 贵州茅台）。
// 失败返回 nil，调用方当空列表处理（搜索是体验加分项，挂了不该让主流程报错）
func Search(ctx context.Context, query string, limit int) []SearchResult {
	q := strings.TrimSpace(query)
	if q == "" || limit <= 0 {
		return nil
	}

	// url.Values 编码：中文 / 空格等转成 %XX，否则腾讯可能把请求当乱码拒绝
	params := url.Values{}
	params.Set("q", q)
	params.Set("t", "all")
	searchURL := smartBoxURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		log.Printf("build search request failed: %v", err)
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (stock-agent-miniapp)")

	resp, err := searchHTTP.Do(req)
	if err != nil {
		log.Printf("search request failed: q=%s err=%v", q, err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("search api http %d: q=%s", resp.StatusCode, q)
		return nil
	}

	// 腾讯 smartbox 返回的是 ASCII 文本（中文以 \uXXXX 转义），
	// 与行情接口 qt.gtimg.cn 的 GBK 字节不同，这里不需要字符解码。
	// 读取同样要限长，防止上游返回超大内容
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBodyBytes))
	if err != nil {
		log.Printf("read search body failed: %v", err)
		return nil
	}

	parsed := parseSmartBox(string(body), limit)
	// 无结果时留痕：便于区分「第三方确实没有」与「解析规则不匹配」
	if len(parsed) == 0 {
		log.Printf("search parsed 0 items: q=%s status=%d raw=%.200s", q, resp.StatusCode, string(body))
	}
	return parsed
}

// parseSmartBox 解析腾讯 smartbox 返回。
//
// 样本形如：
//
//	v_hint="sh~600600~\u9752\u5c9b\u5564\u9152~qdpj~GP-A^sz~000600~...~jtny~GP-A^hk~...~GP^jj~...~KJ-HB";
//
// 按 ^ 分割多条，每条按 ~ 分割字段：
//
//	[0] market  市场小写前缀：sh / sz / bj / hk / jj
//	[1] code    代码（A 股 6 位，港股 5 位）
//	[2] name    中文名，以 \uXXXX 形式转义
//	[3] pinyin  拼音首字母缩写
//	[4] type    类型-市场：GP-A = A 股股票，GP = 港股，KJ-HB = 基金/货基
//
// 注意三点（与行情接口完全不同，容易踩坑，故用真实样本固化）：
//  1. 字段顺序是 market 打头，不是 code；
//  2. 中文是 Unicode 转义文本，不是 GBK 字节，绝不能套 GBK 解码器；
//  3. 会混入港股(hk)、基金(jj)，必须按 market 过滤，只留 A 股
func parseSmartBox(body string, limit int) []SearchResult {
	out := make([]SearchResult, 0, limit)

	m := varAssignRe.FindStringSubmatch(body)
	if m == nil {
		return out
	}
	hint := m[1]

	for _, line := range strings.Split(hint, "^") {
		if len(out) >= limit {
			break
		}
		fields := strings.Split(line, "~")
		if len(fields) < 3 {
			continue
		}

		market := strings.ToLower(strings.TrimSpace(fields[0]))
		code := strings.TrimSpace(fields[1])
		name := decodeUnicodeEscape(strings.TrimSpace(fields[2]))

		// 只要 A 股三个市场，过滤掉港股 / 基金 / 债券等
		if market != "sh" && market != "sz" && market != "bj" {
			continue
		}
		// A 股代码固定 6 位数字
		if len(code) != 6 || !isAllDigits(code) {
			continue
		}
		// type 存在时必须是股票（GP / GP-A），排除基金 KJ-* 等
		if len(fields) >= 5 {
			tp := strings.ToUpper(strings.TrimSpace(fields[4]))
			if tp != "" && !strings.HasPrefix(tp, "GP") {
				continue
			}
		}
		if name == "" {
			name = code
		}

		out = append(out, SearchResult{
			Code:   code,
			Market: strings.ToUpper(market),
			Name:   name,
		})
	}
	return out
}

// decodeUnicodeEscape 把 \uXXXX 还原成真实字符。
// 腾讯把中文以转义形式放在 ASCII 响应里，逐段替换即可。
// 代理对（\ud83d\ude00 这类 emoji）必须先合并高低位再转 rune，
// 单个解出来是无效码元、会变成替换字符
func decodeUnicodeEscape(s string) string {
	locs := unicodeEscape.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	pos := 0
	for i := 0; i < len(locs); i++ {
		start, end := locs[i][0], locs[i][1]
		b.WriteString(s[pos:start])
		esc := s[start:end]

		r, err := decodeEscRune(esc)
		if err != nil {
			b.WriteString(esc) // 解不开就原样保留，至少不丢内容
			pos = end
			continue
		}
		if utf16.IsSurrogate(r) {
			// 高代理：与紧邻的下一个转义拼成代理对（emoji 等）
			if r <= 0xDBFF && i+1 < len(locs) && locs[i+1][0] == end {
				low, err2 := decodeEscRune(s[locs[i+1][0]:locs[i+1][1]])
				if err2 == nil && low >= 0xDC00 && low <= 0xDFFF {
					b.WriteRune(utf16.DecodeRune(r, low))
					pos = locs[i+1][1]
					i++ // 低代理已消费
					continue
				}
			}
			pos = end // 拼不上的孤立代理元，直接丢弃
			continue
		}
		b.WriteRune(r)
		pos = end
	}
	b.WriteString(s[pos:])
	return b.String()
}

// decodeEscRune 解单个 \uXXXX 转义成码元
func decodeEscRune(esc string) (rune, error) {
	n, err := strconv.ParseUint(esc[2:], 16, 32)
	if err != nil {
		return 0, err
	}
	return rune(n), nil
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
