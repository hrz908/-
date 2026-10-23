package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadDotEnv 覆盖 .env 解析的四类形态与「环境变量优先」约定。
func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "" +
		"# 注释行应被忽略\n" +
		"\n" +
		"DOTENV_TEST_PLAIN=plain\n" +
		"export DOTENV_TEST_EXPORT=exported\n" +
		"DOTENV_TEST_QUOTED=\"a b\"\n" +
		"DOTENV_TEST_SINGLE='c d'\n" +
		"DOTENV_TEST_EXISTING=fromfile\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// 已存在的环境变量必须优先于 .env（临时覆盖场景）
	t.Setenv("DOTENV_TEST_EXISTING", "fromenv")

	loadDotEnv(path)

	cases := map[string]string{
		"DOTENV_TEST_PLAIN":    "plain",
		"DOTENV_TEST_EXPORT":   "exported",
		"DOTENV_TEST_QUOTED":   "a b",
		"DOTENV_TEST_SINGLE":   "c d",
		"DOTENV_TEST_EXISTING": "fromenv",
	}
	for key, want := range cases {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// TestLoadDotEnvMissingFile 缺失 .env 属于正常形态，不得 panic / 报错。
func TestLoadDotEnvMissingFile(t *testing.T) {
	loadDotEnv(filepath.Join(t.TempDir(), "not-exists.env"))
}
