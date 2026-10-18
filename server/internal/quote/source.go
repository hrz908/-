package quote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// QuoteAPIBase 自建行情服务的基地址（环境变量 QUOTE_API）。
// 为空时走内置的真实行情源（腾讯财经批量接口），开箱即用。
//
// 自建接口约定：GET {base}?code=SH600519
//
//	返回 {"code":"SH600519","name":"贵州茅台","price":"1705.30","change_percent":"1.23"}
//	数值一律用字符串传递，避免中间环节走 float64 丢失精度
var QuoteAPIBase = os.Getenv("QUOTE_API")

const (
	// TencentQuoteURL 腾讯财经批量行情接口。
	// 支持一次查多只（q=sh600519,sz000001），返回 GBK 文本，字段以 ~ 分隔
	TencentQuoteURL = "https://qt.gtimg.cn/q="

	// quoteTimeout 单次 HTTP 超时。行情属于旁路数据，
	// 第三方卡住时不能把刷新接口一起拖死
	quoteTimeout = 3 * time.Second
)

// 腾讯返回串中按 ~ 分隔的字段下标
const (
	tecName    = 1  // 股票名称
	tecCode    = 2  // 6 位代码
	tecPrice   = 3  // 最新价
	tecPercent = 32 // 涨跌幅（%）
)

// quoteHTTP 复用连接：批量刷新时通常是几十个请求打向同一个域名，
// 每次新建 client 会导致连接无法复用、TIME_WAIT 堆积
var quoteHTTP = &http.Client{Timeout: quoteTimeout}

// fetchStockQuote 取一只股票的行情快照（提示词约定的全局函数）
func fetchStockQuote(ctx context.Context, code string) (*StockSnapshot, error) {
	snaps := fetchStockQuotes(ctx, []string{code})
	if len(snaps) == 0 {
		return nil, fmt.Errorf("no quote for %s", code)
	}
	return snaps[0], nil
}

// fetchStockQuotes 批量拉取，返回解析成功的一部分。
// 失败的 code 不返回，由调用方用「原始请求列表 - 成功集合」的差集得到
func fetchStockQuotes(ctx context.Context, codes []string) []*StockSnapshot {
	if len(codes) == 0 {
		return nil
	}
	if QuoteAPIBase != "" {
		return fetchFromCustomAPI(ctx, codes)
	}
	return fetchFromTencent(ctx, codes)
}

// fetchFromTencent 腾讯批量行情：一次请求拿回整批
func fetchFromTencent(ctx context.Context, codes []string) []*StockSnapshot {
	symbols := make([]string, 0, len(codes))
	for _, c := range codes {
		// 腾讯用小写市场前缀：SH600519 → sh600519
		symbols = append(symbols, strings.ToLower(c))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		TencentQuoteURL+strings.Join(symbols, ","), nil)
	if err != nil {
		log.Printf("build quote request failed: %v", err)
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (stock-agent-miniapp)")

	resp, err := quoteHTTP.Do(req)
	if err != nil {
		log.Printf("request quote api failed: %v", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("quote api http %d", resp.StatusCode)
		return nil
	}

	// 腾讯返回 GBK，必须先转 UTF-8，否则中文股票名是乱码
	body, err := io.ReadAll(simplifiedchinese.GBK.NewDecoder().Reader(resp.Body))
	if err != nil {
		log.Printf("decode quote body failed: %v", err)
		return nil
	}
	return parseTencentQuotes(string(body))
}

// parseTencentQuotes 解析腾讯行情文本。
// 每只股票一行，形如：v_sh600519="1~贵州茅台~600519~1705.30~1700.00~...~0.31~...";
//
//	↑[0] ↑[1]     ↑[2]   ↑[3]     ↑[4]              ↑[32]
func parseTencentQuotes(body string) []*StockSnapshot {
	out := make([]*StockSnapshot, 0)

	for _, line := range strings.Split(body, ";") {
		line = strings.TrimSpace(line)
		eq := strings.Index(line, `="`)
		if eq < 0 {
			continue
		}
		end := strings.LastIndex(line, `"`)
		if end <= eq+2 {
			continue
		}

		// 行首形如 v_sh600519，取 _ 之后的部分还原成 SH600519，用于回写 Code
		symbol := strings.ToUpper(line[:eq])
		if i := strings.Index(symbol, "_"); i >= 0 {
			symbol = symbol[i+1:]
		}

		fields := strings.Split(line[eq+2:end], "~")
		if len(fields) <= tecPercent {
			continue
		}

		price, err := decimal.NewFromString(strings.TrimSpace(fields[tecPrice]))
		if err != nil {
			// 停牌 / 退市 / 代码不存在时价格字段为空，按无行情处理
			log.Printf("parse quote price failed: code=%s raw=%q", symbol, fields[tecPrice])
			continue
		}

		pct, err := decimal.NewFromString(strings.TrimSpace(fields[tecPercent]))
		if err != nil {
			pct = decimal.Zero
		}

		name := strings.TrimSpace(fields[tecName])
		if name == "" {
			name = symbol
		}

		out = append(out, &StockSnapshot{
			Code:          symbol,
			Name:          name,
			Price:         price,
			ChangePercent: pct,
		})
	}
	return out
}

// fetchFromCustomAPI 自建行情服务：批内逐只串行请求
func fetchFromCustomAPI(ctx context.Context, codes []string) []*StockSnapshot {
	out := make([]*StockSnapshot, 0, len(codes))
	for _, code := range codes {
		snap, err := requestCustomQuote(ctx, code)
		if err != nil || snap == nil {
			log.Printf("fetch quote %s failed: %v", code, err)
			continue
		}
		out = append(out, snap)
	}
	return out
}

func requestCustomQuote(ctx context.Context, code string) (*StockSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, QuoteAPIBase, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("code", code)
	req.URL.RawQuery = q.Encode()

	resp, err := quoteHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quote api http %d", resp.StatusCode)
	}

	var s StockSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	// 兜底：接口没给名称时用代码顶上，避免出现无名记录
	if strings.TrimSpace(s.Name) == "" {
		s.Name = code
	}
	s.Code = code
	return &s, nil
}
