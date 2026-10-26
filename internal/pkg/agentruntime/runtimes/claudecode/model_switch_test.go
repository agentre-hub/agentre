package claudecode

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/agents/provider"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

type modelSwitchHandle struct {
	fakeCCHandle
	models []string
	reject bool
}

func (h *modelSwitchHandle) Stream(context.Context, string, []claudecode.Image) (ccStream, error) {
	return &eventCCStream{events: []claudecode.Event{{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}}, {Kind: claudecode.EventDone, Model: "reported"}}}, nil
}

func (h *modelSwitchHandle) SetModel(_ context.Context, value string) error {
	if h.reject {
		return errors.New("Model 'bad' not found")
	}
	h.models = append(h.models, value)
	return nil
}

func TestCLIModelLaunchChoice(t *testing.T) {
	Convey("Given a native backend default, When the session chooses one of three modes, Then launch arguments follow the session without changing process identity", t, func() {
		be := &agent_backend_entity.AgentBackend{Type: "claudecode", DefaultModel: "opus"}
		req := agentruntime.RunRequest{Backend: be}
		identity := launchIdentity(req)
		So(claudeEffectiveModel(req), ShouldEqual, "opus")
		req.CLIModelMode = "cli-default"
		So(claudeEffectiveModel(req), ShouldBeEmpty)
		So(launchIdentity(req), ShouldEqual, identity)
		req.CLIModelMode, req.CLIModel = "fixed", "sonnet"
		So(claudeEffectiveModel(req), ShouldEqual, "sonnet")
		So(launchIdentity(req), ShouldEqual, identity)
	})
}

func TestCLIModelLaunchFailureState(t *testing.T) {
	Convey("Given the process starts but rejects the model before progress, When draining the first turn, Then the original CLI error also invalidates the choice", t, func() {
		h := &fakeCCHandle{stream: &eventCCStream{events: []claudecode.Event{{Kind: claudecode.EventError, Err: errors.New("Model invalid")}, {Kind: claudecode.EventDone}}}}
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) { return h, nil })
		defer restore()
		r := New()
		defer r.CloseAllSessions(context.Background())
		events, result, err := r.Run(context.Background(), agentruntime.RunRequest{Backend: &agent_backend_entity.AgentBackend{Type: "claudecode"}, SessionID: 23, Cwd: t.TempDir(), CLIModelMode: "fixed", CLIModel: "bad"})
		require.NoError(t, err)
		for range events {
		}
		require.ErrorContains(t, result.StopErr, "Model invalid")
		So(r.CLIModelState(context.Background(), 23).Error, ShouldContainSubstring, "Model invalid")
	})
	Convey("Given no live process, When launching the selected model fails, Then the session pill can expose the original error", t, func() {
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) { return nil, errors.New("Model not found at startup") })
		defer restore()
		r := New()
		defer r.CloseAllSessions(context.Background())
		_, _, err := r.Run(context.Background(), agentruntime.RunRequest{Backend: &agent_backend_entity.AgentBackend{Type: "claudecode"}, SessionID: 22, Cwd: t.TempDir(), CLIModelMode: "fixed", CLIModel: "bad"})
		So(err, ShouldNotBeNil)
		So(r.CLIModelState(context.Background(), 22).Error, ShouldContainSubstring, "Model not found at startup")
	})
}

func TestCLIModelSwitch(t *testing.T) {
	Convey("Given a cached native Claude process, When its configured model changes, Then set_model reuses it and rejection prevents the next prompt", t, func() {
		h := &modelSwitchHandle{fakeCCHandle: fakeCCHandle{id: "native", stream: &eventCCStream{events: []claudecode.Event{{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}}, {Kind: claudecode.EventDone, Model: "reported"}}}}}
		spawns := 0
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) { spawns++; return h, nil })
		defer restore()
		r := New()
		defer r.CloseAllSessions(context.Background())
		be := &agent_backend_entity.AgentBackend{Type: "claudecode", DefaultModel: "sonnet"}
		req := agentruntime.RunRequest{Backend: be, SessionID: 10, Cwd: t.TempDir(), UserText: "hello"}
		run := func() error {
			events, _, err := r.Run(context.Background(), req)
			if err == nil {
				for range events {
				}
			}
			return err
		}
		require.NoError(t, run())
		be.DefaultModel = "opus"
		require.NoError(t, run())
		So(spawns, ShouldEqual, 1)
		So(h.models, ShouldResemble, []string{"opus"})
		So(r.CLIModelState(context.Background(), 10).ModelID, ShouldEqual, "reported")
		h.reject = true
		be.DefaultModel = "bad"
		err := run()
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "Model 'bad' not found")
		So(spawns, ShouldEqual, 1)
		So(r.CLIModelState(context.Background(), 10).Error, ShouldContainSubstring, "Model 'bad' not found")
		h.reject = false
		require.NoError(t, r.SetCLIModel(context.Background(), 10, "opus"))
		So(r.CLIModelState(context.Background(), 10).Error, ShouldBeEmpty)
	})
	Convey("Given a busy native process, When a model is selected twice, Then neither selection changes this turn and only the last is applied next turn", t, func() {
		h := &modelSwitchHandle{}
		r := New()
		defer r.CloseAllSessions(context.Background())
		a := &claudeActive{handle: h}
		a.inTurn.Store(true)
		req := agentruntime.RunRequest{Backend: &agent_backend_entity.AgentBackend{Type: "claudecode"}, SessionID: 11, Cwd: t.TempDir(), CLIModelMode: "fixed", CLIModel: "opus"}
		r.cache.PutWithIdentity(sessionKey(11), launchIdentity(req), a)
		setter, ok := any(r).(interface {
			SetCLIModel(context.Context, int64, string) error
		})
		require.True(t, ok, "Claude runtime must implement CLIModelSetter")
		So(setter.SetCLIModel(context.Background(), 11, "sonnet"), ShouldBeNil)
		So(setter.SetCLIModel(context.Background(), 11, "opus"), ShouldBeNil)
		So(h.models, ShouldBeEmpty)
		a.inTurn.Store(false)
		events, _, err := r.Run(context.Background(), req)
		require.NoError(t, err)
		for range events {
		}
		So(h.models, ShouldResemble, []string{"opus"})
	})
}
