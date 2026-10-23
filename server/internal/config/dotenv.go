package config

import (
	"os"
	"strings"
)

// loadDotEnv 本地 .env 兜底加载（零依赖实现，补齐 Viper 化后缺失的一环）。
//
// 背景：config 层的三层优先级是「进程环境变量 > config.yaml > 内置默认值」，
// 但本地开发习惯把密钥放在 server/.env：不加载的话 `go run .` 会静默丢掉
// OPENAI_API_KEY 等只写在 .env 里的配置项（容器部署由 compose 注入环境变量，
// 不受影响，所以这是本地开发期的问题）。
//
// 约定：
//   - 真实环境变量优先级最高：已存在的键一律不覆盖（便于临时 export 覆盖 .env）；
//   - .env 缺失不算错误（默认值 + 环境变量即可运行）；
//   - 支持 `KEY=VALUE`、`export KEY=VALUE`、值两侧的单/双引号；忽略空行与 # 注释。
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}
		// 去掉包裹值的引号（"..." / '...'），保留内部空格
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
}
