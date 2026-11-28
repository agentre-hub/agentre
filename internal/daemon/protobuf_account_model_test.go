package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// protobuf_account_model_test.go 钉住 F3:agrctl 账号模式不带 --model 派发时,这一轮跑在
// 后端绑定的供应商 / 固定模型 / 思考力度上。整条链是真的 —— 真实 agentred(handler 栈、
// auth.account、enginesnapshot.Manager 从账号 server 拉快照)、真实 ctlaccount.Client 发
// send 与 get session;快照正文逐字是 agentre-server 的 HTTP 响应形状。只有 backend 换成
// 记下它收到的执行配置的假 runtime。
//
// 此前的单测按 engine_svc.SnapshotView 写快照,而 server 的控制器把它转成 HTTP DTO
// (api/engine.SnapshotBackend)时丢了绑定的四个键 —— 真栈上这一轮于是跑回 CLI 自身登录态。

const (
	modelProviderKey = "01M8CD7TF7KFF4FR3708SQ5SZ8"
	sonnetModelKey   = "1eb9b868-5d2c-4c43-9d8e-0f3e6b1a2c11"
	opusModelKey     = "7a10c2f4-9b1e-4d7a-8c3f-2e5d6f7a8b90"
	ccVerifySyncID   = "01M8CDBACKENDCCVERIFY00000"
	verifierSyncID   = "01M8CDAGENTVERIFIER0000000"
	verifyBoxFP      = "sha256:verify-box"
)

// serverSnapshot is the /v1/engine/snapshot response data exactly as agentre-server
// serves it over HTTP — it mirrors agentre-server internal/api/engine.SnapshotResponse
// (SnapshotProvider / SnapshotCLIOverlay / SnapshotBackend), written as a literal because
// agentre cannot import the server type — for backend cc-verify (claudecode, --provider
// relay-anthropic, --model claude-sonnet-5-5, --reasoning-effort low). SnapshotBackend
// leaves empty provider/model/effort/env out (omitempty) and always carries config.
const serverSnapshot = `{
  "providers": [{
    "provider_key": "` + modelProviderKey + `", "name": "relay-anthropic", "type": "anthropic",
    "base_url": "https://relay.example/v1", "api_key": "sk-relay", "default_model_key": "` + opusModelKey + `",
    "models": [
      {"model_key": "` + opusModelKey + `", "model_id": "claude-opus-5-5", "name": "Opus", "enabled": true},
      {"model_key": "` + sonnetModelKey + `", "model_id": "claude-sonnet-5-5", "name": "Sonnet", "enabled": true}
    ]
  }],
  "cli_overlays": [],
  "backends": [{
    "backend_sync_id": "` + ccVerifySyncID + `", "provider_key": "` + modelProviderKey + `",
    "model_key": "` + sonnetModelKey + `", "reasoning_effort": "low", "config": {}
  }]
}`

// dispatchPlanJSON is /v1/workspace/dispatch-target for agent verifier: its one
// eligible device is the agentred verify-box, running engine profile cc-verify.
const dispatchPlanJSON = `{
  "found": true,
  "engine": {"sync_id": "` + ccVerifySyncID + `", "name": "cc-verify", "runtime": "claudecode"},
  "devices": [{"device_id": 7, "device_fingerprint": "` + verifyBoxFP + `", "device_name": "verify-box",
    "kind": "agentred", "eligible": true, "chosen": true}],
  "chosen": {"device_fingerprint": "` + verifyBoxFP + `", "device_name": "verify-box",
    "kind": "agentred", "engine_sync_id": "` + ccVerifySyncID + `", "runtime": "claudecode"}
}`

func envelope(data string) []byte {
	return []byte(`{"code":0,"msg":"ok","data":` + data + `}`)
}

// ranConfig is what the backend runtime was handed for one turn.
type ranConfig struct {
	provider, modelID, effort string
}

// recordingRT is a claudecode backend that records the resolved run config and
// ends every turn at once.
type recordingRT struct {
	ran chan ranConfig
}

func (*recordingRT) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (r *recordingRT) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	var got ranConfig
	if req.Provider != nil {
		got.provider = req.Provider.Name
	}
	if req.Effective != nil {
		got.modelID = req.Effective.ModelID
	}
	if req.Backend != nil {
		got.effort = req.Backend.ReasoningEffort
	}
	r.ran <- got
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

// accountServer is the account server's HTTP surface seen by the ctlaccount client.
type accountServer struct {
	mu    sync.Mutex
	saved []map[string]string
}

func (s *accountServer) GetRaw(_ context.Context, pathAndQuery string) ([]byte, error) {
	if strings.HasPrefix(pathAndQuery, "/v1/workspace/dispatch-target?") {
		return envelope(dispatchPlanJSON), nil
	}
	if strings.HasPrefix(pathAndQuery, "/v1/agent-sessions?") {
		s.mu.Lock()
		defer s.mu.Unlock()
		rows, _ := json.Marshal(s.saved)
		return envelope(`{"items":` + string(rows) + `}`), nil
	}
	return envelope(`{}`), nil
}

func (s *accountServer) PostRaw(_ context.Context, path string, body []byte) ([]byte, error) {
	if path == "/v1/saved-sessions" {
		var row map[string]string
		_ = json.Unmarshal(body, &row)
		s.mu.Lock()
		s.saved = append(s.saved, row)
		s.mu.Unlock()
	}
	return envelope(`{}`), nil
}

// modelCatalog is the account's resources as agrctl lists them: agent verifier
// on backend cc-verify, provider relay-anthropic with its two models.
type modelCatalog struct{}

func (modelCatalog) items(kind agentrewire.CtlKind) []*agentrewire.CtlResource {
	switch kind {
	case agentrewire.CtlKind_CTL_KIND_AGENT:
		return []*agentrewire.CtlResource{{Doc: &agentrewire.CtlResource_Agent{Agent: &agentrewire.CtlAgent{
			Id: 1, Name: "verifier", SyncId: verifierSyncID, BackendIds: []int64{3},
		}}}}
	case agentrewire.CtlKind_CTL_KIND_BACKEND:
		return []*agentrewire.CtlResource{{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{
			Id: 3, Name: "cc-verify", Type: "claudecode", ProviderId: 2, ModelId: 21, ReasoningEffort: "low",
			SyncId: ccVerifySyncID, DeviceFingerprint: verifyBoxFP, Device: "verify-box",
		}}}}
	case agentrewire.CtlKind_CTL_KIND_PROVIDER:
		return []*agentrewire.CtlResource{{Doc: &agentrewire.CtlResource_Provider{Provider: &agentrewire.CtlProvider{
			Id: 2, Name: "relay-anthropic", SyncId: modelProviderKey,
		}}}}
	case agentrewire.CtlKind_CTL_KIND_MODEL:
		return []*agentrewire.CtlResource{
			{Doc: &agentrewire.CtlResource_Model{Model: &agentrewire.CtlModel{Id: 20, ProviderId: 2, Key: opusModelKey, ModelId: "claude-opus-5-5", Enabled: true}}},
			{Doc: &agentrewire.CtlResource_Model{Model: &agentrewire.CtlModel{Id: 21, ProviderId: 2, Key: sonnetModelKey, ModelId: "claude-sonnet-5-5", Enabled: true}}},
		}
	}
	return nil
}

func (c modelCatalog) List(kind agentrewire.CtlKind) ([]*agentrewire.CtlResource, error) {
	return c.items(kind), nil
}

func (c modelCatalog) ByID(kind agentrewire.CtlKind, id int64) *agentrewire.CtlResource {
	for _, it := range c.items(kind) {
		if ctlResourceID(it) == id {
			return it
		}
	}
	return nil
}

func (modelCatalog) Path(agentrewire.CtlKind, int64) string { return "" }

func (c modelCatalog) Locate(kind agentrewire.CtlKind, ref string) (*agentrewire.CtlResource, error) {
	for _, it := range c.items(kind) {
		if it.GetAgent().GetName() == ref || it.GetModel().GetModelId() == ref ||
			it.GetProvider().GetName()+"/"+it.GetModel().GetModelId() == ref {
			return it, nil
		}
	}
	return nil, ctlaccount.ErrSessionNotVisible
}

func ctlResourceID(it *agentrewire.CtlResource) int64 {
	switch {
	case it.GetAgent() != nil:
		return it.GetAgent().GetId()
	case it.GetBackend() != nil:
		return it.GetBackend().GetId()
	case it.GetProvider() != nil:
		return it.GetProvider().GetId()
	case it.GetModel() != nil:
		return it.GetModel().GetId()
	}
	return 0
}

type modelRig struct {
	daemon *Daemon
	rt     *recordingRT
	ctx    context.Context
	client *ctlaccount.Client
}

// newModelRig is a logged-in agentred whose account server serves the
// server-shaped engine snapshot and answers introspection as the agrctl CLI
// device, with the first snapshot applied the way a relay connect applies it.
func newModelRig(t *testing.T) *modelRig {
	t.Helper()
	rt := &recordingRT{ran: make(chan ranConfig, 4)}
	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, rt))
	d, err := New(Options{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(d.db) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/engine/snapshot" {
			_, _ = w.Write(envelope(serverSnapshot))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": cliIntrospection()})
	}))
	t.Cleanup(srv.Close)
	d.state.Login(scopeAccount, state.AccountCredential{AccessToken: "receiver-token"})
	d.state.Mutate(func(s *state.State) { s.AccountServerURL = srv.URL })
	require.NoError(t, d.state.Save())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, d.engineSnapshot.Pull(ctx))
	client := accountClientOn(d, "cli-credential", &accountServer{}, modelCatalog{})
	return &modelRig{daemon: d, rt: rt, ctx: ctx, client: client}
}

func (r *modelRig) ran(t *testing.T) ranConfig {
	t.Helper()
	select {
	case got := <-r.rt.ran:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached the backend")
		return ranConfig{}
	}
}

// Given backend cc-verify is bound to relay-anthropic / claude-sonnet-5-5 / effort low
// in the account snapshot, When agrctl sends to agent verifier without --model, Then
// the backend runtime runs on that provider, model and effort, and get session shows
// that model id.
func TestDaemonAccountModel_GivenABackendWithAFixedModel_WhenSentWithoutOverrides_ThenTheTurnRunsOnIt(t *testing.T) {
	rig := newModelRig(t)

	reply, err := rig.client.Send(rig.ctx, ctlaccount.SendInput{Agent: "verifier", Text: "Reply with exactly: fixed-model-check."})
	require.NoError(t, err)

	assert.Equal(t, ranConfig{provider: "relay-anthropic", modelID: "claude-sonnet-5-5", effort: "low"}, rig.ran(t))
	detail, err := rig.client.GetSession(rig.ctx, reply.ConversationID)
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-5-5", detail.Model, "get session names the backend's fixed model the session follows")
}

// Given the same backend, When agrctl sends with --model and --effort, Then the
// overrides win over the backend's configuration.
func TestDaemonAccountModel_GivenABackendWithAFixedModel_WhenSentWithOverrides_ThenTheOverridesWin(t *testing.T) {
	rig := newModelRig(t)

	_, err := rig.client.Send(rig.ctx, ctlaccount.SendInput{Agent: "verifier", Text: "hi", Model: "claude-opus-5-5", Effort: "high"})
	require.NoError(t, err)

	assert.Equal(t, ranConfig{provider: "relay-anthropic", modelID: "claude-opus-5-5", effort: "high"}, rig.ran(t))
}
