package stock

import (
	"context"
	"log"
	"strings"

	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/quote"
)

// A股主数据的读路径（迁移 008）：只读 stock_basic / v_stock_profile。
// 表结构与索引由 SQL 迁移持有，刻意不进 AutoMigrate——同一张表两套 DDL 迟早分叉。

// profileRow 是 v_stock_profile 的扫描形态：日期与数组在 SQL 里先转成文本，
// 避免 NullTime / text[] 的扫描适配（pgx 的数组支持在 GORM 链路上不够直观）。
type profileRow struct {
	Code       string `gorm:"column:code"`
	Market     string `gorm:"column:market"`
	TSCode     string `gorm:"column:ts_code"`
	GwCode     string `gorm:"column:gw_code"`
	Name       string `gorm:"column:name"`
	Board      string `gorm:"column:board"`
	Status     string `gorm:"column:status"`
	IsST       bool   `gorm:"column:is_st"`
	IndustryL1 string `gorm:"column:industry_l1"`
	IndustryL2 string `gorm:"column:industry_l2"`
	IndustryL3 string `gorm:"column:industry_l3"`
	Concepts   string `gorm:"column:concepts"`
	ListDate   string `gorm:"column:list_date"`
	DelistDate string `gorm:"column:delist_date"`
}

// Profile 对外结构：代码三种写法一次给全，调用方不再自己拼。
type Profile struct {
	Code       string   `json:"code"`    // 600519
	Market     string   `json:"market"`  // SH
	TSCode     string   `json:"ts_code"` // 600519.SH
	GwCode     string   `json:"gw_code"` // SH600519
	Name       string   `json:"name"`
	Board      string   `json:"board"` // main / gem / star / bse
	Status     string   `json:"status"`
	IsST       bool     `json:"is_st"`
	IndustryL1 string   `json:"industry_l1"`
	IndustryL2 string   `json:"industry_l2"`
	IndustryL3 string   `json:"industry_l3"`
	Concepts   []string `json:"concepts"`
	ListDate   string   `json:"list_date,omitempty"`
	DelistDate string   `json:"delist_date,omitempty"`
}

const profileSQL = `
SELECT code, market, ts_code, gw_code, name, board, status, is_st,
       COALESCE(industry_l1, '') AS industry_l1,
       COALESCE(industry_l2, '') AS industry_l2,
       COALESCE(industry_l3, '') AS industry_l3,
       COALESCE(array_to_string(concepts, ','), '') AS concepts,
       COALESCE(to_char(list_date, 'YYYY-MM-DD'), '') AS list_date,
       COALESCE(to_char(delist_date, 'YYYY-MM-DD'), '') AS delist_date
FROM v_stock_profile
WHERE code = ?`

// searchRow 本地搜索的返回行，字段名与 quote.SearchResult 对齐。
type searchRow struct {
	Code   string `gorm:"column:code"`
	Market string `gorm:"column:market"`
	Name   string `gorm:"column:name"`
}

// likeEscaper 转义 LIKE 通配符。参数走占位符，这里只需保证用户输入里的
// % _ 被当成字面量（不转义的话搜 "100%" 会把整表捞出来）。
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// digitsOf 抽出用户输入里的数字（最多 6 位）：支持 "600519" / "SH600519" / "600519.SH" / "600"。
func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if len(d) > 6 {
		d = d[:6]
	}
	return d
}

// splitConcepts "白酒,融资融券" -> ["白酒","融资融券"]。
func splitConcepts(s string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildLocalSearchSQL 拼搜索语句，单独抽出来便于单测（不依赖数据库）。
// 排序：同名优先，其次三元组相似度，最后按代码保证稳定。
func buildLocalSearchSQL(query string, limit int) (string, []any) {
	term := strings.TrimSpace(query)
	if term == "" || limit <= 0 {
		return "", nil
	}

	conds := []string{"status = 'listed'"}
	args := make([]any, 0, 5)
	if digits := digitsOf(term); digits != "" {
		conds = append(conds, "code LIKE ?")
		args = append(args, digits+"%")
	}
	pattern := "%" + likeEscaper.Replace(term) + "%"
	conds = append(conds, "(name ILIKE ? OR pinyin_abbr ILIKE ?)")
	args = append(args, pattern, pattern)

	args = append(args, term, term, limit)
	sql := "SELECT code, market, name FROM stock_basic WHERE " +
		strings.Join(conds, " AND ") +
		" ORDER BY (name = ?) DESC, similarity(name, ?) DESC, code LIMIT ?"
	return sql, args
}

// SearchLocal 用主数据表兜底搜索：腾讯 smartbox 挂掉或返回空时接管。
// 表未建（迁移未执行）只记日志返回 nil，搜索退回上游结果，不把主流程拖失败。
func SearchLocal(ctx context.Context, query string, limit int) []quote.SearchResult {
	if deps.DB == nil {
		return nil
	}
	sql, args := buildLocalSearchSQL(query, limit)
	if sql == "" {
		return nil
	}

	var rows []searchRow
	if err := deps.DB.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		log.Printf("local stock search failed: q=%s err=%v", query, err)
		return nil
	}
	out := make([]quote.SearchResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, quote.SearchResult{Code: r.Code, Market: r.Market, Name: r.Name})
	}
	return out
}

// FetchProfile 按 6 位裸码查主数据；(nil, nil) 表示库里没有这只股票。
func FetchProfile(ctx context.Context, code string) (*Profile, error) {
	if deps.DB == nil {
		return nil, nil
	}
	var row profileRow
	if err := deps.DB.WithContext(ctx).Raw(profileSQL, code).Scan(&row).Error; err != nil {
		return nil, err
	}
	if row.Code == "" {
		return nil, nil
	}
	return &Profile{
		Code:       row.Code,
		Market:     row.Market,
		TSCode:     row.TSCode,
		GwCode:     row.GwCode,
		Name:       row.Name,
		Board:      row.Board,
		Status:     row.Status,
		IsST:       row.IsST,
		IndustryL1: row.IndustryL1,
		IndustryL2: row.IndustryL2,
		IndustryL3: row.IndustryL3,
		Concepts:   splitConcepts(row.Concepts),
		ListDate:   row.ListDate,
		DelistDate: row.DelistDate,
	}, nil
}
