package quote

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"

	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/util"
)

// 行情批量与缓存参数：包级变量由 Init 按配置覆盖（默认值即迁移前硬编码档）。
var (
	// ttl 行情缓存 30 秒，与提示词约定一致
	ttl = 30 * time.Second

	// limit 拉取行情的并发上限：防止瞬时并发打爆接口被封 IP
	limit = 5

	// batchSizeP 分批拉取时每批的股票数。
	// 内置源支持一次查多只，50 只自选股逐只要 50 次请求（分 10 轮），
	// 按 20 只一批只要 3 次。请求数越少，越快也越不容易触发第三方限流
	batchSizeP = 20

	// mgetChunkSize 批量读缓存的分片大小。
	// 自选股上千时单次 MGET 带上千个 key 会拉高单次 RTT 与内存峰值
	mgetChunkSize = 100
)

// StockSnapshot 行情快照（提示词约定的结构体）
type StockSnapshot struct {
	Code          string          `json:"code"`
	Name          string          `json:"name"`
	Price         decimal.Decimal `json:"price"`
	ChangePercent decimal.Decimal `json:"change_percent"`
}

// key 行情缓存键：stock:{code}:latest
func key(code string) string {
	return fmt.Sprintf("stock:%s:latest", code)
}

// SaveQuoteToRedis 写单条行情缓存，过期 30 秒
func SaveQuoteToRedis(ctx context.Context, s *StockSnapshot) error {
	if deps.RDB == nil || s == nil {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return deps.RDB.Set(ctx, key(s.Code), b, ttl).Err()
}

// BatchSaveQuotes 批量写：一次 pipeline 提交，避免逐条往返
func BatchSaveQuotes(ctx context.Context, list []*StockSnapshot) error {
	if deps.RDB == nil || len(list) == 0 {
		return nil
	}
	_, err := deps.RDB.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, s := range list {
			if s == nil {
				continue
			}
			b, mErr := json.Marshal(s)
			if mErr != nil {
				// 单条序列化失败不影响其它
				log.Printf("marshal quote failed: %v", mErr)
				continue
			}
			p.Set(ctx, key(s.Code), b, ttl)
		}
		return nil
	})
	return err
}

// GetQuoteFromRedis 读单条行情缓存
func GetQuoteFromRedis(ctx context.Context, code string) (*StockSnapshot, error) {
	if deps.RDB == nil {
		return nil, redis.Nil
	}
	b, err := deps.RDB.Get(ctx, key(code)).Bytes()
	if err != nil {
		return nil, err
	}
	var s StockSnapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// BatchGetQuotes 批量读：分片 MGET。
// 50 只自选股从 50 次往返降为 1 次；缓存未命中的 code 不出现在结果里，
// 由调用方按降级策略处理（读不到 = 已过期 / 未缓存）。
// 分片是为了自选股规模上千时不把上千个 key 塞进单次命令
func BatchGetQuotes(ctx context.Context, codes []string) (map[string]*StockSnapshot, error) {
	out := make(map[string]*StockSnapshot, len(codes))
	if deps.RDB == nil || len(codes) == 0 {
		return out, nil
	}

	for _, chunk := range chunkStrings(codes, mgetChunkSize) {
		keys := make([]string, 0, len(chunk))
		for _, c := range chunk {
			keys = append(keys, key(c))
		}
		vals, err := deps.RDB.MGet(ctx, keys...).Result()
		if err != nil {
			return out, err
		}
		for i, v := range vals {
			s, ok := v.(string)
			if !ok {
				continue // nil = 缓存不存在
			}
			var snap StockSnapshot
			if err := json.Unmarshal([]byte(s), &snap); err != nil {
				log.Printf("unmarshal quote %s failed: %v", chunk[i], err)
				continue
			}
			out[chunk[i]] = &snap
		}
	}
	return out, nil
}

// FetchQuotes 并发拉行情：errgroup + SetLimit(limit)。
// 分两步：
//  1. 去重 + 分批，每批一次 HTTP 往返；
//  2. 批与批之间并发，上限 limit。
//
// 某批失败只影响批内的 code，其余照常返回，交给调用方降级。
// goroutine 返回 error 时 errgroup 会取消 gctx，让还没发出的请求提前收手
func FetchQuotes(ctx context.Context, codes []string) (map[string]*StockSnapshot, []string) {
	out := make(map[string]*StockSnapshot, len(codes))
	if len(codes) == 0 {
		return out, nil
	}

	// 去重：同一只股票在一次刷新里只查一次，
	// 后续用「去重集合 - 成功集合」的差集得到失败列表
	uniq := util.Unique(codes)

	var (
		mu      sync.Mutex
		g, gctx = errgroup.WithContext(ctx)
	)
	g.SetLimit(limit)

	for _, batch := range chunkStrings(uniq, batchSizeP) {
		batch := batch
		g.Go(func() error {
			snaps := fetchStockQuotes(gctx, batch)
			mu.Lock()
			defer mu.Unlock()
			for _, s := range snaps {
				out[s.Code] = s
			}
			// 拉不到就报错：借 errgroup 取消其它批次，省掉注定失败的请求
			if len(snaps) == 0 {
				return fmt.Errorf("fetch quotes failed: %v", batch)
			}
			return nil
		})
	}

	_ = g.Wait()

	failed := make([]string, 0)
	for _, c := range uniq {
		if _, ok := out[c]; !ok {
			failed = append(failed, c)
		}
	}
	return out, failed
}

func chunkStrings(in []string, size int) [][]string {
	if size <= 0 {
		size = 1
	}
	out := make([][]string, 0)
	for start := 0; start < len(in); start += size {
		end := start + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[start:end])
	}
	return out
}

// FetchOne 拉单只行情：添加自选时用，失败即 502 不写库
func FetchOne(ctx context.Context, code string) (*StockSnapshot, error) {
	return fetchStockQuote(ctx, code)
}
