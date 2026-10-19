package quote

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"stock-agent-server/internal/util"
)

// tencentFixture 取自腾讯行情的真实返回（已转 UTF-8）。
// 原始：v_sh600519="1~贵州茅台~600519~1329.00~1312.97~1311.57~...~16.03~1.22~..."
// 其中 1329.00 为最新价、1312.97 为昨收、16.03 为涨跌额、1.22 为涨跌幅
const tencentFixture = `v_sh600519="1~贵州茅台~600519~1329.00~1312.97~1311.57~38502~21136~17365~1328.58~1~1328.50~1~1328.00~1~1327.99~2~1327.19~1~1329.00~54~1329.01~1~1329.09~1~1329.10~2~1329.18~7~~20261016161447~16.03~1.22~1329.88~1301.68~1329.00/38502/5075528306~38502~507553~0.31~20.40~~1329.88~1301.68~2.15~16613.58~16613.58~6.61~1444.27~1181.67~1.10~-59~1318.26~18.66~20.18~~~0.04~507552.8306~265.8000~20~   A~GP-A~-1.49~5.23~3.91~32.41~27.30~1539.98~1151.01~7.44~3.41~5.56~1250081601~1250081601~-83.10~-2.99~1250081601~~~-7.25~-0.06~~CNY~0~___D__F__N~1326.40~4~";`

func TestParseTencentQuotes(t *testing.T) {
	snaps := parseTencentQuotes(tencentFixture)
	if len(snaps) != 1 {
		t.Fatalf("期望解析出 1 条，实际 %d 条", len(snaps))
	}

	s := snaps[0]
	if s.Code != "SH600519" {
		t.Errorf("Code 期望 SH600519，实际 %s", s.Code)
	}
	if s.Name != "贵州茅台" {
		t.Errorf("Name 期望 贵州茅台，实际 %s", s.Name)
	}
	if !s.Price.Equal(decimal.NewFromFloat(1329.00)) {
		t.Errorf("Price 期望 1329.00，实际 %s", s.Price)
	}
	if !s.ChangePercent.Equal(decimal.NewFromFloat(1.22)) {
		t.Errorf("ChangePercent 期望 1.22，实际 %s", s.ChangePercent)
	}
}

// TestParseTencentQuotesBadCode 停牌 / 代码不存在时价格字段为空，应被跳过而不是塞进脏数据
func TestParseTencentQuotesBadCode(t *testing.T) {
	bad := `v_bj430047="62~诺思兰德~430047~~8.17~0.00~0~0~0~0.00~0~0.00~0~0.00~0~0.00~0~0.00~0~0.00~0~0.00~0~0.00~0~0.00~0~0.00~0~~20261016090000~0.00~0.00~0.00~0.00~8.17/0/0~0~0.00~0.00~-35.46~C~0.00~0.00~0.00~14.71~22.41~9.03~-1~-1~0.00~0~0.00~-38.40~-41.71~~~~0.0000~0.0000~0~ ~GP~0.00~0.00~0.00~-25.45~-12.62~~~0.00~0.00~0.00~180106427~274271974~~0.00~180106427~~~0.00~0.00~~CNY~4~~0.00~0";`

	if got := parseTencentQuotes(bad); len(got) != 0 {
		t.Errorf("空价格应被丢弃，实际返回 %d 条", len(got))
	}
}

// TestChunkStrings 分批：末尾不足一批时也要收进去，且不越界
func TestChunkStrings(t *testing.T) {
	in := []string{"a", "b", "c", "d", "e"}
	got := chunkStrings(in, 2)

	if len(got) != 3 {
		t.Fatalf("5 个按 2 分批应为 3 批，实际 %d", len(got))
	}
	if len(got[2]) != 1 || got[2][0] != "e" {
		t.Errorf("最后一批应为 [e]，实际 %v", got[2])
	}

	if empty := chunkStrings(nil, 2); len(empty) != 0 {
		t.Errorf("空输入应返回 0 批，实际 %d", len(empty))
	}
}

// TestUniqueStrings 去重保序。各包的 uniqueStrings / uniqueCodes 已统一成
// util.Unique 泛型版，这里验证行为没变
func TestUniqueStrings(t *testing.T) {
	got := util.Unique([]string{"SH600519", "SZ000001", "SH600519"})
	if len(got) != 2 {
		t.Fatalf("去重后应为 2 个，实际 %d：%v", len(got), got)
	}
	if got[0] != "SH600519" || got[1] != "SZ000001" {
		t.Errorf("应保持首次出现的顺序，实际 %v", got)
	}
}

// TestFetchStockQuoteReal 联网真实调用：验证腾讯源确实能取到行情。
// 不希望 CI/离线环境失败，用 -short 跳过
func TestFetchStockQuoteReal(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过联网测试")
	}

	s, err := fetchStockQuote(context.Background(), "SH600519")
	if err != nil {
		t.Fatalf("拉取真实行情失败：%v", err)
	}
	if s.Name == "" {
		t.Error("股票名称不应为空")
	}
	if !s.Price.IsPositive() {
		t.Errorf("最新价应大于 0，实际 %s", s.Price)
	}
	t.Logf("SH600519 %s 最新价 %s 涨跌幅 %s%%", s.Name, s.Price, s.ChangePercent)
}
