package agent_backend_svc

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
)

func setupScanTest(t *testing.T) (
	context.Context,
	*mock_agent_backend_repo.MockAgentBackendRepo,
	*mock_llm_provider_repo.MockLLMProviderRepo,
	*agentBackendSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	backendMock := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	providerMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	agent_backend_repo.RegisterAgentBackend(backendMock)
	llm_provider_repo.RegisterLLMProvider(providerMock)

	svc := &agentBackendSvc{
		now:    func() int64 { return 1234567890 },
		probes: map[string]context.CancelFunc{},
	}
	return context.Background(), backendMock, providerMock, svc
}

type findByNameMatcher struct{}

func (m findByNameMatcher) Matches(x any) bool {
	s, ok := x.(string)
	if !ok {
		return false
	}
	return s == "Claude Code CLI" || s == "Codex CLI" || s == "Pi Agent CLI"
}

func (m findByNameMatcher) String() string { return "is a CLI backend default name" }

func TestScanAndCreateAgentBackends_ReturnsAllThreeTypes(t *testing.T) {
	ctx, backendMock, _, svc := setupScanTest(t)

	backendMock.EXPECT().
		ExistsByName(gomock.Any(), findByNameMatcher{}).
		Return(false, nil).
		AnyTimes()
	backendMock.EXPECT().
		FindByName(gomock.Any(), findByNameMatcher{}).
		Return(nil, nil).
		AnyTimes()
	backendMock.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
			b.ID = 1
			return nil
		}).
		AnyTimes()

	resp, err := svc.ScanAndCreateAgentBackends(ctx, &ScanAndCreateAgentBackendsRequest{})
	assert.NoError(t, err)
	assert.Len(t, resp.Results, 3)

	types := map[string]bool{}
	for _, r := range resp.Results {
		types[r.Type] = true
		assert.NotEmpty(t, r.Type)
		assert.NotEmpty(t, r.Name)
		if !r.Found {
			assert.False(t, r.Created, "%s: Found=false but Created=true", r.Type)
			assert.False(t, r.Skipped, "%s: Found=false but Skipped=true", r.Type)
			assert.NotEmpty(t, r.Error, "%s: Found=false but Error is empty", r.Type)
		}
		if r.Created {
			assert.True(t, r.Found, "%s: Created=true but Found=false", r.Type)
			assert.False(t, r.Skipped, "%s: Created=true and Skipped=true conflict", r.Type)
		}
		if r.Skipped {
			assert.True(t, r.Found, "%s: Skipped=true but Found=false", r.Type)
			assert.False(t, r.Created, "%s: Skipped=true and Created=true conflict", r.Type)
		}
	}
	assert.True(t, types["claudecode"], "missing claudecode result")
	assert.True(t, types["codex"], "missing codex result")
	assert.True(t, types["piagent"], "missing piagent result")

	direct := cliprober.ScanAllCLIs()
	assert.Len(t, direct, len(resp.Results))
	for i, d := range direct {
		assert.Equal(t, d.BackendType, resp.Results[i].Type, "type mismatch at index %d", i)
		assert.Equal(t, d.Found, resp.Results[i].Found, "found mismatch at index %d", i)
		assert.Equal(t, d.Path, resp.Results[i].CLIPath, "path mismatch at index %d", i)
	}
}

func TestScanAndCreateAgentBackends_NameDuplicateSkips(t *testing.T) {
	results := cliprober.ScanAllCLIs()
	if !results[0].Found {
		t.Skip("claude not found; cannot test name-duplicate skip")
	}

	ctx, backendMock, _, svc := setupScanTest(t)

	backendMock.EXPECT().
		ExistsByName(gomock.Any(), gomock.Any()).
		Return(false, nil).
		AnyTimes()
	backendMock.EXPECT().
		FindByName(gomock.Any(), "Claude Code CLI").
		Return(&agent_backend_entity.AgentBackend{ID: 99, Name: "Claude Code CLI"}, nil)
	backendMock.EXPECT().
		FindByName(gomock.Any(), gomock.Not("Claude Code CLI")).
		Return(nil, nil).
		AnyTimes()
	backendMock.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		Return(nil).
		AnyTimes()

	resp, err := svc.ScanAndCreateAgentBackends(ctx, &ScanAndCreateAgentBackendsRequest{})
	convey.Convey("Claude Code found with name conflict → skipped", t, func() {
		convey.So(err, convey.ShouldBeNil)
		claude := resp.Results[0]
		convey.Convey("claudecode result", func() {
			convey.So(claude.Found, convey.ShouldBeTrue)
			convey.So(claude.Created, convey.ShouldBeFalse)
			convey.So(claude.Skipped, convey.ShouldBeTrue)
			convey.So(claude.Error, convey.ShouldContainSubstring, "already exists")
		})
	})
}

// TestScanAndCreateAgentBackends_TombstoneNameSkips 回归 Problem 19 / 决策 25:
// 扫描创建的撞名判据必须把墓碑一并计入,否则「扫描建 → 被删 → 再扫描建」每轮都会
// 留一条新墓碑(实测 Claude Code CLI / Codex CLI / Pi Agent CLI 各 47 条同名墓碑,
// createtime 完全相同)。同名墓碑存在时,重新扫描不应新建第二条 —— 应跳过且不调
// Create。
func TestScanAndCreateAgentBackends_TombstoneNameSkips(t *testing.T) {
	results := cliprober.ScanAllCLIs()
	if !results[0].Found {
		t.Skip("claude not found; cannot test tombstone name skip")
	}

	ctx, backendMock, _, svc := setupScanTest(t)

	backendMock.EXPECT().
		ExistsByName(gomock.Any(), "Claude Code CLI").
		Return(true, nil)
	backendMock.EXPECT().
		ExistsByName(gomock.Any(), gomock.Not("Claude Code CLI")).
		Return(false, nil).
		AnyTimes()
	backendMock.EXPECT().
		FindByName(gomock.Any(), gomock.Not("Claude Code CLI")).
		Return(nil, nil).
		AnyTimes()
	backendMock.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
			b.ID = 100
			return nil
		}).
		AnyTimes()

	resp, err := svc.ScanAndCreateAgentBackends(ctx, &ScanAndCreateAgentBackendsRequest{})
	convey.Convey("Claude Code found but a same-named tombstone already exists → skipped, no second row created", t, func() {
		convey.So(err, convey.ShouldBeNil)
		claude := resp.Results[0]
		convey.So(claude.Found, convey.ShouldBeTrue)
		convey.So(claude.Created, convey.ShouldBeFalse)
		convey.So(claude.Skipped, convey.ShouldBeTrue)
		convey.So(claude.Error, convey.ShouldContainSubstring, "already exists")
	})
}

func TestScanAndCreateAgentBackends_FoundCreates(t *testing.T) {
	results := cliprober.ScanAllCLIs()
	if !results[0].Found {
		t.Skip("claude not found; cannot test auto-create")
	}

	ctx, backendMock, _, svc := setupScanTest(t)

	backendMock.EXPECT().
		ExistsByName(gomock.Any(), gomock.Any()).
		Return(false, nil).
		AnyTimes()
	backendMock.EXPECT().
		FindByName(gomock.Any(), "Claude Code CLI").
		Return(nil, nil)
	backendMock.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
			b.ID = 100
			return nil
		})
	backendMock.EXPECT().
		FindByName(gomock.Any(), gomock.Not("Claude Code CLI")).
		Return(nil, nil).
		AnyTimes()
	backendMock.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
			b.ID = 200
			return nil
		}).
		AnyTimes()

	resp, err := svc.ScanAndCreateAgentBackends(ctx, &ScanAndCreateAgentBackendsRequest{})
	convey.Convey("Claude Code found → auto-created", t, func() {
		convey.So(err, convey.ShouldBeNil)
		claude := resp.Results[0]
		convey.So(claude.Found, convey.ShouldBeTrue)
		convey.So(claude.Created, convey.ShouldBeTrue)
		convey.So(claude.Skipped, convey.ShouldBeFalse)
		convey.So(claude.BackendID, convey.ShouldEqual, 100)
	})
}

func TestDefaultNameForType(t *testing.T) {
	assert.Equal(t, "Claude Code CLI", defaultNameForType("claudecode"))
	assert.Equal(t, "Codex CLI", defaultNameForType("codex"))
	assert.Equal(t, "Pi Agent CLI", defaultNameForType("piagent"))
	assert.Equal(t, "unknown CLI", defaultNameForType("unknown"))
}
