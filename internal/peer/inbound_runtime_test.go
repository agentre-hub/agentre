package peer_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 本文件锁住「浏览器接的是**桌面 App** 时，runtime 那几个方法答的必须是真话」。
//
// 此前 capabilities 是个自陈的桩：它返回空的 CapabilitiesResult{}，只用来证明授权
// 对端够得着桌面端注册表。空的能力矩阵不是「这个 backend 没有能力」——它是「这一端
// 什么都没说」，而浏览器把它读成前者，于是权限档位在这条路上一档都列不出来。
// 同理 runtime.setPermissionMode 一直只有 agentred 那侧注册，接桌面 App 的浏览器
// 连模式都设不了。两件事是同一处接线漏的，一起补。

// capsRuntime 是一个只回答能力矩阵的 runtime 替身：这几条用例断言的是「注册表里
// 那一份原样送出去了没有」，跑不跑轮次与它无关。
type capsRuntime struct{ caps capability.Capabilities }

func (r *capsRuntime) Capabilities() capability.Capabilities { return r.caps }

func (r *capsRuntime) Run(context.Context, agentruntime.RunRequest) (
	<-chan agentruntime.Event, *agentruntime.RunResult, error,
) {
	return nil, nil, errors.New("capsRuntime never runs a turn")
}

// Given 这台桌面端的注册表里有一个 claudecode runtime，When 同账号的浏览器问它
// 「这个 backend 支持哪些权限档位」，Then 拿到的是**注册表里那一份**——档位集合、
// 默认档与循环顺序逐字回来，而不是一个空壳。
func TestInbound_GivenRegisteredRuntime_WhenAuthorizedPeerAsksCapabilities_ThenAnswersTheRealMatrix(t *testing.T) {
	want := capability.Capabilities{
		Set: map[capability.Capability]bool{capability.CapSetPermission: true},
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes:         []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			DefaultMode:          "acceptEdits",
			SwitchableDuringTurn: true,
			Order:                []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			LaunchDefaultMode:    "",
		},
	}
	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, &capsRuntime{caps: want}))

	ws := startInboundPeer(t)
	authorizePeer(t, ws, `1`)

	response := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`2`), Method: wire.MethodCapabilities,
		Params: mustJSON(t, wire.CapabilitiesParams{BackendType: string(agent_backend_entity.TypeClaudeCode)}),
	})
	require.Nil(t, response.Error)

	var got agentrewire.RuntimeCapabilitiesResponse
	require.NoError(t, protojson.Unmarshal(response.Result, &got))
	assert.Equal(t, want.PermissionModeMeta.AllowedModes, got.PermissionMode.AllowedModes,
		"档位元数据必须原样送出：浏览器的权限 pill 整个建立在它上面")
	var setPermission bool
	for _, entry := range got.Capabilities {
		if entry.Name == string(capability.CapSetPermission) && entry.Enabled {
			setPermission = true
		}
	}
	require.True(t, setPermission, "能力集合同样要是真的，不能只回一个空 Set")
}

// Given 浏览器问的那个 backend 在这台机器上没有注册 runtime，When 它问能力，
// Then 如实报错 —— 而不是回一份空矩阵成功。两者在界面上是两句话：前者要说
// 「这台机器答不出」，后者会被读成「这个 backend 没有权限档位」。
func TestInbound_GivenUnregisteredBackendType_WhenAsksCapabilities_ThenErrorsInsteadOfEmptySuccess(t *testing.T) {
	ws := startInboundPeer(t)
	authorizePeer(t, ws, `1`)

	response := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`2`), Method: wire.MethodCapabilities,
		Params: mustJSON(t, wire.CapabilitiesParams{BackendType: "no-such-runtime"}),
	})
	require.NotNil(t, response.Error, "没注册的 backend 必须报错，不能空成功")
}

// Given 一台已登录的桌面端在线，When 同账号的浏览器要改某条会话的权限模式，
// Then 这个方法在**这一端**也是服务的：请求进得到 chat_svc（这里让它撞上一条不
// 存在的会话，于是回一个会话级错误），而不是被 RPC 层以「没有这个方法」挡回去。
func TestInbound_GivenAuthorizedPeer_WhenSettingPermissionMode_ThenServedHereToo(t *testing.T) {
	sessions := registerInboundPeerChatForDelete(t)
	ws := startInboundPeer(t)

	// 账号门：改权限模式比读更该在门后。
	unauthenticated := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`1`), Method: wire.MethodSetPermissionMode,
		Params: mustJSON(t, wire.SetPermissionModeParams{SessionID: 4242, Mode: "default"}),
	})
	require.NotNil(t, unauthenticated.Error)
	assert.Equal(t, rpcerror.ErrUnauthorized.Code, unauthenticated.Error.Code)

	authorizePeer(t, ws, `2`)

	// 这条会话不存在：chat_svc 因此回一个会话级错误。断言的是**请求到达了它**，
	// 而不是在路由层就没这个方法。
	sessions.EXPECT().Find(gomock.Any(), int64(4242)).Return(nil, nil)

	response := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`3`), Method: wire.MethodSetPermissionMode,
		Params: mustJSON(t, wire.SetPermissionModeParams{SessionID: 4242, Mode: "default"}),
	})
	require.NotNil(t, response.Error)
	assert.NotEqual(t, rpcerror.ErrMethodNotFound.Code, response.Error.Code,
		"接桌面 App 的浏览器同样要设得了权限模式，不能只有 agentred 那一侧有")
}

// Given 一台已登录的桌面端在线，When 同账号的浏览器要改某条会话钉的模型，
// Then 这个方法在**这一端**也是服务的：请求进得到 chat_svc（这里让它撞上一条不
// 存在的会话），而不是被 RPC 层以「没有这个方法」挡回去。
//
// 两台机器都要写得进：同一条对话可以在桌面端与 agentred 上各有一份，用户在浏览器
// 里换模型时两边都落，在哪一台打开都看到自己刚选的那个。agentred 那一侧的落库由
// daemon 的 SessionModelTargetHandlers 承担，这里是桌面端这一侧。
func TestInbound_GivenAuthorizedPeer_WhenSettingModelTarget_ThenServedHereToo(t *testing.T) {
	sessions := registerInboundPeerChatForDelete(t)
	ws := startInboundPeer(t)

	unauthenticated := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`1`), Method: wire.MethodSetModelTarget,
		Params: mustJSON(t, wire.SetModelTargetParams{SessionID: 4242, ProviderKey: "p"}),
	})
	require.NotNil(t, unauthenticated.Error)
	assert.Equal(t, rpcerror.ErrUnauthorized.Code, unauthenticated.Error.Code)

	authorizePeer(t, ws, `2`)

	sessions.EXPECT().Find(gomock.Any(), int64(4242)).Return(nil, nil)

	response := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`3`), Method: wire.MethodSetModelTarget,
		Params: mustJSON(t, wire.SetModelTargetParams{SessionID: 4242, ProviderKey: "p"}),
	})
	require.NotNil(t, response.Error)
	assert.NotEqual(t, rpcerror.ErrMethodNotFound.Code, response.Error.Code,
		"接桌面 App 的浏览器同样要改得了模型，不能只有 agentred 那一侧有")
}

// 两格都空是**要写下去的值**（改回跟随 Agent 绑定），不是「参数没填」——
// 在参数校验这一层把它挡掉，用户就再也回不到跟随绑定了。
func TestInbound_GivenBothKeysEmpty_WhenSettingModelTarget_ThenItIsAcceptedAsInheritAgent(t *testing.T) {
	sessions := registerInboundPeerChatForDelete(t)
	ws := startInboundPeer(t)
	authorizePeer(t, ws, `1`)

	sessions.EXPECT().Find(gomock.Any(), int64(4242)).Return(nil, nil)

	response := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`2`), Method: wire.MethodSetModelTarget,
		Params: mustJSON(t, wire.SetModelTargetParams{SessionID: 4242}),
	})
	require.NotNil(t, response.Error)
	assert.NotEqual(t, rpcerror.ErrInvalidParams.Code, response.Error.Code,
		"两格都空必须一路走到 chat_svc（这里因会话不存在而报错），不能在参数层被当成没填")
}
