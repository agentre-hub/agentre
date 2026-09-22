package code

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 「无效目标」这一句归共享包 agentre-ui 持有（portForward.add.invalidTarget），所有
// 宿主说同一句话；Go 目录里的 PortForwardInvalidTarget 是它不得不留的一份副本，
// 这里钉住两边逐字相同，免得一边改了另一边静默漂走。
func TestPortForwardInvalidTargetMatchesSharedUICatalog(t *testing.T) {
	for lang, catalog := range map[string]map[int]string{"en": enUS, "zh-CN": zhCN} {
		path := filepath.Join("..", "..", "..", "frontend", "packages", "agentre-ui",
			"src", "i18n", "locales", lang, "port-forward.json")
		raw, err := os.ReadFile(path) //nolint:gosec // 守卫读取仓库内固定相对路径。
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc struct {
			PortForward struct {
				Add struct {
					InvalidTarget string `json:"invalidTarget"`
				} `json:"add"`
			} `json:"portForward"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		want := doc.PortForward.Add.InvalidTarget
		if want == "" {
			t.Fatalf("%s: portForward.add.invalidTarget is missing", path)
		}
		if got := catalog[PortForwardInvalidTarget]; got != want {
			t.Errorf("%s PortForwardInvalidTarget = %q, want shared UI sentence %q", lang, got, want)
		}
	}
}
