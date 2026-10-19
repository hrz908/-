package util

// Unique 去重且保持原有顺序。
// 之前 quote / alert / watchlist 各写了一份 uniqueStrings / uniqueCodes，
// 用泛型统一到这里
func Unique[T comparable](in []T) []T {
	seen := make(map[T]struct{}, len(in))
	out := make([]T, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// Values 取 map 的所有 value（顺序不保证）。
// 用于把「code -> snapshot」的 map 转成切片批量写缓存
func Values[K comparable, V any](m map[K]V) []V {
	out := make([]V, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
