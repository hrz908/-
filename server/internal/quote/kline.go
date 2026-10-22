package quote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"stock-agent-server/internal/stock"
)

// K 线接口（腾讯 fqkline）相关常量。
// 与批量行情接口分开：行情走 qt.gtimg.cn，K 线走 web.ifzq.gtimg.cn
const (
	// tencentKlineURL 腾讯财经前复权 K 线接口。
	// 形如 ?param=sh600519,day,,,10,qfq
	tencentKlineURL = "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param="

	// klineTimeout 单次 HTTP 超时。K 线是详情页数据，
	// 第三方卡住时不能把接口一起拖死
	klineTimeout = 5 * time.Second

	// klineMaxBodyBytes K 线响应体读取上限。365 根日 K 的 JSON 远小于 4MB，
	// 超出说明上游异常，直接报错而非撑爆内存
	klineMaxBodyBytes = 4 << 20

	// MaxKlineCount 单次最多返回的 K 线根数（约一年半的交易日）
	MaxKlineCount = 365
)

// KlineRow 单根 K 线。数值一律字符串传递（decimal 序列化为字符串），
// 与项目里「数值不走 float64」的约定一致
type KlineRow struct {
	Date          string          `json:"date"`
	Open          decimal.Decimal `json:"open"`
	High          decimal.Decimal `json:"high"`
	Low           decimal.Decimal `json:"low"`
	Close         decimal.Decimal `json:"close"`
	Volume        int64           `json:"volume"` // 成交量（手）
	ChangePercent decimal.Decimal `json:"change_percent"`
}

// QuoteDetail 单只股票的完整行情快照：比 StockSnapshot 多出
// 开高低、涨跌额、量额、换手率、总市值、状态与时间，供行情详情页使用
type QuoteDetail struct {
	Code          string          `json:"code"`
	Name          string          `json:"name"`
	Price         decimal.Decimal `json:"price"`
	Change        decimal.Decimal `json:"change"`
	ChangePercent decimal.Decimal `json:"change_percent"`
	Open          decimal.Decimal `json:"open"`
	High          decimal.Decimal `json:"high"`
	Low           decimal.Decimal `json:"low"`
	Volume        int64           `json:"volume"` // 成交量（手）
	Amount        decimal.Decimal `json:"amount"` // 成交额（万元）
	Turnover      decimal.Decimal `json:"turnover"` // 换手率（%）
	TotalMv       decimal.Decimal `json:"total_mv"` // 总市值（元；上游亿元×1e8）
	Status        string          `json:"status"` // trading / closed
	Time          string          `json:"time"`   // 行情时间（腾讯返回的原始串）
}

// klineHTTP 独立 client：K 线超时（5s）与批量行情（3s）不同，
// 复用连接但各配各的超时
var klineHTTP = &http.Client{Timeout: klineTimeout}

// FetchDetail 拉单只股票的完整行情。
// 配置了自建行情源时降级复用 FetchOne（自建接口只约定了 4 个字段），
// 缺的量额等字段置零；内置腾讯源则直接解析全字段
func FetchDetail(ctx context.Context, code string) (*QuoteDetail, error) {
	if quoteAPIBase != "" {
		snap, err := FetchOne(ctx, code)
		if err != nil {
			return nil, err
		}
		return &QuoteDetail{
			Code:          snap.Code,
			Name:          snap.Name,
			Price:         snap.Price,
			ChangePercent: snap.ChangePercent,
			Status:        marketStatus(time.Now()),
		}, nil
	}

	if _, ok := stock.NormalizeCode(code); !ok {
		return nil, fmt.Errorf("invalid stock code %q", code)
	}

	body, err := fetchTencentRaw(ctx, tencentQuoteURL+strings.ToLower(code))
	if err != nil {
		return nil, err
	}
	// fetchTencentRaw 已完成 GBK → UTF-8 转码，这里按文本解析
	details := parseTencentDetails(body)
	if len(details) == 0 {
		return nil, fmt.Errorf("no quote detail for %s", code)
	}
	return details[0], nil
}

// FetchKline 拉取前复权 K 线。period 取 day / week / month，count 上限 MaxKlineCount
func FetchKline(ctx context.Context, code, period string, count int) ([]KlineRow, error) {
	normalized, ok := stock.NormalizeCode(code)
	if !ok {
		return nil, fmt.Errorf("invalid stock code %q", code)
	}
	if count <= 0 {
		count = 10
	}
	if count > MaxKlineCount {
		count = MaxKlineCount
	}

	symbol := strings.ToLower(normalized)
	url := fmt.Sprintf("%s%s,%s,,,%d,qfq", tencentKlineURL, symbol, period, count)

	body, err := requestKline(ctx, url)
	if err != nil {
		return nil, err
	}
	return parseKlineBody(body, symbol, period)
}

func requestKline(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (stock-agent-miniapp)")

	resp, err := klineHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kline api http %d", resp.StatusCode)
	}
	// fqkline 返回 UTF-8 JSON，无需 GBK 转码
	return io.ReadAll(io.LimitReader(resp.Body, klineMaxBodyBytes))
}

// parseKlineBody 解析 fqkline 响应：
//
//	{"code":0,"data":{"sh600519":{"day":[["2026-10-17","1700.00","1705.30","1710.00","1690.00","23456.00"],...]}}}
//
// 每行是 [日期, 开, 收, 高, 低, 量(手)]，个别品种行尾还会挂一个附加对象，忽略即可。
// 前复权数据的 key 可能带 qfq 后缀（如 "qfqday"），两种都试
func parseKlineBody(body []byte, symbol, period string) ([]KlineRow, error) {
	var envelope struct {
		Code json.Number              `json:"code"`
		Data map[string]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode kline body failed: %w", err)
	}
	if envelope.Code.String() != "0" {
		return nil, fmt.Errorf("kline api code %s", envelope.Code.String())
	}

	node := envelope.Data[symbol]
	if node == nil {
		return nil, fmt.Errorf("no kline data for %s", symbol)
	}
	rawRows, ok := pickKlineRows(node[period], node["qfq"+period])
	if !ok {
		return nil, fmt.Errorf("no %s kline rows for %s", period, symbol)
	}

	rows := make([]KlineRow, 0, len(rawRows))
	prevClose := decimal.Zero
	for _, r := range rawRows {
		if len(r) < 5 {
			continue // 不足「日期+开收高低」的行直接丢弃
		}
		row := KlineRow{Date: r[0]}
		row.Open = parseDecimal(r[1])
		row.Close = parseDecimal(r[2])
		row.High = parseDecimal(r[3])
		row.Low = parseDecimal(r[4])
		if len(r) > 5 {
			row.Volume = int64(parseDecimal(r[5]).IntPart())
		}
		// 涨跌幅按前收盘价计算；首行没有前收，退化为按开盘价
		base := prevClose
		if base.IsZero() {
			base = row.Open
		}
		if !base.IsZero() {
			row.ChangePercent = row.Close.Sub(base).
				Div(base).Mul(decimal.NewFromInt(100)).Round(2)
		}
		prevClose = row.Close
		rows = append(rows, row)
	}
	return rows, nil
}

// pickKlineRows 从候选值里挑出第一个能解析成 [][]string 的。
// 腾讯偶发把行数据挂在带 qfq 前缀的 key 下，两种 key 都兜住
func pickKlineRows(candidates ...any) ([][]string, bool) {
	for _, cand := range candidates {
		if cand == nil {
			continue
		}
		b, err := json.Marshal(cand)
		if err != nil {
			continue
		}
		var rows [][]json.RawMessage
		if err := json.Unmarshal(b, &rows); err != nil {
			continue
		}
		out := make([][]string, 0, len(rows))
		for _, r := range rows {
			cells := make([]string, 0, len(r))
			for _, cell := range r {
				var s string
				// 单元格可能是字符串也可能是数字，统一转成字符串再走 decimal
				if err := json.Unmarshal(cell, &s); err != nil {
					var n json.Number
					if err := json.Unmarshal(cell, &n); err != nil {
						s = ""
					} else {
						s = n.String()
					}
				}
				cells = append(cells, s)
			}
			out = append(out, cells)
		}
		return out, true
	}
	return nil, false
}

func parseDecimal(s string) decimal.Decimal {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return decimal.Zero
	}
	return d
}

// 腾讯行情串中 QuoteDetail 用到的其余字段下标
// （tecName/tecCode/tecPrice/tecPercent 已在 source.go 定义）
const (
	tecPrevClose = 4  // 昨收
	tecOpen      = 5  // 今开
	tecVolume    = 6  // 成交量（手）
	tecChange    = 31 // 涨跌额
	tecHigh      = 33 // 最高
	tecLow       = 34 // 最低
	tecAmount    = 37 // 成交额（万元）
	tecTime      = 30 // 行情时间，形如 20261020150003
	tecTurnover  = 38 // 换手率（%）
	tecTotalMv   = 44 // 总市值（亿元）
)

// parseTencentDetails 解析腾讯行情文本为完整快照，字段布局同 parseTencentQuotes
func parseTencentDetails(body string) []*QuoteDetail {
	out := make([]*QuoteDetail, 0)

	for _, m := range tencentVarRe.FindAllStringSubmatch(body, -1) {
		line := m[0]
		eq := strings.Index(line, `="`)

		symbol := strings.ToUpper(line[:eq])
		if i := strings.Index(symbol, "_"); i >= 0 {
			symbol = symbol[i+1:]
		}

		fields := strings.Split(m[1], "~")
		if len(fields) <= tecTotalMv {
			continue
		}

		price := parseDecimal(fields[tecPrice])
		if len(strings.TrimSpace(fields[tecPrice])) == 0 {
			// 停牌 / 退市 / 代码不存在时价格字段为空，按无行情处理
			log.Printf("parse quote detail failed: code=%s raw=%q", symbol, fields[tecPrice])
			continue
		}

		name := strings.TrimSpace(fields[tecName])
		if name == "" {
			name = symbol
		}

		out = append(out, &QuoteDetail{
			Code:          symbol,
			Name:          name,
			Price:         price,
			Change:        parseDecimal(fields[tecChange]),
			ChangePercent: parseDecimal(fields[tecPercent]),
			Open:          parseDecimal(fields[tecOpen]),
			High:          parseDecimal(fields[tecHigh]),
			Low:           parseDecimal(fields[tecLow]),
			Volume:        int64(parseDecimal(fields[tecVolume]).IntPart()),
			Amount:        parseDecimal(fields[tecAmount]),
			Turnover:      parseDecimal(fields[tecTurnover]),
			TotalMv:       parseDecimal(fields[tecTotalMv]).Mul(decimal.NewFromInt(100000000)),
			Status:        marketStatus(time.Now()),
			Time:          formatQuoteTime(strings.TrimSpace(fields[tecTime])),
		})
	}
	return out
}

// formatQuoteTime 把腾讯的紧凑时间串（20261020150003）转成可读格式；
// 长度不符时原样返回，不做臆测
func formatQuoteTime(raw string) string {
	if len(raw) != 14 {
		return raw
	}
	return raw[0:4] + "-" + raw[4:6] + "-" + raw[6:8] + " " + raw[8:10] + ":" + raw[10:12] + ":" + raw[12:14]
}

// shanghaiLoc A 股行情时间基准时区
var shanghaiLoc = time.FixedZone("CST", 8*3600)

// marketStatus 按交易时段粗判市场状态：仅覆盖常规时段
// （周一至五 9:30-11:30 / 13:00-15:00），不含节假日日历——
// 详情页展示用，精确到「是否交易时段」已足够，不值得为此维护节假日表
func marketStatus(now time.Time) string {
	t := now.In(shanghaiLoc)
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return "closed"
	}
	hm := t.Hour()*60 + t.Minute()
	morning := hm >= 9*60+30 && hm < 11*60+30
	afternoon := hm >= 13*60 && hm < 15*60
	if morning || afternoon {
		return "trading"
	}
	return "closed"
}
