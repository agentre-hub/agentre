package bootstrap

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

// stubDispatch 只答 ResolveSessionWorkspace；碰到别的方法当场 panic。
type stubDispatch struct {
	dispatch_svc.DispatchSvc
	cwd string
}

func (s stubDispatch) ResolveSessionWorkspace(context.Context, int64) (int64, string, error) {
	return 0, s.cwd, nil
}

// TestInitWiresWorkspaceFilesThroughDispatch 证明 Init 装好派发服务，且工作区文件的会话
// 解析经它走（不再经执行目标服务）。
func TestInitWiresWorkspaceFilesThroughDispatch(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	if dispatch_svc.Dispatch() == nil {
		t.Fatal("dispatch_svc.Dispatch() = nil after Init; bootstrap forgot to RegisterDispatch")
	}
	prev := dispatch_svc.Dispatch()
	cwd := t.TempDir()
	dispatch_svc.RegisterDispatch(stubDispatch{cwd: cwd})
	t.Cleanup(func() { dispatch_svc.RegisterDispatch(prev) })

	roots, err := workspace_fs_svc.Default().WorkRoots(context.Background(), 42)
	if err != nil {
		t.Fatalf("WorkRoots() error = %v", err)
	}
	if len(roots) == 0 || roots[0].Path != cwd {
		t.Fatalf("WorkRoots() = %+v; want primary root %q from the dispatch service", roots, cwd)
	}
}

// TestInitWiresSessionEngineToMemberEffectiveEngine 证明 Init 把会话读路径的「成员生效的
// 引擎配置」接到 agent_svc 的继承链上：没有自己引用、没有部门的成员拿到账号默认那份。
func TestInitWiresSessionEngineToMemberEffectiveEngine(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)
	ctx := context.Background()

	profile := &agent_backend_entity.EngineProfile{
		Type: string(agent_backend_entity.TypeCodex), Name: "Codex", Status: consts.ACTIVE,
	}
	if err := agent_backend_repo.EngineProfile().Create(ctx, profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if err := agent_backend_repo.EngineProfile().SetAccountDefault(ctx, profile.ID); err != nil {
		t.Fatalf("set account default: %v", err)
	}
	member := &agent_entity.Agent{Name: "成员", Status: consts.ACTIVE}
	if err := agent_repo.Agent().Create(ctx, member); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	got, err := sessionengine.Effective(ctx, member)
	if err != nil {
		t.Fatalf("Effective() error = %v", err)
	}
	if got == nil || got.ID != profile.ID {
		t.Fatalf("Effective() = %+v; want the account default profile %d", got, profile.ID)
	}
}
