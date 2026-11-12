package chat_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo/mock_app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
	"github.com/agentre-hub/agentre/internal/service/memory_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// ── MemoryTier ──────────────────────────────────────────────────────────────

func TestMemoryTier(t *testing.T) {
	withMemory := capability.Capabilities{Set: map[capability.Capability]bool{capability.CapMemory: true}}
	without := capability.Capabilities{Set: map[capability.Capability]bool{capability.CapSteer: true}}

	cases := []struct {
		name    string
		caps    capability.Capabilities
		backend agent_backend_entity.BackendType
		want    MemoryLevel
	}{
		{"Claude Code 声明了记忆能力 → 完整", withMemory, agent_backend_entity.TypeClaudeCode, MemoryLevelFull},
		{"Codex 声明了记忆能力 → 完整", withMemory, agent_backend_entity.TypeCodex, MemoryLevelFull},
		{"Pi 声明了记忆能力 → 完整", withMemory, agent_backend_entity.TypePiAgent, MemoryLevelFull},
		{"远程设备太旧、没报记忆能力的 Claude Code → 不带", without, agent_backend_entity.TypeClaudeCode, MemoryLevelNone},
		{"远程能力还没拉到(空集)的 Codex → 不带", capability.Capabilities{}, agent_backend_entity.TypeCodex, MemoryLevelNone},
		{"内置后端 → 只带摘要", without, agent_backend_entity.TypeBuiltin, MemoryLevelSummaries},
		{"acp → 不带", without, agent_backend_entity.TypeACP, MemoryLevelNone},
		{"openclaw → 不带", without, agent_backend_entity.TypeOpenClaw, MemoryLevelNone},
		{"hermes → 不带", without, agent_backend_entity.TypeHermes, MemoryLevelNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, MemoryTier(tc.caps, string(tc.backend)))
		})
	}
}

// ── 段落渲染 ─────────────────────────────────────────────────────────────────

// memTime 返回本地时区某天中午的毫秒时间戳:日期按本地时间显示,中午离日界最远。
func memTime(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 12, 0, 0, 0, time.Local).UnixMilli()
}

// memItem 一条最近修改于 changed 的条目（行的 updatetime 同刻）。
func memItem(id int64, summary, detail string, changed int64) *memory_svc.MemoryItem {
	return &memory_svc.MemoryItem{ID: id, Summary: summary, Detail: detail, Updatetime: changed,
		LastChange: memory_svc.ChangeDTO{At: changed}}
}

func fourLayerSections() []memorySection {
	return []memorySection{
		{kind: memory_entity.OwnerGlobal, items: []*memory_svc.MemoryItem{
			memItem(12, "回复用中文，结论先行", "", memTime(2026, 10, 3)),
		}},
		{kind: memory_entity.OwnerDepartment, name: "后端组", items: []*memory_svc.MemoryItem{
			memItem(31, "仓储层测试一律用 mock 库", "", memTime(2026, 9, 20)),
		}},
		{kind: memory_entity.OwnerAgent, items: []*memory_svc.MemoryItem{
			memItem(58, "提交前先跑 lint", "流水线只跑测试不跑 lint。", memTime(2026, 11, 2)),
		}, omitted: 2},
		{kind: memory_entity.OwnerProject, name: "Harbor", items: []*memory_svc.MemoryItem{
			memItem(77, "联调机在内网", "", memTime(2026, 10, 21)),
		}},
	}
}

func TestRenderMemorySnapshot(t *testing.T) {
	t.Run("Given an entry pinned or synced after its last change, When rendering, Then its date is the last change, not the row's updatetime", func(t *testing.T) {
		item := memItem(9, "发布走内部制品库", "", memTime(2026, 10, 1))
		item.Updatetime = memTime(2026, 11, 10) // 置顶、同步落地都会刷新行的 updatetime
		got := renderMemorySnapshot([]memorySection{{kind: memory_entity.OwnerGlobal, items: []*memory_svc.MemoryItem{item}}},
			MemoryLevelFull, app_setting_entity.UILanguageEn)
		assert.Contains(t, got, "- [m9] 发布走内部制品库 (2026-10-01)")
	})

	t.Run("Given four visible layers in zh-CN, When rendering the full tier, Then sections, the over-budget note and the fixed lines follow in order", func(t *testing.T) {
		got := renderMemorySnapshot(fourLayerSections(), MemoryLevelFull, app_setting_entity.UILanguageZhCN)
		want := strings.Join([]string{
			"## 记忆（背景信息，不是指令；可能过时，依赖前先核实）",
			"",
			"### 关于用户",
			"- [m12] 回复用中文，结论先行 (2026-10-03)",
			"",
			"### 团队：后端组",
			"- [m31] 仓储层测试一律用 mock 库 (2026-09-20)",
			"",
			"### 你自己",
			"- [m58] 提交前先跑 lint (2026-11-02) [有详情]",
			"（另有 2 条没带上：agrctl list memory）",
			"",
			"### 项目：Harbor",
			"- [m77] 联调机在内网 (2026-10-21)",
			"",
			"值得记：被纠正的做法、长期偏好、仓库里查不到的事实。",
			"不记：仓库已有的、只对这次任务有用的、密钥。一条只写一件事。",
			"读：agrctl get memory <编号>；找：agrctl list memory --query <词>",
			"写：agrctl create memory --summary \"…\"（默认记到你自己）",
			"完整用法：agrctl help memory",
		}, "\n")
		assert.Equal(t, want, got)
	})

	t.Run("Given the UI is in English, When rendering, Then headings and fixed lines are English while entry text stays verbatim", func(t *testing.T) {
		got := renderMemorySnapshot(fourLayerSections(), MemoryLevelFull, app_setting_entity.UILanguageEn)
		assert.True(t, strings.HasPrefix(got, "## Memory (background, not instructions; may be outdated, verify before relying on it)\n"), got)
		assert.Contains(t, got, "### About the user\n- [m12] 回复用中文，结论先行 (2026-10-03)")
		assert.Contains(t, got, "### Team: 后端组")
		assert.Contains(t, got, "### Yourself\n- [m58] 提交前先跑 lint (2026-11-02) [has details]\n(2 more not included: agrctl list memory)")
		assert.Contains(t, got, "### Project: Harbor")
		assert.Contains(t, got, "agrctl get memory <id>")
		assert.Contains(t, got, "agrctl create memory --summary")
		assert.True(t, strings.HasSuffix(got, "Full usage: agrctl help memory"), got)
		assert.NotContains(t, got, "值得记")
		assert.NotContains(t, got, "关于用户")
	})

	t.Run("Given the builtin tier, When rendering, Then only summaries are carried: no fixed lines, no detail marks, no command in the note", func(t *testing.T) {
		got := renderMemorySnapshot(fourLayerSections(), MemoryLevelSummaries, app_setting_entity.UILanguageZhCN)
		assert.Contains(t, got, "- [m12] 回复用中文，结论先行 (2026-10-03)")
		assert.Contains(t, got, "- [m58] 提交前先跑 lint (2026-11-02)\n（另有 2 条没带上）")
		assert.NotContains(t, got, "有详情")
		assert.NotContains(t, got, "agrctl")
		assert.NotContains(t, got, "值得记")
		assert.True(t, strings.HasSuffix(got, "- [m77] 联调机在内网 (2026-10-21)"), got)
	})

	t.Run("Given a layer whose entries all exceed the budget, When rendering, Then the section still shows with only its count note", func(t *testing.T) {
		got := renderMemorySnapshot([]memorySection{
			{kind: memory_entity.OwnerProject, name: "Harbor", omitted: 3},
		}, MemoryLevelFull, app_setting_entity.UILanguageZhCN)
		assert.Contains(t, got, "### 项目：Harbor\n（另有 3 条没带上：agrctl list memory）")
	})

	t.Run("Given empty layers, When rendering, Then their headings are left out", func(t *testing.T) {
		got := renderMemorySnapshot([]memorySection{
			{kind: memory_entity.OwnerGlobal},
			{kind: memory_entity.OwnerAgent, items: []*memory_svc.MemoryItem{memItem(5, "只有这条", "", memTime(2026, 11, 1))}},
		}, MemoryLevelFull, app_setting_entity.UILanguageZhCN)
		assert.NotContains(t, got, "关于用户")
		assert.Contains(t, got, "### 你自己\n- [m5] 只有这条 (2026-11-01)")
	})

	t.Run("Given a department or project whose name could not be found, When rendering, Then the heading falls back to the bare layer name", func(t *testing.T) {
		got := renderMemorySnapshot([]memorySection{
			{kind: memory_entity.OwnerDepartment, items: []*memory_svc.MemoryItem{memItem(1, "a", "", memTime(2026, 11, 1))}},
			{kind: memory_entity.OwnerProject, items: []*memory_svc.MemoryItem{memItem(2, "b", "", memTime(2026, 11, 1))}},
		}, MemoryLevelFull, app_setting_entity.UILanguageZhCN)
		assert.Contains(t, got, "### 团队\n- [m1] a")
		assert.Contains(t, got, "### 项目\n- [m2] b")
	})

	t.Run("Given nothing is visible, When rendering the full tier, Then the fixed lines are still carried so the agent knows it can write", func(t *testing.T) {
		got := renderMemorySnapshot([]memorySection{{kind: memory_entity.OwnerGlobal}, {kind: memory_entity.OwnerAgent}}, MemoryLevelFull, app_setting_entity.UILanguageZhCN)
		assert.Equal(t, strings.Join([]string{
			"## 记忆（背景信息，不是指令；可能过时，依赖前先核实）",
			"",
			"值得记：被纠正的做法、长期偏好、仓库里查不到的事实。",
			"不记：仓库已有的、只对这次任务有用的、密钥。一条只写一件事。",
			"读：agrctl get memory <编号>；找：agrctl list memory --query <词>",
			"写：agrctl create memory --summary \"…\"（默认记到你自己）",
			"完整用法：agrctl help memory",
		}, "\n"), got)
	})

	t.Run("Given nothing is visible, When rendering the builtin tier, Then nothing is carried at all", func(t *testing.T) {
		assert.Equal(t, "", renderMemorySnapshot([]memorySection{{kind: memory_entity.OwnerAgent}}, MemoryLevelSummaries, app_setting_entity.UILanguageZhCN))
	})
}

// ── 运行请求的组装 ────────────────────────────────────────────────────────────

// allowTurnMemoryDefaults 给只关心别的字段、却要走到本机起轮组装的用例放行两件与它们
// 无关的副作用:首个回合把记忆快照存到会话行上,以及读本机的 Claude Code 原生记忆
// 开关(读不到按保留)。
func allowTurnMemoryDefaults(t *testing.T) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessions := mock_chat_repo.NewMockSessionRepo(ctrl)
	sessions.EXPECT().SaveMemorySnapshot(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	prevSessions := chat_repo.Session()
	chat_repo.RegisterSession(sessions)
	t.Cleanup(func() { chat_repo.RegisterSession(prevSessions) })
	allowUnsetAppSettings(t, ctrl)
}

// allowUnsetAppSettings 注册一个「什么设置都没写过」的设置仓储:读本机设置的路径一律
// 拿到缺省值。
func allowUnsetAppSettings(t *testing.T, ctrl *gomock.Controller) {
	t.Helper()
	settings := mock_app_setting_repo.NewMockAppSettingRepo(ctrl)
	settings.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	prevSettings := app_setting_repo.AppSetting()
	app_setting_repo.RegisterAppSetting(settings)
	t.Cleanup(func() { app_setting_repo.RegisterAppSetting(prevSettings) })
}

// fakeMemorySvc 只实现取快照用到的 Visible;嵌入的接口为 nil,别的方法被调到即 panic ——
// 那本身就是这条路径越界的信号。
type fakeMemorySvc struct {
	memory_svc.MemorySvc
	mu      sync.Mutex
	visible func(agentID, projectID int64) (*memory_svc.VisibleMemory, error)
	calls   [][2]int64
}

func (f *fakeMemorySvc) Visible(_ context.Context, agentID, projectID int64) (*memory_svc.VisibleMemory, error) {
	f.mu.Lock()
	f.calls = append(f.calls, [2]int64{agentID, projectID})
	f.mu.Unlock()
	return f.visible(agentID, projectID)
}

func (f *fakeMemorySvc) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// memoryRuntime 本地执行用的假 runtime:能力集可配,Run 不会被调到(用例只到组装)。
type memoryRuntime struct{ caps capability.Capabilities }

func (r memoryRuntime) Capabilities() capability.Capabilities { return r.caps }
func (memoryRuntime) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, nil
}

var fullMemoryCaps = capability.Capabilities{Set: map[capability.Capability]bool{capability.CapMemory: true}}

// memoryTurnEnv 一份「库」:会话行上的快照两列由 SaveMemorySnapshot 写进 stored,
// 下一轮从 reload 读回,与生产上每轮重新读会话行同构。
type memoryTurnEnv struct {
	t        *testing.T
	ctrl     *gomock.Controller
	svc      *chatSvc
	memory   *fakeMemorySvc
	sessions *mock_chat_repo.MockSessionRepo
	settings map[string]string

	mu       sync.Mutex
	stored   map[int64]*chat_entity.Session
	saveHits int
}

func newMemoryTurnEnv(t *testing.T) *memoryTurnEnv {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	env := &memoryTurnEnv{
		t: t, ctrl: ctrl, svc: &chatSvc{},
		settings: map[string]string{app_setting_entity.KeyUILanguage: app_setting_entity.UILanguageZhCN},
		stored:   map[int64]*chat_entity.Session{},
	}

	env.memory = &fakeMemorySvc{visible: func(agentID, projectID int64) (*memory_svc.VisibleMemory, error) {
		return visibleFor(agentID, projectID, "初始"), nil
	}}
	prevMemory := memory_svc.Memory()
	memory_svc.SetDefault(env.memory)
	t.Cleanup(func() { memory_svc.SetDefault(prevMemory) })

	env.sessions = mock_chat_repo.NewMockSessionRepo(ctrl)
	env.sessions.EXPECT().SaveMemorySnapshot(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64, snapshot string) (bool, error) {
			env.mu.Lock()
			defer env.mu.Unlock()
			env.saveHits++
			row := env.stored[id]
			if row == nil {
				row = &chat_entity.Session{ID: id, ConversationID: convID(id)}
				env.stored[id] = row
			}
			if row.MemorySnapshotTaken {
				return false, nil
			}
			row.MemorySnapshot, row.MemorySnapshotTaken = snapshot, true
			return true, nil
		}).AnyTimes()
	env.sessions.EXPECT().Find(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*chat_entity.Session, error) {
			env.mu.Lock()
			defer env.mu.Unlock()
			if row := env.stored[id]; row != nil {
				cp := *row
				return &cp, nil
			}
			return &chat_entity.Session{ID: id, ConversationID: convID(id)}, nil
		}).AnyTimes()
	// 远端借出成功会把执行位置写到会话行,与本用例无关。
	env.sessions.EXPECT().UpdateExecDaemon(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	prevSessions := chat_repo.Session()
	chat_repo.RegisterSession(env.sessions)
	t.Cleanup(func() { chat_repo.RegisterSession(prevSessions) })

	settings := mock_app_setting_repo.NewMockAppSettingRepo(ctrl)
	settings.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, key string) (*app_setting_entity.AppSetting, error) {
			v, ok := env.settings[key]
			if !ok {
				return nil, nil
			}
			return &app_setting_entity.AppSetting{Key: key, Value: v}, nil
		}).AnyTimes()
	prevSettings := app_setting_repo.AppSetting()
	app_setting_repo.RegisterAppSetting(settings)
	t.Cleanup(func() { app_setting_repo.RegisterAppSetting(prevSettings) })

	departments := mock_department_repo.NewMockDepartmentRepo(ctrl)
	departments.EXPECT().Find(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*department_entity.Department, error) {
			return &department_entity.Department{ID: id, Name: "后端组"}, nil
		}).AnyTimes()
	prevDepartments := department_repo.Department()
	department_repo.RegisterDepartment(departments)
	t.Cleanup(func() { department_repo.RegisterDepartment(prevDepartments) })

	projects := mock_project_repo.NewMockProjectRepo(ctrl)
	projects.EXPECT().Find(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*project_entity.Project, error) {
			return &project_entity.Project{ID: id, Name: "Harbor"}, nil
		}).AnyTimes()
	prevProjects := project_repo.Project()
	project_repo.RegisterProject(projects)
	t.Cleanup(func() { project_repo.RegisterProject(prevProjects) })

	messages := mock_transcript_repo.NewMockMessageRepo(ctrl)
	messages.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	prevMessages := transcript_repo.Message()
	transcript_repo.RegisterMessage(messages)
	t.Cleanup(func() { transcript_repo.RegisterMessage(prevMessages) })

	exec_target_svc.RegisterCwdResolver(func(context.Context, *chat_entity.Session) (string, error) { return "", nil })
	t.Cleanup(func() { exec_target_svc.RegisterCwdResolver(nil) })
	return env
}

// local 让 backendType 在本机执行,runtime 的能力集为 caps。
func (env *memoryTurnEnv) local(bt agent_backend_entity.BackendType, caps capability.Capabilities) *agent_backend_entity.AgentBackend {
	env.t.Helper()
	installPairedDevice(env.t, env.ctrl, 7)
	env.t.Cleanup(agentruntime.SwapRuntimeForTest(bt, memoryRuntime{caps: caps}))
	return &agent_backend_entity.AgentBackend{ID: 12, Type: string(bt), DeviceFingerprint: "sha256:self"}
}

// remote 让 Claude Code 在 7 号配对设备上执行;那台设备上报的能力集为 caps
// (经 runtime.capabilities 的真实预取路径)。
func (env *memoryTurnEnv) remote(caps capability.Capabilities) *agent_backend_entity.AgentBackend {
	env.t.Helper()
	rec := newRecordingDaemonClient()
	rec.expect(wire.MethodCapabilities, func(_, result any) error {
		*(result.(*wire.CapabilitiesResult)) = wire.CapabilitiesResult{Capabilities: caps}
		return nil
	})
	pool := mock_remote_device_svc.NewMockConnPool(env.ctrl)
	lease := mock_remote_device_svc.NewMockLease(env.ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).AnyTimes()
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(rec)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	env.svc.setConnPoolForTest(pool)
	installPairedDevice(env.t, env.ctrl, 7)
	return &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: testDeviceFingerprint(7)}
}

// reload 读回会话行(含已存下的快照),模拟下一轮从库里取会话。
func (env *memoryTurnEnv) reload(sess *chat_entity.Session) *chat_entity.Session {
	env.mu.Lock()
	defer env.mu.Unlock()
	cp := *sess
	if row := env.stored[sess.ID]; row != nil {
		cp.MemorySnapshot, cp.MemorySnapshotTaken = row.MemorySnapshot, row.MemorySnapshotTaken
	}
	return &cp
}

func (env *memoryTurnEnv) turn(sess *chat_entity.Session, a *agent_entity.Agent, be *agent_backend_entity.AgentBackend, forkAnchor string) (agentruntime.RunRequest, error) {
	env.t.Helper()
	userMsg := &chat_entity.Message{ID: 900 + int64(env.saveHits), SessionID: sess.ID, Role: "user"}
	assistantMsg := &chat_entity.Message{ID: 1900 + int64(env.saveHits), SessionID: sess.ID, Role: "assistant"}
	prepared, err := env.svc.prepareTurnRun(context.Background(), sess, a, be, nil, userMsg, assistantMsg, forkAnchor, false, false)
	if err != nil {
		return agentruntime.RunRequest{}, err
	}
	prepared.release()
	return prepared.req, nil
}

// visibleFor 每层一条;tag 进摘要,用来分辨「取的是哪一次」。
func visibleFor(agentID, projectID int64, tag string) *memory_svc.VisibleMemory {
	v := &memory_svc.VisibleMemory{Layers: []*memory_svc.VisibleLayer{
		{Owner: memory_entity.GlobalOwner(), Carried: []*memory_svc.MemoryItem{memItem(1, tag+"·关于用户", "", memTime(2026, 11, 1))}},
		{Owner: memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 3}, Carried: []*memory_svc.MemoryItem{memItem(2, tag+"·部门", "", memTime(2026, 11, 1))}},
		{Owner: memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: agentID}, Carried: []*memory_svc.MemoryItem{memItem(3, fmt.Sprintf("%s·agent%d", tag, agentID), "", memTime(2026, 11, 1))},
			OverBudget: []*memory_svc.MemoryItem{memItem(4, "超了", "", memTime(2026, 10, 1)), memItem(5, "又超了", "", memTime(2026, 10, 1))}},
	}}
	if projectID > 0 {
		v.Layers = append(v.Layers, &memory_svc.VisibleLayer{
			Owner:   memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: projectID},
			Carried: []*memory_svc.MemoryItem{memItem(6, tag+"·项目", "", memTime(2026, 11, 1))},
		})
	}
	return v
}

func memoryAgent(id int64, enabled bool) *agent_entity.Agent {
	return &agent_entity.Agent{ID: id, AgentBackendID: 12, PromptJSON: `["你是后端工程师。","先读代码再动手。"]`, MemoryEnabled: enabled}
}

const memoryAgentPrompt = "你是后端工程师。\n先读代码再动手。"

func TestBuildRunRequest_MemorySnapshot_FullTierFollowsAgentPrompt(t *testing.T) {
	env := newMemoryTurnEnv(t)
	be := env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps)
	sess := &chat_entity.Session{ID: 301, AgentID: 7, ProjectID: 40}

	req, err := env.turn(sess, memoryAgent(7, true), be, "")
	require.NoError(t, err)

	require.Equal(t, [][2]int64{{7, 40}}, env.memory.calls, "按会话的 agent 与项目取可见记忆")
	require.True(t, strings.HasPrefix(req.SystemPrompt, memoryAgentPrompt+"\n\n## 记忆（"), "快照接在 agent 自己的提示词之后:\n%s", req.SystemPrompt)
	snapshot := strings.TrimPrefix(req.SystemPrompt, memoryAgentPrompt+"\n\n")
	assert.Contains(t, snapshot, "### 关于用户\n- [m1] 初始·关于用户 (2026-11-01)")
	assert.Contains(t, snapshot, "### 团队：后端组\n- [m2] 初始·部门")
	assert.Contains(t, snapshot, "### 你自己\n- [m3] 初始·agent7 (2026-11-01)\n（另有 2 条没带上：agrctl list memory）")
	assert.Contains(t, snapshot, "### 项目：Harbor\n- [m6] 初始·项目")
	assert.True(t, strings.HasSuffix(snapshot, "完整用法：agrctl help memory"), "完整档带固定说明")
	assert.Equal(t, snapshot, env.stored[301].MemorySnapshot, "首个回合把快照存到会话行上")
}

func TestBuildRunRequest_MemorySnapshot_FrozenAcrossTurns(t *testing.T) {
	env := newMemoryTurnEnv(t)
	be := env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps)
	a := memoryAgent(7, true)
	sess := &chat_entity.Session{ID: 302, AgentID: 7}

	first, err := env.turn(sess, a, be, "")
	require.NoError(t, err)
	require.Equal(t, 1, env.memory.callCount())

	// 会话中途记忆变了(新写入、改了、归档了):下个会话才带上。
	env.memory.visible = func(agentID, projectID int64) (*memory_svc.VisibleMemory, error) {
		return visibleFor(agentID, projectID, "之后"), nil
	}
	for _, tc := range []struct {
		name       string
		forkAnchor string
	}{
		{"下一轮(含进程/应用重启后:会话行重新读出)", ""},
		{"重新生成(同一会话内丢弃尾部重发)", "anchor-assistant"},
		{"编辑重发 / 回退到某条消息后再发", "anchor-user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, err := env.turn(env.reload(sess), a, be, tc.forkAnchor)
			require.NoError(t, err)
			assert.Equal(t, first.SystemPrompt, next.SystemPrompt, "同一会话各回合文本一致")
		})
	}
	assert.Equal(t, 1, env.memory.callCount(), "快照只在首个回合取一次")
	assert.NotContains(t, first.SystemPrompt, "之后")
}

func TestBuildRunRequest_MemorySnapshot_StaleCopyUsesStoredSnapshot(t *testing.T) {
	env := newMemoryTurnEnv(t)
	be := env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps)
	a := memoryAgent(7, true)
	sess := &chat_entity.Session{ID: 303, AgentID: 7}
	stale := *sess // 首个回合之前读出、手里还没有快照的另一份实体

	first, err := env.turn(sess, a, be, "")
	require.NoError(t, err)
	env.memory.visible = func(agentID, projectID int64) (*memory_svc.VisibleMemory, error) {
		return visibleFor(agentID, projectID, "之后"), nil
	}

	again, err := env.turn(&stale, a, be, "")
	require.NoError(t, err)
	assert.Equal(t, first.SystemPrompt, again.SystemPrompt, "库里已有快照时用库里那份,不另起一份")
}

func TestBuildRunRequest_MemorySnapshot_CarriesNothing(t *testing.T) {
	t.Run("Given the agent's memory toggle is off, When the first turn runs, Then neither entries nor fixed lines are carried and that is frozen", func(t *testing.T) {
		env := newMemoryTurnEnv(t)
		be := env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps)
		sess := &chat_entity.Session{ID: 311, AgentID: 7}

		req, err := env.turn(sess, memoryAgent(7, false), be, "")
		require.NoError(t, err)
		assert.Equal(t, memoryAgentPrompt, req.SystemPrompt)
		assert.Equal(t, 0, env.memory.callCount())
		require.NotNil(t, env.stored[311])
		assert.True(t, env.stored[311].MemorySnapshotTaken, "「不带」同样在首个回合定下")

		// 会话中途把开关打开:这条会话仍不带,下个会话才带。
		req, err = env.turn(env.reload(sess), memoryAgent(7, true), be, "")
		require.NoError(t, err)
		assert.Equal(t, memoryAgentPrompt, req.SystemPrompt)
		assert.Equal(t, 0, env.memory.callCount())
	})

	t.Run("Given a session that started before the upgrade, When it runs a turn, Then it carries nothing and nothing is re-taken", func(t *testing.T) {
		env := newMemoryTurnEnv(t)
		be := env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps)
		// 迁移 202611110104 把已有消息的老会话标成「已取过、空快照」。
		sess := &chat_entity.Session{ID: 312, AgentID: 7, MemorySnapshotTaken: true}

		req, err := env.turn(sess, memoryAgent(7, true), be, "")
		require.NoError(t, err)
		assert.Equal(t, memoryAgentPrompt, req.SystemPrompt)
		assert.Equal(t, 0, env.memory.callCount())
		assert.Equal(t, 0, env.saveHits)
	})

	t.Run("Given a backend that does not read the prompt (acp), When the first turn runs, Then nothing is carried", func(t *testing.T) {
		env := newMemoryTurnEnv(t)
		be := env.local(agent_backend_entity.TypeACP, capability.Capabilities{})
		sess := &chat_entity.Session{ID: 313, AgentID: 7}

		req, err := env.turn(sess, memoryAgent(7, true), be, "")
		require.NoError(t, err)
		assert.Equal(t, memoryAgentPrompt, req.SystemPrompt)
		assert.Equal(t, 0, env.memory.callCount())
	})

	t.Run("Given a remote device whose agrctl does not know the memory commands, When the first turn runs, Then nothing is carried", func(t *testing.T) {
		env := newMemoryTurnEnv(t)
		be := env.remote(capability.Capabilities{Set: map[capability.Capability]bool{capability.CapSteer: true}})
		sess := &chat_entity.Session{ID: 314, AgentID: 7}

		req, err := env.turn(sess, memoryAgent(7, true), be, "")
		require.NoError(t, err)
		assert.Equal(t, memoryAgentPrompt, req.SystemPrompt)
		assert.Equal(t, 0, env.memory.callCount())
	})
}

func TestBuildRunRequest_MemorySnapshot_RemoteWithCapabilityMatchesLocal(t *testing.T) {
	localEnv := newMemoryTurnEnv(t)
	localReq, err := localEnv.turn(&chat_entity.Session{ID: 321, AgentID: 7}, memoryAgent(7, true),
		localEnv.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps), "")
	require.NoError(t, err)

	remoteEnv := newMemoryTurnEnv(t)
	remoteReq, err := remoteEnv.turn(&chat_entity.Session{ID: 322, AgentID: 7}, memoryAgent(7, true),
		remoteEnv.remote(fullMemoryCaps), "")
	require.NoError(t, err)

	assert.Equal(t, localReq.SystemPrompt, remoteReq.SystemPrompt, "远程执行带上的内容与本地一致")
	assert.Contains(t, remoteReq.SystemPrompt, "完整用法：agrctl help memory")
}

// 同一台设备上跑着两种后端（这里是 acp 与 Claude Code）：远端 runtime 缓存了两份能力集，
// 判档必须取本轮后端那一份，否则 Claude Code 的会话会随机拿到 acp 的能力集、首回合就冻结成
// 「不带记忆」（spec「远程执行：带上的内容与本地一致」）。
func TestBuildRunRequest_MemorySnapshot_RemoteDeviceWithTwoBackendsUsesThisTurnsBackend(t *testing.T) {
	env := newMemoryTurnEnv(t)
	rec := newRecordingDaemonClient()
	for _, caps := range []capability.Capabilities{
		{Set: map[capability.Capability]bool{capability.CapSteer: true}}, // acp：不认识记忆命令
		fullMemoryCaps, // claudecode
	} {
		rec.expect(wire.MethodCapabilities, func(_, result any) error {
			*(result.(*wire.CapabilitiesResult)) = wire.CapabilitiesResult{Capabilities: caps}
			return nil
		})
	}
	pool := mock_remote_device_svc.NewMockConnPool(env.ctrl)
	lease := mock_remote_device_svc.NewMockLease(env.ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).AnyTimes()
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(rec)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	env.svc.setConnPoolForTest(pool)
	installPairedDevice(env.t, env.ctrl, 7)
	acp := &agent_backend_entity.AgentBackend{ID: 13, Type: string(agent_backend_entity.TypeACP), DeviceFingerprint: testDeviceFingerprint(7)}
	cc := &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: testDeviceFingerprint(7)}

	_, err := env.turn(&chat_entity.Session{ID: 400, AgentID: 7}, memoryAgent(7, true), acp, "")
	require.NoError(t, err)
	// map 的遍历次序是随机的：多跑几条会话，取错的那一份迟早会出现。
	for i := int64(0); i < 24; i++ {
		req, err := env.turn(&chat_entity.Session{ID: 401 + i, AgentID: 7}, memoryAgent(7, true), cc, "")
		require.NoError(t, err)
		require.Contains(t, req.SystemPrompt, "完整用法：agrctl help memory", "会话 %d", 401+i)
	}
}

func TestBuildRunRequest_MemorySnapshot_BuiltinCarriesSummariesOnly(t *testing.T) {
	env := newMemoryTurnEnv(t)
	be := env.local(agent_backend_entity.TypeBuiltin, capability.Capabilities{})
	sess := &chat_entity.Session{ID: 331, AgentID: 7}

	req, err := env.turn(sess, memoryAgent(7, true), be, "")
	require.NoError(t, err)
	assert.Contains(t, req.SystemPrompt, memoryAgentPrompt+"\n\n## 记忆（")
	assert.Contains(t, req.SystemPrompt, "- [m1] 初始·关于用户 (2026-11-01)")
	assert.Contains(t, req.SystemPrompt, "（另有 2 条没带上）")
	assert.NotContains(t, req.SystemPrompt, "agrctl", "内置后端拿不到 agrctl:不带固定说明,也不提命令")
}

func TestBuildRunRequest_MemorySnapshot_SubagentTakesItsOwnSnapshot(t *testing.T) {
	env := newMemoryTurnEnv(t)
	be := env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps)

	parent := &chat_entity.Session{ID: 341, AgentID: 7, ProjectID: 40}
	_, err := env.turn(parent, memoryAgent(7, true), be, "")
	require.NoError(t, err)

	child := &chat_entity.Session{ID: 342, AgentID: 9, ProjectID: 40, Purpose: chat_entity.SessionPurposeSubagent}
	childReq, err := env.turn(child, memoryAgent(9, true), be, "")
	require.NoError(t, err)

	assert.Equal(t, [][2]int64{{7, 40}, {9, 40}}, env.memory.calls, "被调用的 agent 按自己的身份取")
	assert.Contains(t, env.stored[341].MemorySnapshot, "初始·agent7")
	assert.Contains(t, childReq.SystemPrompt, "初始·agent9", "子 agent 会话带的是它自己那层")
	assert.NotContains(t, childReq.SystemPrompt, "初始·agent7", "不沿用发起方会话的快照")
}

func TestBuildRunRequest_MemorySnapshot_FixedLinesFollowUILanguage(t *testing.T) {
	env := newMemoryTurnEnv(t)
	env.settings[app_setting_entity.KeyUILanguage] = app_setting_entity.UILanguageEn
	be := env.local(agent_backend_entity.TypeCodex, fullMemoryCaps)

	req, err := env.turn(&chat_entity.Session{ID: 351, AgentID: 7}, memoryAgent(7, true), be, "")
	require.NoError(t, err)
	assert.Contains(t, req.SystemPrompt, "## Memory (background")
	assert.Contains(t, req.SystemPrompt, "- [m1] 初始·关于用户 (2026-11-01)", "条目原文照搬")
	assert.Contains(t, req.SystemPrompt, "Full usage: agrctl help memory")
	assert.NotContains(t, req.SystemPrompt, "值得记")
}

func TestBuildRunRequest_MemorySnapshot_ReadFailureFailsTheTurn(t *testing.T) {
	env := newMemoryTurnEnv(t)
	be := env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps)
	env.memory.visible = func(int64, int64) (*memory_svc.VisibleMemory, error) { return nil, errors.New("db locked") }

	_, err := env.turn(&chat_entity.Session{ID: 361, AgentID: 7}, memoryAgent(7, true), be, "")
	require.Error(t, err, "读不到记忆时不能悄悄定下一份空快照")
	assert.Equal(t, 0, env.saveHits)
}

func TestBuildRunRequest_NativeAutoMemoryFlagIsLocalOnly(t *testing.T) {
	t.Run("Given Claude Code native memory is turned off, When a local Claude Code turn starts, Then the new process is told to disable it", func(t *testing.T) {
		env := newMemoryTurnEnv(t)
		env.settings[app_setting_entity.KeyClaudeNativeMemory] = "false"
		req, err := env.turn(&chat_entity.Session{ID: 371, AgentID: 7}, memoryAgent(7, true),
			env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps), "")
		require.NoError(t, err)
		assert.True(t, req.DisableNativeAutoMemory)
	})

	t.Run("Given native memory is kept (default), When a local turn starts, Then it is left alone", func(t *testing.T) {
		env := newMemoryTurnEnv(t)
		req, err := env.turn(&chat_entity.Session{ID: 372, AgentID: 7}, memoryAgent(7, true),
			env.local(agent_backend_entity.TypeClaudeCode, fullMemoryCaps), "")
		require.NoError(t, err)
		assert.False(t, req.DisableNativeAutoMemory)
	})

	t.Run("Given native memory is turned off, When the turn executes on a remote device, Then the flag is not set (the setting is this machine's only)", func(t *testing.T) {
		env := newMemoryTurnEnv(t)
		env.settings[app_setting_entity.KeyClaudeNativeMemory] = "false"
		req, err := env.turn(&chat_entity.Session{ID: 373, AgentID: 7}, memoryAgent(7, true), env.remote(fullMemoryCaps), "")
		require.NoError(t, err)
		assert.False(t, req.DisableNativeAutoMemory)
	})
}
