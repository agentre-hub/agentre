package piagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// testProviderCfg 构造一条执行侧配置（EffectiveLLMConfig v1 seam）供物化扩展测试用。
func testProviderCfg(key, name, baseURL, typ, modelID string) *agentruntime.EffectiveLLMConfig {
	return &agentruntime.EffectiveLLMConfig{
		Mode:          agentruntime.EffectiveModeProviderDefault,
		ProviderKey:   key,
		ProviderName:  name,
		ProviderType:  typ,
		ModelID:       modelID,
		BaseURL:       baseURL,
		ContextWindow: 200000,
		MaxOutput:     8192,
	}
}

func TestMaterializeProviderExtension_WritesContentHashedFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	tcs := []struct {
		name string
		cfg  *agentruntime.EffectiveLLMConfig
	}{
		{
			name: "anthropic",
			cfg:  testProviderCfg("prov-a", "ProvA", "https://a.example", string(llm_provider_entity.TypeAnthropic), "claude-3"),
		},
		{
			name: "openai-chat",
			cfg:  testProviderCfg("prov-b", "ProvB", "https://b.example", string(llm_provider_entity.TypeOpenAIChat), "gpt-4o"),
		},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			path, err := MaterializeProviderExtension(tc.cfg)
			if err != nil {
				t.Fatalf("MaterializeProviderExtension: %v", err)
			}
			wantDir := filepath.Join(dataDir, "piagent", "ext")
			if !strings.HasPrefix(path, wantDir) {
				t.Fatalf("path not under %s: %s", wantDir, path)
			}
			base := filepath.Base(path)
			if !strings.HasPrefix(base, "agentre-provider-") || !strings.HasSuffix(base, ".mjs") {
				t.Fatalf("unexpected filename: %s", base)
			}
			raw, err := os.ReadFile(path) //nolint:gosec // path returned by MaterializeProviderExtension, constrained to the test temp data dir.
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			source, err := agentruntime.PiAgentProviderExtension(tc.cfg)
			if err != nil {
				t.Fatalf("PiAgentProviderExtension: %v", err)
			}
			if string(raw) != source {
				t.Fatalf("file content != rendered source")
			}
			// 密钥永不落盘（决策 #4）：扩展文件只含 $ENV_VAR 引用，绝不含明文 APIKey。
			if strings.Contains(string(raw), "sk-plaintext") {
				t.Fatalf("extension leaked APIKey literal")
			}
		})
	}
}

func TestMaterializeProviderExtension_Idempotent(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	cfg := testProviderCfg("prov-x", "ProvX", "https://x.example", string(llm_provider_entity.TypeOpenAIChat), "gpt-4o")
	p1, err := MaterializeProviderExtension(cfg)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if _, err := os.Stat(p1); err != nil {
		t.Fatalf("file missing: %v", err)
	}
	p2, err := MaterializeProviderExtension(cfg)
	if err != nil || p2 != p1 {
		t.Fatalf("not idempotent: p1=%s p2=%s err=%v", p1, p2, err)
	}
}

func TestMaterializeProviderExtension_DifferentSourceDifferentPath(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	base := func(modelID string) *agentruntime.EffectiveLLMConfig {
		return testProviderCfg("prov-y", "ProvY", "https://y.example", string(llm_provider_entity.TypeOpenAIResponse), modelID)
	}
	p1, err := MaterializeProviderExtension(base("model-one"))
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	p2, err := MaterializeProviderExtension(base("model-two"))
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("expected different hashed paths, both %s", p1)
	}
}
