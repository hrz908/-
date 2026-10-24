package stock

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildLocalSearchSQL(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		limit   int
		wantSQL []string
		noSQL   []string
		wantArg []any
	}{
		{
			name:    "代码：带前缀匹配",
			query:   "600519",
			limit:   5,
			wantSQL: []string{"code LIKE ?", "name ILIKE ?", "status = 'listed'"},
			wantArg: []any{"600519%", "%600519%", "%600519%", "600519", "600519", 5},
		},
		{
			name:    "中文名：不加代码条件",
			query:   "茅台",
			limit:   3,
			wantSQL: []string{"name ILIKE ?"},
			noSQL:   []string{"code LIKE ?"},
			wantArg: []any{"%茅台%", "%茅台%", "茅台", "茅台", 3},
		},
		{
			name:    "带市场的写法：数字照抽，展示串照搜",
			query:   "SH 600519",
			limit:   5,
			wantSQL: []string{"code LIKE ?"},
			wantArg: []any{"600519%", "%SH 600519%", "%SH 600519%", "SH 600519", "SH 600519", 5},
		},
		{
			name:    "通配符必须转义（转义结果在参数里，不在 SQL 文本里）",
			query:   "100%_A",
			limit:   5,
			wantSQL: []string{"name ILIKE ?", "code LIKE ?"},
			wantArg: []any{"100%", `%100\%\_A%`, `%100\%\_A%`, "100%_A", "100%_A", 5},
		},
		{
			name:  "空 query 不查库",
			query: "   ",
			limit: 5,
		},
		{
			name:  "limit 非正数不查库",
			query: "600519",
			limit: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := buildLocalSearchSQL(tt.query, tt.limit)
			if tt.wantSQL == nil && tt.noSQL == nil && tt.wantArg == nil {
				if sql != "" || args != nil {
					t.Fatalf("期望不生成 SQL，实际 sql=%q args=%v", sql, args)
				}
				return
			}
			if sql == "" {
				t.Fatal("期望生成 SQL，实际为空")
			}
			for _, frag := range tt.wantSQL {
				if !strings.Contains(sql, frag) {
					t.Errorf("SQL 缺少片段 %q：%s", frag, sql)
				}
			}
			for _, frag := range tt.noSQL {
				if strings.Contains(sql, frag) {
					t.Errorf("SQL 不应包含片段 %q：%s", frag, sql)
				}
			}
			if tt.wantArg != nil && !reflect.DeepEqual(args, tt.wantArg) {
				t.Errorf("参数不匹配\n得到: %#v\n期望: %#v", args, tt.wantArg)
			}
		})
	}
}

func TestDigitsOf(t *testing.T) {
	tests := map[string]string{
		"600519":    "600519",
		"SH600519":  "600519",
		"600519.SH": "600519",
		"SH 600519": "600519",
		"600":       "600",
		"920001234": "920001", // 超过 6 位只取前 6 位，避免拼出非法代码
		"茅台":        "",
		"":          "",
	}
	for in, want := range tests {
		if got := digitsOf(in); got != want {
			t.Errorf("digitsOf(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestSplitConcepts(t *testing.T) {
	if got := splitConcepts(""); len(got) != 0 {
		t.Errorf("空串应返回空切片，实际 %v", got)
	}
	got := splitConcepts("白酒, 融资融券 ,")
	want := []string{"白酒", "融资融券"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitConcepts = %v, 期望 %v", got, want)
	}
}
