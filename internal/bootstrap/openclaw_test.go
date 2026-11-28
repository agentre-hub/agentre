package bootstrap

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// Given 一份 OpenClaw 引擎配置摊成的后端视图（没有后端 ID，同步身份是配置自己的），When 解析
// 它的 Gateway 连接配置，Then 地址取自视图、token 取自按配置 sync_id 命名的槽位（迁移规则 8
// 搬过去的那一格）。
func TestOpenClawRuntimeConfig_ResolvesFromTheBackendView(t *testing.T) {
	prev := keychain.Default()
	store := keychain.NewMemory()
	keychain.SetDefault(store)
	t.Cleanup(func() { keychain.SetDefault(prev) })
	if err := store.Set(backendcred.OpenClawTokenAccount("profile-oc"), "gw-token"); err != nil {
		t.Fatal(err)
	}
	view := &agent_backend_entity.AgentBackend{
		Type: string(agent_backend_entity.TypeOpenClaw), Name: "OpenClaw",
		OpenClawGatewayURL: "ws://127.0.0.1:18789", OpenClawAgentID: "main",
		OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
	}
	view.SyncID = "profile-oc"

	cfg, err := openClawRuntimeConfig(context.Background(), view)
	if err != nil {
		t.Fatalf("openClawRuntimeConfig() error = %v", err)
	}
	if cfg.URL != "ws://127.0.0.1:18789" || cfg.Token != "gw-token" || cfg.Identity == nil {
		t.Fatalf("openClawRuntimeConfig() = %+v; want the view's URL, the profile slot token and an identity", cfg)
	}
}

// Given 不是 OpenClaw 的后端，Then 报错而不是拿一份不相干的配置去连 Gateway。
func TestOpenClawRuntimeConfig_RejectsOtherBackends(t *testing.T) {
	if _, err := openClawRuntimeConfig(context.Background(), &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)}); err == nil {
		t.Fatal("openClawRuntimeConfig() error = nil for a codex backend")
	}
	if _, err := openClawRuntimeConfig(context.Background(), nil); err == nil {
		t.Fatal("openClawRuntimeConfig() error = nil for a nil backend")
	}
}
