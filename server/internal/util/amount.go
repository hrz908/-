package util

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Money 金额统一保留 2 位小数，避免 1705.3 / 1705.29999 这类不一致
func Money(d decimal.Decimal) string {
	return d.StringFixed(2)
}

// Thousands 千分位 + 2 位小数：1705.30 → 1,705.30
func Thousands(d decimal.Decimal) string {
	s := d.StringFixed(2)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}

	intPart, frac := s, ""
	if i := strings.Index(s, "."); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}

	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}

	out := b.String()
	if neg {
		out = "-" + out
	}
	return out + frac
}

// WithSign 数值带正负号（+1.23 / -0.86 / 0.00）。
// -0.001 经 StringFixed(2) 会得到 "-0.00"，先归一成 "0.00"，否则前端显示负零。
func WithSign(d decimal.Decimal) string {
	s := d.StringFixed(2)
	if s == "-0.00" {
		s = "0.00"
	}
	if d.IsPositive() {
		return "+" + s
	}
	return s
}
