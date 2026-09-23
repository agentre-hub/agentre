package ctlcmd

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/ctlendpoint"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/service/ctl_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 端到端：agrctl 对着真正的 ctl_svc 控制 handler（资源与审批换成内存假件）跑，钉住
// 两边的等待审批协议（102 + ApprovalPendingHeader）与被拒时的退出码。

type e2eOrg struct{}

func (e2eOrg) ListAgents(context.Context) ([]*agentrewire.CtlAgent, error) {
	return []*agentrewire.CtlAgent{{Id: 12, Name: "reviewer", DepartmentId: 2}}, nil
}

func (e2eOrg) ListDepartments(context.Context) ([]*agentrewire.CtlDepartment, error) {
	return []*agentrewire.CtlDepartment{{Id: 2, Name: "eng"}, {Id: 6, Name: "qa"}}, nil
}

func (e2eOrg) ListProjects(context.Context) ([]*agentrewire.CtlProject, error)   { return nil, nil }
func (e2eOrg) ListProviders(context.Context) ([]*agentrewire.CtlProvider, error) { return nil, nil }
func (e2eOrg) ListModels(context.Context) ([]*agentrewire.CtlModel, error)       { return nil, nil }
func (e2eOrg) ListBackends(context.Context) ([]*agentrewire.CtlBackend, error)   { return nil, nil }

type e2eWriter struct {
	mu      sync.Mutex
	updates int
}

func (w *e2eWriter) Create(context.Context, ctl_svc.Write) (int64, error) { return 0, nil }
func (w *e2eWriter) Delete(context.Context, ctl_svc.Write) error          { return nil }
func (w *e2eWriter) Update(context.Context, ctl_svc.Write) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.updates++
	return nil
}

func (w *e2eWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.updates
}

// answeringApprovals 立即以 allow 作答的会话审批卡 / 桌面队列。
type answeringApprovals struct{ allow bool }

func (a answeringApprovals) BeginToolApproval(context.Context, int64, *blocks.ToolApprovalBlock) (<-chan bool, error) {
	ch := make(chan bool, 1)
	ch <- a.allow
	return ch, nil
}
func (a answeringApprovals) FinishToolApproval(context.Context, int64, string, string, string) error {
	return nil
}
func (a answeringApprovals) Enqueue(context.Context, ctl_svc.ExternalApproval) (<-chan bool, error) {
	return a.BeginToolApproval(context.Background(), 0, nil)
}
func (a answeringApprovals) Answer(string, bool) error { return nil }
func (a answeringApprovals) Withdraw(string)           {}

func realExecutor(t *testing.T, allow bool) (*httptest.Server, *e2eWriter) {
	t.Helper()
	w := &e2eWriter{}
	svc := ctl_svc.Default()
	org := e2eOrg{}
	svc.RegisterResources(ctl_svc.Resources{
		Org: org, Projects: org, Providers: org, Backends: org,
		Writers: map[agentrewire.CtlKind]ctl_svc.KindWriter{agentrewire.CtlKind_CTL_KIND_AGENT: w},
	})
	svc.RegisterSessionApprovals(answeringApprovals{allow: allow})
	svc.RegisterExternalApprovals(answeringApprovals{allow: allow})
	t.Cleanup(func() {
		svc.RegisterResources(ctl_svc.ProductionResources())
		svc.RegisterExternalApprovals(nil)
	})
	srv := httptest.NewServer(svc.ControlHandler())
	t.Cleanup(srv.Close)
	return srv, w
}

var updateReviewer = []string{"update", "agent", "reviewer", "--department", "qa"}

func TestE2E_GivenSessionCallerWhenApprovedThenWaitingLineAndUpdated(t *testing.T) {
	srv, w := realExecutor(t, true)
	r := runWith(updateReviewer, envFor(srv, ctl_svc.Default().SessionToken(7, 42)), term{})
	wantCode(t, r, 0)
	if r.stderr != "waiting for approval in session #42 …\n" {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if r.stdout != "agent reviewer updated\n" || w.count() != 1 {
		t.Fatalf("stdout = %q, updates = %d", r.stdout, w.count())
	}
}

func TestE2E_GivenSessionCallerWhenRejectedThenExit1(t *testing.T) {
	srv, w := realExecutor(t, false)
	r := runWith(updateReviewer, envFor(srv, ctl_svc.Default().SessionToken(7, 42)), term{})
	wantCode(t, r, 1)
	if r.stderr != "waiting for approval in session #42 …\nError: rejected in session #42\n" {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if w.count() != 0 {
		t.Fatal("a rejected write must not reach the service")
	}
}

func desktopHandshake(t *testing.T, srv *httptest.Server) func(string) (string, bool) {
	t.Helper()
	dir := t.TempDir()
	if err := ctlendpoint.Write(dir, ctlendpoint.Endpoint{URL: srv.URL, Token: ctl_svc.Default().Token()}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_DATA_DIR", dir)
	return noEnv
}

func TestE2E_GivenExternalCallerWhenRejectedThenExit1(t *testing.T) {
	srv, w := realExecutor(t, false)
	r := runWith(updateReviewer, desktopHandshake(t, srv), term{tty: false})
	wantCode(t, r, 1)
	if r.stderr != "waiting for approval in the Agentre desktop …\nError: rejected in the Agentre desktop\n" {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if w.count() != 0 {
		t.Fatal("a rejected write must not reach the service")
	}
}

func TestE2E_GivenHumanCallerThenWrittenWithoutWaiting(t *testing.T) {
	srv, w := realExecutor(t, false)
	r := runWith(updateReviewer, desktopHandshake(t, srv), term{tty: true})
	wantCode(t, r, 0)
	if r.stderr != "" || w.count() != 1 {
		t.Fatalf("stderr = %q, updates = %d", r.stderr, w.count())
	}
}
