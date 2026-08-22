package piagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/piagent/mcpbridge"
	"github.com/agentre-ai/agentre/internal/pkg/cliprocess"
	pkgpiagent "github.com/agentre-ai/agentre/pkg/piagent"
)

func TestPiAgentCapabilities(t *testing.T) {
	Convey("Given pi-agent runtime", t, func() {
		caps := New().Capabilities()

		Convey("When checking supported controls Then it mirrors implemented Pi RPC controls", func() {
			So(caps.Has(capability.CapSteer), ShouldBeTrue)
			So(caps.Has(capability.CapAbort), ShouldBeTrue)
			So(caps.Has(capability.CapImageInput), ShouldBeTrue)
			So(caps.Has(capability.CapCompact), ShouldBeTrue)
			So(caps.Has(capability.CapReportContextWindow), ShouldBeTrue)
			So(caps.Has(capability.CapForkSession), ShouldBeTrue)
			So(caps.Has(capability.CapSetPermission), ShouldBeFalse)
			So(caps.Has(capability.CapCancelSteer), ShouldBeFalse)
			So(caps.Has(capability.CapDrainSteer), ShouldBeFalse)
			So(caps.Has(capability.CapToolPermission), ShouldBeFalse)
			// CapMCPTools=true:pi-agent 经内嵌桥扩展消费 RunRequest.MCPServers。
			So(caps.Has(capability.CapMCPTools), ShouldBeTrue)
		})

		Convey("When comparing optional interfaces Then advertised controls match implementations", func() {
			r := any(New())
			_, steerer := r.(agentruntime.Steerer)
			_, aborter := r.(agentruntime.Aborter)
			_, setter := r.(agentruntime.PermissionModeSetter)
			_, canceler := r.(agentruntime.SteerCanceler)
			_, drainer := r.(agentruntime.SteerDrainer)

			So(steerer, ShouldEqual, caps.Has(capability.CapSteer))
			So(aborter, ShouldEqual, caps.Has(capability.CapAbort))
			So(setter, ShouldEqual, caps.Has(capability.CapSetPermission))
			So(canceler, ShouldEqual, caps.Has(capability.CapCancelSteer))
			So(drainer, ShouldEqual, caps.Has(capability.CapDrainSteer))
		})
	})
}

func TestRun_ForksAndReturnsNativeSessionState(t *testing.T) {
	Convey("Given a resumed Pi session and an exact user-entry fork anchor", t, func() {
		sess := &fakeSession{
			stream: &scriptedStream{anchor: "new-user"},
			sid:    "session-new",
		}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return sess, nil
		})
		defer restore()

		Convey("When the runtime runs the prompt Then it forwards the anchor and returns native session state", func() {
			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID:         1,
				ProviderSessionID: "session-old",
				ForkAnchor:        "fork-user",
				Cwd:               t.TempDir(),
				UserText:          "repeat",
			})
			So(err, ShouldBeNil)
			for range events {
			}

			So(sess.gotForkAnchor, ShouldEqual, "fork-user")
			So(result.ProviderSessionID, ShouldEqual, "session-new")
			So(result.UserAnchor, ShouldEqual, "new-user")
		})
	})
}

func TestTurnRunOptionsRejectWhitespacePaddedForkAnchorWithoutRewriting(t *testing.T) {
	opts, err := turnRunOptions("", nil, &turnSpec{forkAnchor: " fork-user "})

	require.Nil(t, opts)
	require.ErrorContains(t, err, "invalid fork anchor")
}

func TestRun_CanceledAcceptedTurnReturnsSettledUserAnchor(t *testing.T) {
	stream := &acceptedStopStream{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	sess := &fakeSession{
		stream:      stream,
		sid:         "session-accepted",
		interrupter: &acceptedStopInterruptor{stream: stream},
	}
	restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
		return sess, nil
	})
	defer restore()
	runtime := New()
	ctx, cancel := context.WithCancel(context.Background())
	events, result, err := runtime.Run(ctx, agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
		SessionID: 2,
		Cwd:       t.TempDir(),
		UserText:  "stop after acceptance",
	})
	require.NoError(t, err)
	<-stream.started

	cancel()
	_, abortErr := runtime.Abort(context.Background(), 2, 0)
	require.NoError(t, abortErr)
	for range events {
	}

	assert.Equal(t, "pi-user-anchor-after-stop", result.UserAnchor)
	// Stop 走的是 runtime.Abort，drainStream 因此把终态归类成 ErrAborted。
	assert.ErrorIs(t, result.StopErr, agentruntime.ErrAborted)
}

func TestPrepareRunWithholdsPromptUntilStart(t *testing.T) {
	Convey("Given a resumed Pi session with a fork anchor", t, func() {
		lines := []string{
			`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-old"}}`,
			`{"id":"session-fork","type":"response","command":"fork","success":true,"data":{"cancelled":false}}`, //nolint:misspell // Pi RPC field uses British spelling.
			`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-new"}}`,
			`{"id":"session-entries-before","type":"response","command":"get_entries","success":true,"data":{"entries":[],"leafId":null}}`,
			`{"type":"response","command":"prompt","success":true}`,
			`{"type":"agent_end","messages":[],"willRetry":false}`,
			`{"type":"agent_settled"}`,
			`{"id":"session-entries-after","type":"response","command":"get_entries","success":true,"data":{"entries":[{"type":"message","id":"turn-user","parentId":null,"message":{"role":"user"}}],"leafId":"turn-user"}}`,
			`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
		}
		proc := &runtimeRPCProcess{
			stdin:  &cliprocess.LockedBuffer{},
			stdout: strings.NewReader(strings.Join(lines, "\n") + "\n"),
			done:   make(chan error, 1),
		}
		restore := SetSessionFactoryForTest(runtimeRPCSessionFactory(proc))
		defer restore()
		runtime := New()

		Convey("When the service preflights Then the forked ID is available while prompt waits for Start", func() {
			prepared, err := runtime.PrepareRun(context.Background(), agentruntime.RunRequest{
				Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID:         3,
				ProviderSessionID: "session-old",
				ForkAnchor:        "fork-user",
				Cwd:               t.TempDir(),
				UserText:          "commit first",
			})
			So(err, ShouldBeNil)
			identity, ok := prepared.(PreparedRunIdentity)
			So(ok, ShouldBeTrue)
			So(identity.ProviderSessionID(), ShouldEqual, "session-new")
			So(proc.commands(), ShouldResemble, []string{"get_state", "fork", "get_state", "get_entries", "get_session_stats"})

			events, result, err := prepared.Start(context.Background())
			So(err, ShouldBeNil)
			for range events {
			}
			So(result.ProviderSessionID, ShouldEqual, identity.ProviderSessionID())
			So(result.UserAnchor, ShouldEqual, "turn-user")
			So(proc.commands(), ShouldResemble, []string{
				"get_state", "fork", "get_state", "get_entries", "get_session_stats", "prompt", "get_entries", "get_session_stats",
			})
		})
	})
}

func TestPreparedRunCloseBeforeStartSendsNoPrompt(t *testing.T) {
	Convey("Given a prepared Pi fork whose caller abandons ownership", t, func() {
		lines := []string{
			`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-old"}}`,
			`{"id":"session-fork","type":"response","command":"fork","success":true,"data":{"cancelled":false}}`, //nolint:misspell // Pi RPC field uses British spelling.
			`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-new"}}`,
			`{"id":"session-entries-before","type":"response","command":"get_entries","success":true,"data":{"entries":[],"leafId":null}}`,
		}
		proc := &runtimeRPCProcess{
			stdin:  &cliprocess.LockedBuffer{},
			stdout: strings.NewReader(strings.Join(lines, "\n") + "\n"),
			done:   make(chan error, 1),
		}
		restore := SetSessionFactoryForTest(runtimeRPCSessionFactory(proc))
		defer restore()
		prepared, err := New().PrepareRun(context.Background(), agentruntime.RunRequest{
			Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
			SessionID:         4,
			ProviderSessionID: "session-old",
			ForkAnchor:        "fork-user",
			Cwd:               t.TempDir(),
			UserText:          "must not be sent",
		})
		So(err, ShouldBeNil)

		Convey("When Close wins before Start Then cleanup is idempotent and Start stays prompt-free", func() {
			So(prepared.Close(context.Background()), ShouldBeNil)
			So(prepared.Close(context.Background()), ShouldBeNil)
			events, result, startErr := prepared.Start(context.Background())
			So(startErr, ShouldNotBeNil)
			So(events, ShouldBeNil)
			So(result, ShouldBeNil)
			So(proc.commands(), ShouldResemble, []string{"get_state", "fork", "get_state", "get_entries", "get_session_stats"})
		})
	})
}

func TestRun_PreservesCompletedAnswerWhenUserAnchorMetadataFails(t *testing.T) {
	Convey("Given Pi completes the assistant answer but final user-anchor metadata is unavailable", t, func() {
		proc := newRuntimeRPCProcessWithMetadataFailure()
		restore := SetSessionFactoryForTest(runtimeRPCSessionFactory(proc))
		defer restore()

		Convey("When the runtime drains Then it keeps Done and leaves the user anchor empty", func() {
			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID:         2,
				ProviderSessionID: "session-old",
				Cwd:               t.TempDir(),
				UserText:          "hello",
			})
			So(err, ShouldBeNil)

			var text strings.Builder
			done := false
			failed := false
			for event := range events {
				switch event := event.(type) {
				case agentruntime.TextDelta:
					text.WriteString(event.Text)
				case agentruntime.Done:
					done = true
				case agentruntime.ErrorEvent:
					failed = true
				}
			}

			So(text.String(), ShouldEqual, "completed answer")
			So(done, ShouldBeTrue)
			So(failed, ShouldBeFalse)
			So(result.StopErr, ShouldBeNil)
			So(result.ProviderSessionID, ShouldEqual, "session-old")
			So(result.UserAnchor, ShouldBeEmpty)
			So(proc.commands(), ShouldResemble, []string{
				"get_state", "get_entries", "get_session_stats", "prompt", "get_entries", "get_session_stats",
			})
		})
	})
}

func TestDefaultModelForBackend(t *testing.T) {
	Convey("Given a pi-agent backend using ~/.pi/agent config", t, func() {
		Convey("When reasoning_effort is set, then Agentre leaves model empty so pi uses user defaultProvider/defaultModel and thinking stays separate", func() {
			model := defaultModelForBackend(&agent_backend_entity.AgentBackend{
				Type:            string(agent_backend_entity.TypePiAgent),
				ReasoningEffort: "high",
			})

			So(model, ShouldEqual, fallbackModelID)
			So(model, ShouldEqual, "")
		})
	})
}

func TestPiModelFallback(t *testing.T) {
	Convey("Given 未绑 provider(CLI 登录态)的 pi-agent 后端", t, func() {
		Convey("Then 回落默认(空 = pi 用自身配置;#26 override 已移除)", func() {
			m := piModelFallback(agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:            string(agent_backend_entity.TypePiAgent),
					ReasoningEffort: "high",
				},
			})
			So(m, ShouldEqual, "")
		})
	})
}

// TestPiResultModelPlaceholder 锁住 result.Model 在 pi 真实 usage 帧上报前的占位:
// effectiveModel = firstNonEmpty(解析出的 ModelID, backendDefault)。#26 override 已移除,
// 占位只随解析出的 ModelID。
func TestPiResultModelPlaceholder(t *testing.T) {
	Convey("Given pi-agent runtime 且 pi 不报模型(无 usage 帧)", t, func() {
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return &fakeSession{stream: &emptyStream{}, sid: "pi-session"}, nil
		})
		defer restore()

		Convey("When 绑 provider Then 占位 = 解析出的 ModelID", func() {
			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "gpt-5.4", ProviderType: string(llm_provider_entity.TypeOpenAIChat), ProviderKey: "provabc", APIKey: "tok-super-secret"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}
			So(result.Model, ShouldEqual, "gpt-5.4")
		})
	})
}

func TestPiUserModelID_StripsProviderPrefix(t *testing.T) {
	Convey("Given pi-agent 绑 provider 且用户设了会话级模型选择", t, func() {
		bound := agentruntime.RunRequest{
			Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: "provabc", ModelID: "deepseek-v3"},
		}

		Convey("When pi 上报的模型带 agentre-<key>/ 前缀 Then 归一为原始模型 id", func() {
			So(piUserModelID(bound, "agentre-provabc/deepseek-r1"), ShouldEqual, "deepseek-r1")
		})

		Convey("When pi 上报的模型不带前缀(裸 id) Then 原样返回", func() {
			So(piUserModelID(bound, "deepseek-r1"), ShouldEqual, "deepseek-r1")
		})

		Convey("When 上报空模型 Then 原样返回空(不产偏离)", func() {
			So(piUserModelID(bound, ""), ShouldEqual, "")
		})

		Convey("When 未绑 provider 但模型恰好以 agentre- 开头 Then 不剥前缀(避免误伤 CLI 登录态模型名)", func() {
			So(piUserModelID(agentruntime.RunRequest{}, "agentre-custom/foo"), ShouldEqual, "agentre-custom/foo")
		})

		Convey("When 前缀匹配其它 provider 的 key Then 不剥前缀", func() {
			So(piUserModelID(bound, "agentre-otherkey/deepseek-r1"), ShouldEqual, "agentre-otherkey/deepseek-r1")
		})
	})
}

func TestRun_DefaultModelWhenProviderMissing(t *testing.T) {
	Convey("Given pi-agent CLI login runtime", t, func() {
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return &fakeSession{stream: &emptyStream{}, sid: "pi-session"}, nil
		})
		defer restore()

		Convey("When running without provider Then result has Pi default model and session id", func() {
			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}
			So(result.Model, ShouldEqual, fallbackModelID)
			So(result.ProviderSessionID, ShouldEqual, "pi-session")
		})
	})
}

func TestRun_MapsMissingNativeSession(t *testing.T) {
	Convey("Given Pi reports that the requested native session no longer exists", t, func() {
		sess := &fakeSession{streamErr: pkgpiagent.ErrSessionNotFound}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return sess, nil
		})
		defer restore()

		Convey("When the runtime starts the turn Then it returns the backend-neutral sentinel", func() {
			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID:         1,
				ProviderSessionID: "pi-native-gone",
				ForkAnchor:        "fork-user",
				Cwd:               t.TempDir(),
				UserText:          "hello",
			})

			So(events, ShouldBeNil)
			So(result, ShouldBeNil)
			So(errors.Is(err, agentruntime.ErrSessionNotFound), ShouldBeTrue)
			So(sess.gotForkAnchor, ShouldEqual, "fork-user")
			So(sess.closed, ShouldBeTrue)
		})
	})
}

// 一轮结束时会话的去向:干净收尾的留给池(下一轮复用),出了错的连会话一起收掉。
//
// 保守是刻意的 —— 出错之后 RPC 进程处在什么状态无从判断,复用它就是把上一轮的残留
// 带进下一轮。异常路径因此退化成 pi 从前的行为(每轮一个进程)。
func TestRun_GivenDrainedTurn_WhenItEnds_ThenTheSessionSurvivesOnlyIfTheTurnWasClean(t *testing.T) {
	Convey("Given a pi-agent session", t, func() {
		Convey("When 这一轮干净收尾 Then 会话留给下一轮,不关", func() {
			sess := &fakeSession{stream: &emptyStream{}, sid: "pi-session"}
			restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
				return sess, nil
			})
			defer restore()

			pool := agentruntime.NewCLISessionPool(8)
			events, _, err := NewWithPool(pool).Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}

			So(sess.closed, ShouldBeFalse)
			So(pool.IdleLen(), ShouldEqual, 1)
		})

		Convey("When 这一轮出错 Then 会话被收掉,不留给下一轮", func() {
			closed := make(chan struct{})
			sess := &fakeSession{
				stream:       &scriptedStream{err: errors.New("stream failed")},
				sid:          "pi-session",
				closeStarted: closed,
			}
			restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
				return sess, nil
			})
			defer restore()

			pool := agentruntime.NewCLISessionPool(8)
			events, _, err := NewWithPool(pool).Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}

			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("出错的轮没有收掉会话:状态不明的进程会被下一轮捡去用")
			}
			So(pool.Len(), ShouldEqual, 0)
		})
	})
}

func TestRun_StaleCleanupCannotUnregisterNewerGeneration(t *testing.T) {
	firstCloseStarted := make(chan struct{})
	allowFirstClose := make(chan struct{})
	secondRelease := make(chan struct{})
	releaseSecond := sync.OnceFunc(func() { close(secondRelease) })
	defer releaseSecond()
	secondInterrupted := make(chan struct{}, 1)
	// 第一轮刻意以错误收尾:只有不可复用的轮才会连会话一起收掉(干净的轮把会话留给
	// 池),而这个用例要的正是「A 的延迟收尾撞上 B」这个时序。
	first := &fakeSession{
		stream:       &scriptedStream{err: errors.New("first turn failed")},
		sid:          "shared-native-session",
		closeStarted: firstCloseStarted,
		allowClose:   allowFirstClose,
	}
	second := &fakeSession{
		stream:      &blockingStream{release: secondRelease},
		sid:         "shared-native-session",
		interrupter: &recordingInterruptor{called: secondInterrupted},
	}
	var (
		factoryMu sync.Mutex
		created   int
	)
	restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
		factoryMu.Lock()
		defer factoryMu.Unlock()
		created++
		if created == 1 {
			return first, nil
		}
		return second, nil
	})
	defer restore()
	runtime := New()
	req := agentruntime.RunRequest{
		Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
		SessionID:         91,
		ProviderSessionID: "shared-native-session",
		Cwd:               t.TempDir(),
		UserText:          "ordinary resumed turn",
	}

	firstEvents, _, err := runtime.Run(context.Background(), req)
	require.NoError(t, err)
	<-firstCloseStarted

	secondEvents, _, err := runtime.Run(context.Background(), req)
	require.NoError(t, err)
	close(allowFirstClose)
	for range firstEvents {
	}

	_, abortErr := runtime.Abort(context.Background(), req.SessionID, 0)
	require.NoError(t, abortErr,
		"generation A's deferred unregister must not remove generation B")
	select {
	case <-secondInterrupted:
	case <-time.After(time.Second):
		t.Fatal("Abort did not reach the newer generation owner")
	}

	releaseSecond()
	for range secondEvents {
	}
}

// Given 一条会话上有一代已经被更新的一代顶掉, When 陈旧的那一代收尾, Then 新一代
// 手上的 RPC 会话与它的 MCP 配置一个都不能动。
//
// 跨轮复用之后这条更要紧了:两代共用池里同一个会话,陈旧的收尾要是照样 Remove,
// 摘掉的就是新一代正在用的那个进程。
func TestPreparedRun_GivenStaleGeneration_WhenItCloses_ThenTheNewerGenerationKeepsItsSession(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	shared := &fakeSession{stream: &emptyStream{}, sid: "shared-native-session"}
	restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
		return shared, nil
	})
	defer restore()
	pool := agentruntime.NewCLISessionPool(8)
	runtime := NewWithPool(pool)
	req := agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
		SessionID: 92,
		Cwd:       t.TempDir(),
		MCPServers: []agentruntime.MCPServerSpec{{
			Name: "group", URL: "http://127.0.0.1:1/mcp/group/",
		}},
	}

	stalePrepared, err := runtime.PrepareRun(context.Background(), req)
	require.NoError(t, err)
	currentPrepared, err := runtime.PrepareRun(context.Background(), req)
	require.NoError(t, err)
	configPath, err := mcpbridge.RenderConfig(req.MCPServers, req.SessionID)
	require.NoError(t, err)

	require.NoError(t, stalePrepared.Close(context.Background()))

	assert.False(t, shared.closed, "陈旧一代不得关掉新一代还在用的会话")
	assert.Equal(t, 1, pool.Len(), "陈旧一代不得把新一代的池条目摘掉")
	_, err = os.Stat(configPath)
	require.NoError(t, err, "陈旧一代不得删掉新一代的 MCP 配置")

	require.NoError(t, currentPrepared.Close(context.Background()))
	assert.True(t, shared.closed, "属主一代收尾时会话才该被关掉")
	assert.Equal(t, 0, pool.Len())
}

// Given 一条会话渲染过 MCP 桥配置, When 它的 RPC 会话被关掉, Then 配置跟着一起没。
//
// 配置的寿命从「某一轮」改成了「进程」:进程跨轮活着,轮末删配置会把还在用的那份删掉。
func TestClientAdapter_GivenMCPConfig_WhenTheSessionCloses_ThenTheConfigGoesWithIt(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	const chatSessionID = int64(93)
	configPath, err := mcpbridge.RenderConfig([]agentruntime.MCPServerSpec{{
		Name: "group", URL: "http://127.0.0.1:1/mcp/group/",
	}}, chatSessionID)
	require.NoError(t, err)

	adapter := &clientAdapter{
		client:        pkgpiagent.New(),
		chatSessionID: chatSessionID,
		ownsMCPConfig: true,
	}
	require.NoError(t, adapter.Close(context.Background()))

	_, err = os.Stat(configPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRun_ClosesOutputAfterSessionClose(t *testing.T) {
	Convey("Given a pi-agent session whose cleanup is still running", t, func() {
		closeStarted := make(chan struct{})
		allowClose := make(chan struct{})
		// 以错误收尾的轮才会真的关会话(干净的轮把会话留给池),这个用例要的是
		// 「收尾还没返回」这个时刻。
		sess := &fakeSession{
			stream:       &scriptedStream{err: errors.New("turn failed")},
			sid:          "pi-session",
			closeStarted: closeStarted,
			allowClose:   allowClose,
		}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return sess, nil
		})
		defer restore()

		Convey("When the stream has drained Then output remains open until session cleanup returns", func() {
			events, _, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)

			<-closeStarted
			for len(events) > 0 {
				<-events
			}
			select {
			case _, open := <-events:
				if !open {
					t.Fatal("runtime closed output before session cleanup returned")
				}
				t.Fatal("runtime emitted an unexpected event while session cleanup was blocked")
			default:
			}

			close(allowClose)
			for range events {
			}
			So(sess.closed, ShouldBeTrue)
		})
	})
}

func TestRun_ForwardsUserBlockImagesToStream(t *testing.T) {
	Convey("Given a pi-agent turn carrying an inline image block", t, func() {
		sess := &fakeSession{stream: &emptyStream{}, sid: "pi-session"}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return sess, nil
		})
		defer restore()

		Convey("When Run executes Then the image reaches Pi as a multimodal attachment", func() {
			events, _, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "what is this?",
				UserBlocks: []cagoblocks.ContentBlock{
					cagoblocks.TextBlock{Text: "what is this?"},
					cagoblocks.ImageBlock{MediaType: "image/png", Source: cagoblocks.BlobSource{Inline: []byte{1, 2, 3}}},
				},
			})
			So(err, ShouldBeNil)
			for range events {
			}
			So(sess.gotImages, ShouldHaveLength, 1)
			So(sess.gotImages[0].MimeType, ShouldEqual, "image/png")
			So(string(sess.gotImages[0].Data), ShouldEqual, string([]byte{1, 2, 3}))
		})
	})
}

func TestRun_PiFailuresStayRedactedAtStartupAndDownstream(t *testing.T) {
	secrets := []string{
		"private user prompt: inspect acquisition payroll",
		"PRIVATE_IMAGE_SESSION_BYTES",
		"session-entry-private-history",
		"Authorization: Bearer private-provider-token",
		"stderr private process payload",
		"command failed with private process arguments",
	}
	imageWire := base64.StdEncoding.EncodeToString([]byte(secrets[1]))
	commonStartup := []string{
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-session-689"}}`,
		`{"id":"session-entries-before","type":"response","command":"get_entries","success":true,"data":{"entries":[{"type":"message","id":"before-leaf","parentId":null,"message":{"role":"user","content":"` + secrets[2] + `"}}],"leafId":"before-leaf"}}`,
	}
	tests := []struct {
		name            string
		lines           []string
		stderr          string
		waitErr         error
		startupError    bool
		wantDiagnostics bool
		wantUsage       bool
		wantErrorType   string
	}{
		{
			name: "RPC failure payload",
			lines: append(append([]string{}, commonStartup...),
				`{"type":"response","command":"prompt","success":false,"error":"`+secrets[0]+` | `+secrets[3]+`","data":{"message":"`+secrets[2]+`"}}`,
			),
			startupError: true,
		},
		{
			name: "terminal event failure payload",
			lines: append(append([]string{}, commonStartup...),
				`{"type":"response","command":"prompt","success":true}`,
				`{"type":"message_end","message":{"role":"assistant","content":"`+secrets[2]+`","model":"gpt-5.5(xhigh)","usage":{"input":4017,"output":128,"cacheRead":69632,"cacheWrite":0}}}`,
				`{"type":"agent_end","messages":[{"role":"assistant","content":"`+secrets[2]+`","stopReason":"error","errorMessage":"`+secrets[0]+` | `+secrets[3]+`"}],"willRetry":false}`,
				`{"type":"agent_settled"}`,
				`{"id":"session-entries-after","type":"response","command":"get_entries","success":true,"data":{"entries":[{"type":"message","id":"before-leaf","parentId":null,"message":{"role":"user"}},{"type":"message","id":"turn-user","parentId":"before-leaf","message":{"role":"user","content":"`+secrets[0]+`","images":[{"data":"`+imageWire+`"}]}}],"leafId":"turn-user"}}`,
				`{"type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"contextWindow":1050000}}}`,
			),
			stderr:          secrets[4],
			wantDiagnostics: true,
			wantUsage:       true,
			wantErrorType:   "*errors.errorString",
		},
		{
			name: "process exit and stderr payload",
			lines: append(append([]string{}, commonStartup...),
				`{"type":"response","command":"prompt","success":true}`,
			),
			stderr:  secrets[4] + " | " + secrets[3],
			waitErr: errors.New(secrets[5]),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &runtimeRPCProcess{
				stdin:  &cliprocess.LockedBuffer{},
				stdout: strings.NewReader(strings.Join(tt.lines, "\n") + "\n"),
				stderr: strings.NewReader(tt.stderr),
				done:   make(chan error, 1),
			}
			if tt.waitErr != nil {
				proc.done <- tt.waitErr
			}
			restoreFactory := SetSessionFactoryForTest(runtimeRPCSessionFactory(proc))
			defer restoreFactory()
			core, logs := observer.New(zapcore.DebugLevel)
			ctx := logger.WithContextLogger(context.Background(), zap.New(core))

			events, result, err := New().Run(ctx, agentruntime.RunRequest{
				Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID:         689,
				AgentID:           8,
				ProviderSessionID: "pi-session-689",
				Cwd:               t.TempDir(),
				UserText:          secrets[0],
				UserBlocks: []cagoblocks.ContentBlock{
					cagoblocks.TextBlock{Text: secrets[0]},
					cagoblocks.ImageBlock{MediaType: "image/png", Source: cagoblocks.BlobSource{Inline: []byte(secrets[1])}},
				},
			})
			if tt.startupError {
				require.Error(t, err)
				assert.Nil(t, events)
				assert.Nil(t, result)
				assert.Equal(t, "piagent rpc prompt failed", err.Error())
				for _, secret := range append(secrets, imageWire) {
					assert.NotContains(t, err.Error(), secret)
				}
			} else {
				require.NoError(t, err)

				var downstreamErr error
				for event := range events {
					if failure, ok := event.(agentruntime.ErrorEvent); ok {
						downstreamErr = failure.Err
					}
				}
				require.Error(t, downstreamErr)
				require.Error(t, result.StopErr)
				assert.Equal(t, downstreamErr.Error(), result.StopErr.Error())
				for _, secret := range append(secrets, imageWire) {
					assert.NotContains(t, downstreamErr.Error(), secret)
					assert.NotContains(t, result.StopErr.Error(), secret)
				}
				if tt.waitErr != nil {
					var exitErr *pkgpiagent.ExitError
					require.ErrorAs(t, result.StopErr, &exitErr)
					assert.Empty(t, exitErr.Stderr)
					for _, secret := range secrets {
						assert.NotContains(t, exitErr.Err.Error(), secret)
					}
				}
			}

			for _, entry := range logs.All() {
				for _, value := range entry.ContextMap() {
					for _, secret := range append(secrets, imageWire) {
						assert.NotContains(t, fmt.Sprint(value), secret)
					}
				}
			}
			matches := logs.FilterMessage("piagent.drainStream: turn failed").All()
			diagnostics := logs.FilterMessage("piagent.logPiFailureDiagnostics: turn failed diagnostics").All()
			if tt.startupError {
				assert.Empty(t, matches, "a rejected prompt must fail before a turn is registered")
				assert.Empty(t, diagnostics)
				return
			}
			require.Len(t, matches, 1)
			fields := matches[0].ContextMap()
			assert.Equal(t, int64(689), fields["sessionID"])
			assert.Equal(t, int64(8), fields["agentID"])
			assert.Equal(t, "pi-session-689", fields["providerSessionID"])
			_, hasRawError := fields["error"]
			assert.False(t, hasRawError)
			if tt.wantErrorType != "" {
				assert.Equal(t, tt.wantErrorType, fields["errorClass"])
			}
			if tt.wantUsage {
				assert.Equal(t, int64(1050000), fields["contextWindow"])
				assert.Equal(t, int64(4017), fields["promptTokens"])
				assert.Equal(t, int64(128), fields["completionTokens"])
				assert.Equal(t, int64(69632), fields["cachedTokens"])
				assert.Equal(t, int64(0), fields["cacheCreationTokens"])
				assert.Equal(t, int64(73649), fields["totalInputTokens"])
			}
			if tt.wantDiagnostics {
				require.Len(t, diagnostics, 1)
				diagnosticFields := diagnostics[0].ContextMap()
				assert.Equal(t, "agent_end", diagnosticFields["piEventType"])
				assert.Equal(t, "error", diagnosticFields["piStopReason"])
			} else {
				assert.Empty(t, diagnostics)
			}
		})
	}
}

func TestPiRawFrameSinkRedactsPayload(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	oldLogger := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(oldLogger) })

	sink := piRawFrameSink(689, "pi-session-689")
	validFrame := []byte(`{"type":"tool_execution_start","toolCallId":"outer-safe-id","args":{"secret":"SENTINEL_PI_RAW_FRAME"}}`)
	malformedFrame := []byte(`{"type":"message_update","payload":"SENTINEL_PI_MALFORMED_FRAME"`)
	untrustedKindFrame := []byte(`{"type":"SENTINEL_PI_UNTRUSTED_KIND","payload":"ignored"}`)
	sink(validFrame)
	sink(malformedFrame)
	sink(untrustedKindFrame)

	captured := observedPiLogText(logs)
	assert.NotContains(t, captured, "SENTINEL_PI_RAW_FRAME")
	assert.NotContains(t, captured, "SENTINEL_PI_MALFORMED_FRAME")
	assert.NotContains(t, captured, "SENTINEL_PI_UNTRUSTED_KIND")
	entries := logs.FilterMessage("piagent.piRawFrameSink: frame observed").All()
	require.Len(t, entries, 3)
	assert.Equal(t, "tool_execution_start", entries[0].ContextMap()["frameType"])
	assert.Equal(t, int64(len(validFrame)), entries[0].ContextMap()["frameBytes"])
	assert.Equal(t, true, entries[1].ContextMap()["parseFailed"])
	assert.Equal(t, int64(len(malformedFrame)), entries[1].ContextMap()["frameBytes"])
	assert.NotContains(t, entries[2].ContextMap(), "frameType")
	assert.Equal(t, int64(len(untrustedKindFrame)), entries[2].ContextMap()["frameBytes"])
}

func TestRun_LogsPiStreamFailureDiagnostics(t *testing.T) {
	Convey("Given a pi-agent stream that fails with payload-bearing diagnostics", t, func() {
		boom := errors.New("SENTINEL_PI_RUN_ERROR")
		diagnostics := pkgpiagent.StreamDiagnostics{
			FinalErrorEventType:  "agent_end",
			FinalErrorStopReason: "error",
			FinalErrorMessage:    "SENTINEL_PI_FINAL_ERROR_MESSAGE",
			FinalErrorFrame:      `{"type":"agent_end","payload":"SENTINEL_PI_FINAL_ERROR_FRAME"}`,
			StderrTail:           "SENTINEL_PI_STDERR_TAIL",
		}
		sess := &fakeSession{
			stream: &scriptedStream{events: []pkgpiagent.Event{
				{Kind: pkgpiagent.EventUsage, Model: "gpt-5.5(xhigh)", Usage: provider.Usage{
					PromptTokens:        4017,
					CompletionTokens:    128,
					CachedTokens:        69632,
					CacheCreationTokens: 0,
				}},
				{Kind: pkgpiagent.EventContextWindow, ContextWindow: 1050000},
				{Kind: pkgpiagent.EventError, Err: boom},
			}, err: boom, sid: "pi-session-689", diagnostics: diagnostics},
			sid: "pi-session-689",
		}
		restoreFactory := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return sess, nil
		})
		defer restoreFactory()
		core, logs := observer.New(zapcore.DebugLevel)
		ctx := logger.WithContextLogger(context.Background(), zap.New(core))

		Convey("When the turn drains Then diagnostics remain available to the result stream but not operational logs", func() {
			events, result, err := New().Run(ctx, agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID: 689,
				AgentID:   8,
				Cwd:       t.TempDir(),
				UserText:  "检查一下pi agent能否支持mcp，实现群聊功能",
			})
			So(err, ShouldBeNil)
			var streamedErr error
			for event := range events {
				if errorEvent, ok := event.(agentruntime.ErrorEvent); ok {
					streamedErr = errorEvent.Err
				}
			}

			So(result.StopErr, ShouldEqual, boom)
			So(result.Model, ShouldEqual, "gpt-5.5(xhigh)")
			So(streamedErr, ShouldEqual, boom)
			matches := logs.FilterMessage("piagent.drainStream: turn failed").All()
			So(matches, ShouldHaveLength, 1)
			fields := matches[0].ContextMap()
			So(fields["sessionID"], ShouldEqual, int64(689))
			So(fields["agentID"], ShouldEqual, int64(8))
			So(fields["providerSessionID"], ShouldEqual, "pi-session-689")
			So(fields["contextWindow"], ShouldEqual, int64(1050000))
			So(fields["promptTokens"], ShouldEqual, int64(4017))
			So(fields["completionTokens"], ShouldEqual, int64(128))
			So(fields["cachedTokens"], ShouldEqual, int64(69632))
			So(fields["cacheCreationTokens"], ShouldEqual, int64(0))
			So(fields["totalInputTokens"], ShouldEqual, int64(73649))
			So(fields["errorClass"], ShouldEqual, "*errors.errorString")
			So(fields["errorBytes"], ShouldEqual, int64(len(boom.Error())))

			diagnosticMatches := logs.FilterMessage("piagent.logPiFailureDiagnostics: turn failed diagnostics").All()
			So(diagnosticMatches, ShouldHaveLength, 1)
			diagnosticFields := diagnosticMatches[0].ContextMap()
			So(diagnosticFields["piEventType"], ShouldEqual, "agent_end")
			So(diagnosticFields["piStopReason"], ShouldEqual, "error")
			// pkg/piagent 只把已脱敏的最终错误帧交出来；错误正文与 stderr 尾巴不再
			// 进入 Diagnostics，因此诊断日志只报帧体量。
			So(diagnosticFields["piFinalErrorFrameBytes"], ShouldEqual, int64(len(diagnostics.FinalErrorFrame)))
			So(diagnosticFields, ShouldNotContainKey, "piErrorMessageBytes")
			So(diagnosticFields, ShouldNotContainKey, "piStderrBytes")

			captured := observedPiLogText(logs)
			assert.NotContains(t, captured, boom.Error())
			assert.NotContains(t, captured, diagnostics.FinalErrorMessage)
			assert.NotContains(t, captured, "SENTINEL_PI_FINAL_ERROR_FRAME")
			assert.NotContains(t, captured, diagnostics.StderrTail)
		})
	})
}

func observedPiLogText(logs *observer.ObservedLogs) string {
	var out strings.Builder
	for _, entry := range logs.All() {
		_, _ = fmt.Fprintf(&out, "%s %v\n", entry.Message, entry.ContextMap())
	}
	return out.String()
}

func TestRun_ProviderInjectsAPIKeyEnvAndBareModel(t *testing.T) {
	Convey("Given a turn bound to a custom LLM provider", t, func() {
		prov := &agentruntime.EffectiveLLMConfig{
			ProviderKey: "provabc", APIKey: "tok-super-secret", ModelID: "deepseek-v3",
			ProviderType: string(llm_provider_entity.TypeAnthropic),
		}
		var gotEnv map[string]string
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, env map[string]string, _ string) (sessionHandle, error) {
			gotEnv = env
			return &fakeSession{stream: &emptyStream{}, sid: "pi-session"}, nil
		})
		defer restore()

		Convey("When running Then the APIKey reaches the factory env and result.Model stays bare", func() {
			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				Effective: prov,
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}
			So(gotEnv, ShouldNotBeNil)
			So(gotEnv[agentruntime.PiAgentProviderEnvKey(prov.ProviderKey)], ShouldEqual, "tok-super-secret")
			// result.Model 保持裸解析出的 ModelID，不加 agentre-<key>/ 前缀。
			So(result.Model, ShouldEqual, "deepseek-v3")
		})
	})
}

func TestRun_ProviderAPIKeyEmpty_ReturnsConfigErrorWithoutSpawning(t *testing.T) {
	Convey("Given a bound provider with an empty APIKey", t, func() {
		spawned := false
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			spawned = true
			return &fakeSession{stream: &emptyStream{}, sid: "pi-session"}, nil
		})
		defer restore()

		Convey("When running Then Run returns a config error naming the provider and never spawns", func() {
			_, _, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: "provx", APIKey: "", ModelID: "m1"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "provx")
			So(spawned, ShouldBeFalse)
		})
	})
}

func TestRun_NativeEffectiveConfig_UsesCLILoginWithoutProviderAPIKey(t *testing.T) {
	Convey("Given 跟随 Agent 绑定解析为 native effective config", t, func() {
		var gotEnv map[string]string
		spawned := false
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, env map[string]string, _ string) (sessionHandle, error) {
			spawned = true
			gotEnv = env
			return &fakeSession{stream: &emptyStream{}, sid: "pi-session"}, nil
		})
		defer restore()

		Convey("When running Then Pi uses CLI login without requiring or injecting a provider API key", func() {
			events, _, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				Effective: &agentruntime.EffectiveLLMConfig{Mode: agentruntime.EffectiveModeNative},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}
			So(spawned, ShouldBeTrue)
			for k := range gotEnv {
				So(strings.HasPrefix(k, "AGENTRE_PI_API_KEY_"), ShouldBeFalse)
			}
		})
	})
}

func TestRun_NoProvider_NoEnvInjection(t *testing.T) {
	Convey("Given an unbound pi-agent backend", t, func() {
		var gotEnv map[string]string
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, env map[string]string, _ string) (sessionHandle, error) {
			gotEnv = env
			return &fakeSession{stream: &emptyStream{}, sid: "pi-session"}, nil
		})
		defer restore()

		Convey("When running Then no provider env key is injected and model stays default", func() {
			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}
			So(gotEnv, ShouldNotBeNil)
			for k := range gotEnv {
				So(strings.HasPrefix(k, "AGENTRE_PI_API_KEY_"), ShouldBeFalse)
			}
			So(result.Model, ShouldEqual, fallbackModelID)
		})
	})
}

func TestProviderRunConfig(t *testing.T) {
	Convey("Given a bound provider and a stubbed extension writer", t, func() {
		restore := SetProviderExtensionWriterForTest(func(string) (string, error) {
			return "/ext/agentre-provider-abc.mjs", nil
		})
		defer restore()

		Convey("When assembling the session config Then model is agentre-<key>/<model> and extension path is returned", func() {
			model, extPath, err := providerRunConfig(&agentruntime.EffectiveLLMConfig{
				ProviderKey: "provabc", ModelID: "deepseek-v3", ProviderType: string(llm_provider_entity.TypeOpenAIChat),
			})
			So(err, ShouldBeNil)
			So(model, ShouldEqual, "agentre-provabc/deepseek-v3")
			So(extPath, ShouldEqual, "/ext/agentre-provider-abc.mjs")
		})
	})

	// 注入 pi 的 provider 扩展只 registerProvider 一个 models 条目(见
	// agentruntime.PiAgentProviderExtension)。--model 与扩展的 models 列表必须来自
	// **同一个** effectiveModel(解析出的 ModelID):扩展按内容哈希落盘,不同模型天然是
	// 不同文件。
	Convey("Given a bound provider whose extension source is captured", t, func() {
		var source string
		restore := SetProviderExtensionWriterForTest(func(s string) (string, error) {
			source = s
			return "/ext/agentre-provider-abc.mjs", nil
		})
		defer restore()

		prov := func() *agentruntime.EffectiveLLMConfig {
			return &agentruntime.EffectiveLLMConfig{
				ProviderKey: "provabc", ModelID: "deepseek-v3",
				ProviderType: string(llm_provider_entity.TypeOpenAIChat), ContextWindow: 128000,
			}
		}

		Convey("Then the extension registers 解析出的 ModelID, matching --model", func() {
			model, _, err := providerRunConfig(prov())
			So(err, ShouldBeNil)
			So(model, ShouldEqual, "agentre-provabc/deepseek-v3")
			So(source, ShouldContainSubstring, `id: "deepseek-v3"`)
			// provider 的其余元数据(contextWindow 等)仍随扩展下发。
			So(source, ShouldContainSubstring, "contextWindow: 128000")
		})
	})

	Convey("Given a bound provider and a stubbed extension writer", t, func() {
		restore := SetProviderExtensionWriterForTest(func(string) (string, error) {
			return "/ext/agentre-provider-abc.mjs", nil
		})
		defer restore()

		Convey("When the provider is nil Then zero values are returned without error", func() {
			model, extPath, err := providerRunConfig(nil)
			So(err, ShouldBeNil)
			So(model, ShouldEqual, "")
			So(extPath, ShouldEqual, "")
		})

		Convey("When the provider model is empty Then no model or extension is produced", func() {
			model, extPath, err := providerRunConfig(&agentruntime.EffectiveLLMConfig{
				ProviderKey: "provabc", ProviderType: string(llm_provider_entity.TypeOpenAIChat),
			})
			So(err, ShouldBeNil)
			So(model, ShouldEqual, "")
			So(extPath, ShouldEqual, "")
		})

		Convey("When the provider type is unsupported Then an error is returned instead of silently running unbound", func() {
			_, _, err := providerRunConfig(&agentruntime.EffectiveLLMConfig{
				ProviderKey: "provabc", ModelID: "deepseek-v3", ProviderType: "deepseek",
			})
			So(err, ShouldNotBeNil)
		})
	})

	Convey("Given a failing extension writer", t, func() {
		restore := SetProviderExtensionWriterForTest(func(string) (string, error) {
			return "", errors.New("disk full")
		})
		defer restore()

		Convey("When materializing Then the error propagates", func() {
			_, _, err := providerRunConfig(&agentruntime.EffectiveLLMConfig{
				ProviderKey: "provabc", ModelID: "deepseek-v3", ProviderType: string(llm_provider_entity.TypeOpenAIChat),
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "disk full")
		})
	})
}

type fakeSession struct {
	stream           stream
	sid              string
	interrupter      interruptable
	gotImages        []pkgpiagent.Image
	gotPrompt        string
	gotForkAnchor    string
	streamCall       int
	streamErr        error
	closed           bool
	closeStarted     chan struct{}
	closeStartedOnce sync.Once
	allowClose       <-chan struct{}
}

// Close 幂等:真适配器的 Close 也是(失败路径同步关一次、池的收尾又异步关一次),
// 替身不跟上就会在第二次调用时 panic。
func (s *fakeSession) Close(context.Context) error {
	if s.closeStarted != nil {
		s.closeStartedOnce.Do(func() { close(s.closeStarted) })
	}
	if s.allowClose != nil {
		<-s.allowClose
	}
	s.closed = true
	return nil
}
func (s *fakeSession) ID() string { return s.sid }
func (s *fakeSession) Stream(_ context.Context, prompt, _ string, images []pkgpiagent.Image) (stream, error) {
	s.streamCall++
	s.gotPrompt = prompt
	s.gotImages = images
	return s.stream, s.streamErr
}
func (s *fakeSession) StreamTurn(ctx context.Context, prompt, mode string, images []pkgpiagent.Image, turn turnSpec) (stream, error) {
	s.gotForkAnchor = turn.forkAnchor
	return s.Stream(ctx, prompt, mode, images)
}
func (s *fakeSession) Compact(context.Context) (stream, error)          { return s.stream, s.streamErr }
func (s *fakeSession) RewindTo(context.Context, string) (string, error) { return s.sid, nil }
func (s *fakeSession) ActiveStream() steerStream                        { return nil }
func (s *fakeSession) ActiveInterruptor() interruptable                 { return s.interrupter }

type recordingInterruptor struct {
	called chan<- struct{}
}

func (i *recordingInterruptor) Interrupt(context.Context) error {
	select {
	case i.called <- struct{}{}:
	default:
	}
	return nil
}

type acceptedStopStream struct {
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	mu        sync.Mutex
	anchor    string
}

func (s *acceptedStopStream) Next() bool {
	s.startOnce.Do(func() { close(s.started) })
	<-s.release
	return false
}
func (*acceptedStopStream) Event() pkgpiagent.Event { return pkgpiagent.Event{} }
func (*acceptedStopStream) SessionID() string       { return "session-accepted" }
func (*acceptedStopStream) Err() error              { return context.Canceled }
func (s *acceptedStopStream) UserAnchor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.anchor
}
func (s *acceptedStopStream) settle() {
	s.mu.Lock()
	s.anchor = "pi-user-anchor-after-stop"
	s.mu.Unlock()
	s.stopOnce.Do(func() { close(s.release) })
}

type acceptedStopInterruptor struct {
	stream *acceptedStopStream
}

func (i *acceptedStopInterruptor) Interrupt(context.Context) error {
	i.stream.settle()
	return nil
}

type blockingStream struct {
	release <-chan struct{}
}

func (s *blockingStream) Next() bool {
	<-s.release
	return false
}
func (*blockingStream) Event() pkgpiagent.Event { return pkgpiagent.Event{} }
func (*blockingStream) SessionID() string       { return "" }
func (*blockingStream) Err() error              { return nil }

type emptyStream struct{}

func (*emptyStream) Next() bool              { return false }
func (*emptyStream) Event() pkgpiagent.Event { return pkgpiagent.Event{} }
func (*emptyStream) SessionID() string       { return "" }
func (*emptyStream) Err() error              { return nil }

type scriptedStream struct {
	events      []pkgpiagent.Event
	idx         int
	err         error
	sid         string
	anchor      string
	diagnostics pkgpiagent.StreamDiagnostics
}

func (s *scriptedStream) Next() bool {
	if s.idx >= len(s.events) {
		return false
	}
	s.idx++
	return true
}

func (s *scriptedStream) Event() pkgpiagent.Event { return s.events[s.idx-1] }
func (s *scriptedStream) SessionID() string       { return s.sid }
func (s *scriptedStream) Err() error              { return s.err }
func (s *scriptedStream) UserAnchor() string      { return s.anchor }
func (s *scriptedStream) Diagnostics() pkgpiagent.StreamDiagnostics {
	return s.diagnostics
}

type runtimeRPCRunner struct {
	proc cliprocess.Handle
}

func (r *runtimeRPCRunner) Start(context.Context, cliprocess.Options) (cliprocess.Handle, error) {
	return r.proc, nil
}

type runtimeRPCProcess struct {
	stdin  *cliprocess.LockedBuffer
	stdout io.Reader
	stderr io.Reader
	done   chan error
}

func newRuntimeRPCProcessWithMetadataFailure() *runtimeRPCProcess {
	lines := []string{
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-old"}}`,
		`{"id":"session-entries-before","type":"response","command":"get_entries","success":true,"data":{"entries":[{"type":"message","id":"before-leaf","parentId":null,"message":{"role":"assistant"}}],"leafId":"before-leaf"}}`,
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"completed answer"}}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"id":"session-entries-after","type":"response","command":"get_entries","success":false,"error":"metadata unavailable"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
	}
	return &runtimeRPCProcess{
		stdin:  &cliprocess.LockedBuffer{},
		stdout: strings.NewReader(strings.Join(lines, "\n") + "\n"),
		done:   make(chan error, 1),
	}
}

func runtimeRPCSessionFactory(proc *runtimeRPCProcess) func(agentruntime.RunRequest, map[string]string, string) (sessionHandle, error) {
	return func(req agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
		client := pkgpiagent.New(
			pkgpiagent.WithRPCProcessRunnerForTesting(&runtimeRPCRunner{proc: proc}),
			pkgpiagent.WithSession(req.ProviderSessionID),
		)
		return &clientAdapter{client: client, sid: req.ProviderSessionID}, nil
	}
}

func (p *runtimeRPCProcess) Stdin() io.Writer  { return p.stdin }
func (p *runtimeRPCProcess) Stdout() io.Reader { return p.stdout }
func (p *runtimeRPCProcess) Stderr() io.Reader {
	if p.stderr != nil {
		return p.stderr
	}
	return strings.NewReader("")
}
func (p *runtimeRPCProcess) Wait() error { return <-p.done }
func (p *runtimeRPCProcess) Kill() error { return p.finish() }
func (p *runtimeRPCProcess) Signal(os.Signal) error {
	return p.finish()
}

func (p *runtimeRPCProcess) finish() error {
	select {
	case p.done <- nil:
	default:
	}
	return nil
}

func (p *runtimeRPCProcess) commands() []string {
	frames := p.stdinFrames()
	out := make([]string, 0, len(frames))
	for _, frame := range frames {
		if command, ok := frame["type"].(string); ok {
			out = append(out, command)
		}
	}
	return out
}

func (p *runtimeRPCProcess) stdinFrames() []map[string]any {
	lines := strings.Split(strings.TrimSpace(p.stdin.String()), "\n")
	frames := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var frame map[string]any
		if json.Unmarshal([]byte(line), &frame) == nil {
			frames = append(frames, frame)
		}
	}
	return frames
}

// TestRun_WebInitiatedFreeSessionResolvesCwdFromSyncID 与 claudecode 那条同源(2026-08-22
// 的 AgentCwd(0) 报错):web 发起的对话在这里同样是 AgentID=0 + Cwd 空,兜底目录改由
// 账号级 AgentSyncID 定。
func TestRun_WebInitiatedFreeSessionResolvesCwdFromSyncID(t *testing.T) {
	Convey("Given 一条 web 发起的自由会话:AgentID=0、Cwd 空、只带账号级 AgentSyncID", t, func() {
		dataDir := t.TempDir()
		t.Setenv("AGENTRE_DATA_DIR", dataDir)

		var gotCwd string
		restore := SetSessionFactoryForTest(
			func(_ agentruntime.RunRequest, _ map[string]string, cwd string) (sessionHandle, error) {
				gotCwd = cwd
				return &fakeSession{stream: &scriptedStream{anchor: "new-user"}, sid: "session-new"}, nil
			})
		defer restore()

		Convey("When 起这一轮, Then 起得来,工作目录落在该 Agent 的账号级同步标识下", func() {
			events, _, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}",
				},
				SessionID:   1,
				AgentID:     0,
				AgentSyncID: "01KZNE7YKJQ6A79YVDCMW1A63R",
				UserText:    "hi",
			})
			So(err, ShouldBeNil)
			for range events { //nolint:revive // drain
			}
			So(gotCwd, ShouldEqual,
				filepath.Join(dataDir, "agents", "sync-01KZNE7YKJQ6A79YVDCMW1A63R"))
		})
	})
}

// Given 一个 pi 轮已经把 RPC 进程开起来了, When 宿主关机, Then 这个进程要被收掉。
//
// pi 的进程不进 CLISessionPool,而桌面退出路径只扫池:确认退出时正在跑的 pi 轮,
// 收尾靠的是 Start 那个 goroutine 里的 defer Close —— 桌面进程先它一步退出,而 pi
// 自带进程组、不会被连坐,于是留下一个孤儿。agentred 那边有 Pi generation 清理兜着,
// 桌面这边没有。
func TestCloseAllSessions_GivenInFlightRun_WhenHostShutsDown_ThenTheRPCProcessIsReleased(t *testing.T) {
	Convey("Given 一个已经开起来的 pi 轮", t, func() {
		sess := &fakeSession{stream: &scriptedStream{}, sid: "session-live"}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return sess, nil
		})
		defer restore()

		r := New()
		_, err := r.PrepareRun(context.Background(), agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
			SessionID: 11,
			Cwd:       t.TempDir(),
			UserText:  "hello",
		})
		So(err, ShouldBeNil)

		Convey("When 宿主关机 Then 它的 RPC 进程被收掉", func() {
			r.CloseAllSessions(context.Background())
			So(sess.closed, ShouldBeTrue)
		})
	})
}

// Given 一条会话被删除, When 释放广播扫过每个 runtime, Then pi 也要放掉它在飞的那一轮 ——
// 会话都没了,这个进程再也不会有人用。
func TestCloseSession_GivenDeletedSession_WhenReleasing_ThenOnlyThatSessionIsReleased(t *testing.T) {
	Convey("Given 两条会话各有一轮在飞", t, func() {
		sessions := map[int64]*fakeSession{
			21: {stream: &scriptedStream{}, sid: "session-21"},
			22: {stream: &scriptedStream{}, sid: "session-22"},
		}
		restore := SetSessionFactoryForTest(func(req agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return sessions[req.SessionID], nil
		})
		defer restore()

		r := New()
		for id := range sessions {
			_, err := r.PrepareRun(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
				SessionID: id,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
		}

		Convey("When 只删掉其中一条 Then 只有它被放掉", func() {
			r.CloseSession(context.Background(), 21)
			So(sessions[21].closed, ShouldBeTrue)
			So(sessions[22].closed, ShouldBeFalse)
		})
	})
}

// Given 一条会话跑完了一轮, When 同一条会话再来一轮, Then 复用池里那个 RPC 会话,
// 不再新建 —— 这正是 claudecode / codex 早就有的跨轮复用。
//
// pi 此前每轮一个进程、轮末就关:每轮开头都白付一次进程启动 + 扩展加载,而它的启动
// 参数(--session/--append-system-prompt/--model/--thinking/--extension)逐轮不变。
func TestRun_GivenIdleSessionInPool_WhenRunningAgain_ThenTheRPCSessionIsReused(t *testing.T) {
	Convey("Given 一条 pi 会话已经跑过一轮", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		created := 0
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			created++
			return &fakeSession{stream: &scriptedStream{}, sid: "session-1"}, nil
		})
		defer restore()

		r := NewWithPool(pool)
		req := agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}"},
			SessionID: 31,
			Cwd:       t.TempDir(),
			UserText:  "one",
		}
		events, _, err := r.Run(context.Background(), req)
		So(err, ShouldBeNil)
		for range events {
		}

		Convey("When 同一条会话再来一轮 Then 复用池里那个,不再新建", func() {
			req.UserText = "two"
			events, _, err := r.Run(context.Background(), req)
			So(err, ShouldBeNil)
			for range events {
			}

			So(created, ShouldEqual, 1)
			So(pool.Len(), ShouldEqual, 1)
		})
	})
}

// Given 池里留着一条会话, When 下一轮的启动身份变了(换模型), Then 旧的被收掉、重新
// spawn —— pi 的模型是 --model 启动参数,复用旧进程就是拿旧模型跑新一轮。
func TestRun_GivenLaunchIdentityChanged_WhenRunningAgain_ThenTheOldSessionIsEvicted(t *testing.T) {
	Convey("Given 一条 pi 会话已经用某个模型跑过一轮", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		created := 0
		firstClosed := make(chan struct{})
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			created++
			if created == 1 {
				return &fakeSession{stream: &scriptedStream{}, sid: "session-1", closeStarted: firstClosed}, nil
			}
			return &fakeSession{stream: &scriptedStream{}, sid: "session-1"}, nil
		})
		defer restore()

		r := NewWithPool(pool)
		backend := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}", ReasoningEffort: "low"}
		req := agentruntime.RunRequest{Backend: backend, SessionID: 32, Cwd: t.TempDir(), UserText: "one"}
		events, _, err := r.Run(context.Background(), req)
		So(err, ShouldBeNil)
		for range events {
		}

		Convey("When 下一轮换了推理档位 Then 旧会话被收掉并重新 spawn", func() {
			next := *backend
			next.ReasoningEffort = "high"
			req.Backend = &next
			req.UserText = "two"
			events, _, err := r.Run(context.Background(), req)
			So(err, ShouldBeNil)
			for range events {
			}

			So(created, ShouldEqual, 2)
			select {
			case <-firstClosed:
			case <-time.After(2 * time.Second):
				t.Fatal("被换掉的旧会话没有被收掉:它的 RPC 进程会一直留着")
			}
		})
	})
}
