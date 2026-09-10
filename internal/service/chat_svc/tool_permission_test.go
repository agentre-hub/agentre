package chat_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// fakePermRunner 是测试用的 Runtime + ToolPermissionSink + PermissionModeSetter;
// 记录 SubmitToolPermission 与 SetPermissionMode 调用。Run 走空 channel,不发起 turn。
type fakePermRunner struct {
	gotSession    int64
	gotReqID      string
	gotAllow      bool
	gotAlways     bool
	gotDenyReason string
	err           error
	calls         int

	modeCalls   int
	gotModeSess int64
	gotMode     string
	modeErr     error
}

// Capabilities 返与生产 claudecode runtime 一致的 PermissionModeMeta;
// chat_svc 重构后 SetPermissionMode / persistPermissionMode 都读 meta 决定
// "是否支持 + 是否运行时可切",fake 必须给出对应值才能命中目标分支。
func (f *fakePermRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes:         []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			DefaultMode:          "acceptEdits",
			SwitchableDuringTurn: true,
		},
	}
}

func (f *fakePermRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func (f *fakePermRunner) SubmitToolPermission(_ context.Context, sessionID int64, requestID string, allow, alwaysAllowSession bool, denyReason string) error {
	f.calls++
	f.gotSession = sessionID
	f.gotReqID = requestID
	f.gotAllow = allow
	f.gotAlways = alwaysAllowSession
	f.gotDenyReason = denyReason
	return f.err
}

func (f *fakePermRunner) SetPermissionMode(_ context.Context, sessionID int64, mode string) error {
	f.modeCalls++
	f.gotModeSess = sessionID
	f.gotMode = mode
	return f.modeErr
}

func TestAnswerToolPermission(t *testing.T) {
	convey.Convey("AnswerToolPermission", t, func() {
		m := setupChatTest(t)

		convey.Convey("happy path allow once 投递给 sink", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			resp, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID: 42,
				RequestID: "req-bash-1",
				Allow:     true,
			})

			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, 1, fake.calls)
			assert.Equal(t, int64(42), fake.gotSession)
			assert.Equal(t, "req-bash-1", fake.gotReqID)
			assert.True(t, fake.gotAllow)
			assert.False(t, fake.gotAlways)
		})

		convey.Convey("allow + alwaysAllowSession 透传给 sink", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID:          42,
				RequestID:          "req-bash-2",
				Allow:              true,
				AlwaysAllowSession: true,
			})

			assert.NoError(t, err)
			assert.True(t, fake.gotAllow)
			assert.True(t, fake.gotAlways)
		})

		convey.Convey("deny 路径 — 空 denyReason 透传给 sink", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID: 42,
				RequestID: "req-bash-3",
				Allow:     false,
			})
			assert.NoError(t, err)
			assert.False(t, fake.gotAllow)
			assert.Equal(t, "", fake.gotDenyReason, "未填 DenyReason 时透传空字符串，由 runner 兜默认文案")
		})

		convey.Convey("deny 附反馈 — DenyReason 透传给 sink", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			reason := "步骤 2 不必要，改成补 plan-rerun 的 e2e。"
			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID:  42,
				RequestID:  "req-plan-1",
				Allow:      false,
				DenyReason: reason,
			})
			assert.NoError(t, err)
			assert.False(t, fake.gotAllow)
			assert.Equal(t, reason, fake.gotDenyReason)
		})

		convey.Convey("空 sessionID 或 requestID 返 InvalidParameter，不调 sink", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID: 0, RequestID: "x", Allow: true,
			})
			assert.Error(t, err)
			assert.Equal(t, 0, fake.calls)

			_, err = m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID: 1, RequestID: "", Allow: true,
			})
			assert.Error(t, err)
			assert.Equal(t, 0, fake.calls)
		})

		convey.Convey("sink 失败错误透传", func() {
			fake := &fakePermRunner{err: errors.New("waiter missing")}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID: 42, RequestID: "req-x", Allow: true,
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "waiter missing")
		})

		// ExitPlanMode 三选项扩展 —— allow + targetPermissionMode 让后端在 sink 提交
		// 后接力一次 SetPermissionMode,把 CLI 自动切到的 default 推到目标模式。
		convey.Convey("allow + targetPermissionMode=acceptEdits 触发 SetPermissionMode", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
			// persistPermissionMode 路径:再查 session + UpdatePermissionMode。
			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
			m.session.EXPECT().UpdatePermissionMode(m.ctx, int64(42), "acceptEdits").Return(nil)

			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID:            42,
				RequestID:            "req-plan-1",
				Allow:                true,
				TargetPermissionMode: "acceptEdits",
			})
			assert.NoError(t, err)
			assert.Equal(t, 1, fake.calls, "SubmitToolPermission 必须先调")
			assert.Equal(t, 1, fake.modeCalls, "随后接力 SetPermissionMode")
			assert.Equal(t, "acceptEdits", fake.gotMode)
			assert.Equal(t, int64(42), fake.gotModeSess)
		})

		convey.Convey("allow + targetPermissionMode=bypassPermissions 触发 SetPermissionMode", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
			m.session.EXPECT().UpdatePermissionMode(m.ctx, int64(42), "bypassPermissions").Return(nil)

			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID:            42,
				RequestID:            "req-plan-2",
				Allow:                true,
				TargetPermissionMode: "bypassPermissions",
			})
			assert.NoError(t, err)
			assert.Equal(t, 1, fake.modeCalls)
			assert.Equal(t, "bypassPermissions", fake.gotMode)
		})

		convey.Convey("allow + targetPermissionMode=default 不触发 SetPermissionMode (让 CLI 自切)", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID:            42,
				RequestID:            "req-plan-3",
				Allow:                true,
				TargetPermissionMode: "default",
			})
			assert.NoError(t, err)
			assert.Equal(t, 1, fake.calls)
			assert.Equal(t, 0, fake.modeCalls, "default 由 CLI 自切,后端不该再发 control_request")
		})

		convey.Convey("deny + targetPermissionMode 被忽略", func() {
			fake := &fakePermRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
				ID: 42, AgentID: 7, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.AnswerToolPermission(m.ctx, &chat_svc.AnswerToolPermissionRequest{
				SessionID:            42,
				RequestID:            "req-plan-4",
				Allow:                false,
				DenyReason:           "继续规划",
				TargetPermissionMode: "acceptEdits",
			})
			assert.NoError(t, err)
			assert.False(t, fake.gotAllow)
			assert.Equal(t, 0, fake.modeCalls, "deny 时 target 必须被忽略")
		})
	})
}
