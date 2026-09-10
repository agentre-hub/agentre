package wirecall_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 每个 RPC 方法都要有一个 typed 调用函数,而且只有一个。
//
// 守的是一类具体的漂移:从前 method ID 与消息类型的配对散在两个仓库的十几个文件里
// (桌面端的 service 层、Wails 绑定层、两个 internal/pkg 包,以及 agentre-server 的
// machineConn),同一对配了好几遍。漏一个方法没什么可见后果 —— 直到有人第二次手写它,
// 而两处写得不一样。
func TestCoverage_GivenTheMethodEnum_ThenEveryMethodHasExactlyOneTypedCaller(t *testing.T) {
	t.Parallel()

	covered := wirecall.Covered()
	values := agentrewire.RpcMethod(0).Descriptor().Values()

	var missing []string
	total := 0
	for i := range values.Len() {
		value := values.Get(i)
		method := agentrewire.RpcMethod(value.Number())
		if method == agentrewire.RpcMethod_RPC_METHOD_UNSPECIFIED {
			continue
		}
		total++
		if _, ok := covered[method]; !ok {
			missing = append(missing, string(value.Name()))
		}
	}

	require.NotZero(t, total, "枚举里一个方法都没有,守卫会空过")
	require.Empty(t, missing, "这些方法还没有 typed 调用函数")
	require.Len(t, covered, total, "wirecall covers 了枚举之外的东西")
}

// 一行写错方法号,两个函数就会指向同一个方法 —— 而它们各自的用例都还是绿的,因为
// 两边发的都是「一个合法的方法号」。define 在重复注册时直接 panic,这条用例钉住它。
func TestDefine_GivenAMethodRegisteredTwice_ThenItPanics(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		wirecall.Define[*agentrewire.HealthPingRequest](
			agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING,
			func() *agentrewire.HealthPingResponse { return &agentrewire.HealthPingResponse{} })
	})
}

// 把两个方法的请求类型抄串了,重复注册那条守卫看不出来:两个方法号都还在,各注册一次。
// 这条按命名约定立论 —— RPC_METHOD_SESSION_PULL 配的就该是 SessionPullRequest。
//
// 约定之外的都在 exceptions 里逐条写明理由;想加一条,得先说服自己那不是抄错。
func TestPairing_GivenAMethod_ThenItsRequestTypeFollowsTheNamingConvention(t *testing.T) {
	t.Parallel()

	exceptions := map[agentrewire.RpcMethod]string{
		// 取目标与设目标是同一个请求消息(RuntimeGoalRequest),方法号区分语义。
		agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_GET: "RuntimeGoalRequest",
		agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_SET: "RuntimeGoalRequest",
		// 目标的三个方法(取/设/清)共用 RuntimeGoalRequest,方法号区分语义;
		// 只有清除的**响应**是单独的 RuntimeGoalClearResponse。
		agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_CLEAR: "RuntimeGoalRequest",
		// 技能目录的消息叫 SkillCatalog,方法叫 SKILLS_CATALOG。
		agentrewire.RpcMethod_RPC_METHOD_SKILLS_CATALOG: "SkillCatalogRequest",
	}

	for method, pairing := range wirecall.Covered() {
		want, ok := exceptions[method]
		if !ok {
			want = conventionalRequestName(method)
		}
		assert.Equal(t, want, pairing.RequestType,
			"%v 的请求类型不符合命名约定;确实是有意的就在 exceptions 里写明理由", method)
	}
}

// 响应类型同理。分开写是因为响应共用得多(控制类方法都答 Empty),但**不检查**它的
// 代价是真的:MCP_PROXY 一度被配成 SkillCatalogResponse,请求那条守卫看不出来。
func TestPairing_GivenAMethod_ThenItsResponseTypeFollowsTheNamingConvention(t *testing.T) {
	t.Parallel()

	exceptions := map[agentrewire.RpcMethod]string{}
	for _, method := range []agentrewire.RpcMethod{
		// 控制类方法只报成败,没有内容可答。
		agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE,
		agentrewire.RpcMethod_RPC_METHOD_TERMINAL_RESIZE,
		agentrewire.RpcMethod_RPC_METHOD_TERMINAL_CLOSE,
		agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER,
		agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE,
		agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STOP_BACKGROUND_TASK,
	} {
		exceptions[method] = "Empty"
	}
	// 一问一答共用同一个应答壳。
	exceptions[agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER] = "PeerSessionControlResponse"
	exceptions[agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION] = "PeerSessionControlResponse"
	exceptions[agentrewire.RpcMethod_RPC_METHOD_PROJECT_SET_LOCAL_PATH] = "ProjectLocalPathResponse"
	exceptions[agentrewire.RpcMethod_RPC_METHOD_PROJECT_CLEAR_LOCAL_PATH] = "ProjectLocalPathResponse"
	exceptions[agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_GET] = "RuntimeGoalResponse"
	exceptions[agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_SET] = "RuntimeGoalResponse"
	exceptions[agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_CLEAR] = "RuntimeGoalClearResponse"
	exceptions[agentrewire.RpcMethod_RPC_METHOD_SKILLS_CATALOG] = "SkillCatalogResponse"

	for method, pairing := range wirecall.Covered() {
		want, ok := exceptions[method]
		if !ok {
			want = strings.TrimSuffix(conventionalRequestName(method), "Request") + "Response"
		}
		assert.Equal(t, want, pairing.ResponseType,
			"%v 的响应类型不符合命名约定;确实是有意的就在 exceptions 里写明理由", method)
	}
}

// conventionalRequestName 把 RPC_METHOD_SESSION_PULL 翻成 SessionPullRequest。
func conventionalRequestName(method agentrewire.RpcMethod) string {
	name := strings.TrimPrefix(method.String(), "RPC_METHOD_")
	acronyms := map[string]string{"CLI": "CLI", "LLM": "LLM", "MCP": "MCP", "FS": "Fs"}
	var b strings.Builder
	for _, part := range strings.Split(name, "_") {
		if fixed, ok := acronyms[part]; ok {
			b.WriteString(fixed)
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + strings.ToLower(part[1:]))
	}
	return b.String() + "Request"
}

// 交回的响应类型要是配对里写的那个 —— 用一个代表性方法钉住 define 真的把
// newResponse 用上了,而不是交回一个空壳。
func TestDefine_GivenAPairing_ThenItRemembersTheResponseType(t *testing.T) {
	t.Parallel()

	pairing := wirecall.Covered()[agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL]
	require.Equal(t, "SessionPullRequest", pairing.RequestType)
	require.Equal(t, "SessionPullResponse", pairing.ResponseType)
	require.Equal(t,
		reflect.TypeOf(&agentrewire.SessionPullResponse{}),
		reflect.TypeOf(pairing.NewResponse()))
}
