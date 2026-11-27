package server_svc_test

import (
	"os"
	"testing"
)

// TestMain 把数据目录钉到一次性目录：桌面端登录 / 退出会在 AppDataDir 里代签、清理 agrctl
// 凭据，单测绝不能碰到开发者真实的数据目录。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "server_svc_test_data")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("AGENTRE_DATA_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
