package ctlcmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// 供应商测试 / 模型发现 / 模型导入：账号模式在一台设备上执行（ENGINE_TEST / ENGINE_DISCOVER），
// 桌面与会话模式走本机 /ctl/v1/provider-*。

const upstreamSecret = "sk-LEAK-ME-NOT" //nolint:gosec // 测试用假密钥，断言它不会出现在输出里。

func providerMissing() error {
	return &rpcerror.Error{Code: rpcerror.CodeProviderMissing, Message: "LLM provider not configured"}
}

func discovered(ids ...string) func(*agentrewire.EngineDiscoverRequest) (*agentrewire.EngineDiscoverResponse, error) {
	return func(*agentrewire.EngineDiscoverRequest) (*agentrewire.EngineDiscoverResponse, error) {
		out := &agentrewire.EngineDiscoverResponse{}
		for _, id := range ids {
			out.Models = append(out.Models, &agentrewire.EngineModel{ModelId: id, Name: id})
		}
		return out, nil
	}
}

func TestTestProvider_Account_RunsOnTheFirstOnlineDeviceAndSendsTheProviderAndModelKeys(t *testing.T) {
	w := newWorld(t)
	r := runWith([]string{"test", "provider", "openrouter", "--model", "openai/gpt-5.1-mini"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, []string{"test:PV-4/0b6c-22"}, w.box.engineCalls())
	assert.Contains(t, r.stdout, "ok")
	assert.Contains(t, r.stdout, "build-box", "names the device that ran it")
}

func TestTestProvider_Account_AnUpstreamErrorIsReportedWithoutSecrets(t *testing.T) {
	w := newWorld(t)
	w.box.engineTest = func(*agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error) {
		return &agentrewire.EngineTestResponse{Ok: false, Message: "upstream 401: invalid api key"}, nil
	}
	r := runWith([]string{"test", "provider", "openrouter"}, envMap(), term{})
	assert.Equal(t, exitFailed, r.code)
	assert.Contains(t, r.stderr, "upstream 401: invalid api key")
	assert.NotContains(t, r.stdout+r.stderr, upstreamSecret)
}

func TestTestProvider_Account_DeviceFlagPicksTheDeviceAndAnOfflineOneIsRefused(t *testing.T) {
	w := newWorld(t)
	ok := runWith([]string{"test", "provider", "openrouter", "--device", "build-box"}, envMap(), term{})
	require.Equal(t, exitOK, ok.code, ok.stderr, "the same-named cli device does not make the name ambiguous")
	assert.Len(t, w.box.engineCalls(), 1)

	off := runWith([]string{"test", "provider", "openrouter", "--device", "laptop"}, envMap(), term{})
	assert.Equal(t, exitFailed, off.code)
	assert.Contains(t, off.stderr, "device laptop is offline")
	assert.Empty(t, w.laptop.engineCalls())
}

func TestTestProvider_Account_ProviderNotYetSyncedToTheDevice(t *testing.T) {
	w := newWorld(t)
	w.box.engineTest = func(*agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error) {
		return nil, providerMissing()
	}

	named := runWith([]string{"test", "provider", "openrouter", "--device", "build-box"}, envMap(), term{})
	assert.Equal(t, exitFailed, named.code)
	assert.Contains(t, named.stderr, "device build-box has not received this provider yet — try again shortly")

	auto := runWith([]string{"test", "provider", "openrouter"}, envMap(), term{})
	assert.Equal(t, exitFailed, auto.code)
	assert.Contains(t, auto.stderr, "no online device has received this provider yet")
}

func TestTestProvider_Account_NoOnlineDevice(t *testing.T) {
	w := newWorld(t)
	w.relay.SetOffline(boxFP, true)
	r := runWith([]string{"test", "provider", "openrouter"}, envMap(), term{})
	assert.Equal(t, exitFailed, r.code)
	assert.NotEmpty(t, r.stderr)
}

func TestDiscoverModels_Account_MarksTheModelsAlreadyImported(t *testing.T) {
	w := newWorld(t)
	w.box.engineDiscover = discovered("openai/gpt-5.1", "openai/new-model")
	r := runWith([]string{"discover", "models", "openrouter"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, []string{"discover:PV-4"}, w.box.engineCalls())
	assert.Regexp(t, `openai/gpt-5\.1\s+imported`, r.stdout)
	assert.NotRegexp(t, `openai/new-model\s+imported`, r.stdout)
	assert.Contains(t, r.stdout, "openai/new-model")

	j := runWith([]string{"discover", "models", "openrouter", "-o", "json"}, envMap(), term{})
	require.Equal(t, exitOK, j.code, j.stderr)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(j.stdout), &rows), j.stdout)
	assert.Equal(t, []map[string]any{
		{"modelId": "openai/gpt-5.1", "name": "openai/gpt-5.1", "imported": true},
		{"modelId": "openai/new-model", "name": "openai/new-model", "imported": false},
	}, []map[string]any{rows[0], rows[1]})
}

func TestImportModels_Account_AllCreatesTheMissingOnesAndCountsTheSkipped(t *testing.T) {
	w := newWorld(t)
	w.box.engineDiscover = discovered("openai/gpt-5.1", "openai/new-a", "openai/new-b")
	r := runWith([]string{"import", "models", "openrouter", "--all"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Contains(t, r.stdout, "imported 2 models, skipped 1 that already exist")
	exec := w.srv.exec
	require.Equal(t, 2, exec.writeCount())
	for i, id := range []string{"openai/new-a", "openai/new-b"} {
		wr := exec.writes[i]
		assert.Equal(t, agentrewire.CtlOp_CTL_OP_CREATE, wr.GetOp())
		assert.Equal(t, agentrewire.CtlKind_CTL_KIND_MODEL, wr.GetKind())
		assert.Equal(t, id, wr.GetResource().GetModel().GetModelId())
		assert.Equal(t, int64(4), wr.GetResource().GetModel().GetProviderId())
		assert.ElementsMatch(t, []string{"providerId", "modelId"}, wr.GetFields())
	}
}

func TestImportModels_Account_NamedModelsMustExistUpstreamAndExistingOnesAreSkipped(t *testing.T) {
	w := newWorld(t)
	w.box.engineDiscover = discovered("openai/gpt-5.1", "openai/new-a")
	r := runWith([]string{"import", "models", "openrouter", "openai/new-a", "openai/gpt-5.1"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Contains(t, r.stdout, "imported 1 model, skipped 1 that already exist")
	require.Equal(t, 1, w.srv.exec.writeCount())

	bad := runWith([]string{"import", "models", "openrouter", "openai/ghost"}, envMap(), human)
	assert.Equal(t, exitFailed, bad.code)
	assert.Contains(t, bad.stderr, `"openai/ghost" is not offered by the provider`)
	assert.Equal(t, 1, w.srv.exec.writeCount(), "nothing is written when a named model is unknown")
}

func TestImportModels_Usage(t *testing.T) {
	newWorld(t)
	assert.Equal(t, exitUsage, runWith([]string{"import", "models", "openrouter"}, envMap(), human).code, "needs --all or ids")
	assert.Equal(t, exitUsage, runWith([]string{"import", "models", "openrouter", "--all", "x"}, envMap(), human).code, "--all and ids conflict")
	assert.Equal(t, exitUsage, runWith([]string{"import", "providers", "openrouter", "--all"}, envMap(), human).code)
}

func TestImportModels_Account_NotSyncedAndPATAndExternalRules(t *testing.T) {
	w := newWorld(t)
	w.box.engineDiscover = func(*agentrewire.EngineDiscoverRequest) (*agentrewire.EngineDiscoverResponse, error) {
		return nil, providerMissing()
	}
	r := runWith([]string{"import", "models", "openrouter", "--all", "--device", "build-box"}, envMap(), human)
	assert.Equal(t, exitFailed, r.code)
	assert.Contains(t, r.stderr, "device build-box has not received this provider yet — try again shortly")

	w.box.engineDiscover = discovered("openai/new-a")
	ext := runWith([]string{"import", "models", "openrouter", "--all"}, envMap(), term{})
	assert.Equal(t, exitFailed, ext.code, "a logged-in account with an external program is refused")
	assert.Contains(t, ext.stderr, "needs a person at a terminal")
	assert.Equal(t, 0, w.srv.exec.writeCount())

	pat := runWith([]string{"import", "models", "openrouter", "--all"}, patEnv(w), term{})
	require.Equal(t, exitOK, pat.code, pat.stderr)
	assert.Equal(t, 1, w.srv.exec.writeCount())
}

func TestProviderProbes_Desktop_GoThroughTheLocalControlAPI(t *testing.T) {
	f, srv := newFakeExecutor(t, "tok")
	accountDirs(t)
	f.probeTest = `{"ok":true,"message":"connection ok"}`
	f.probeModel = []string{"openai/gpt-5.1", "openai/new-a"}
	env := envFor(srv, "tok")

	r := runWith([]string{"test", "provider", "openrouter", "--model", "openai/gpt-5.1-mini"}, env, human)
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Contains(t, r.stdout, "connection ok")
	log := f.probeLog()
	require.Len(t, log, 1)
	path, body, _ := strings.Cut(log[0], " ")
	assert.Equal(t, "/ctl/v1/provider-test", path)
	assert.JSONEq(t, `{"providerId":4,"modelKey":"0b6c-22"}`, body)

	f.probeTest = `{"ok":false,"message":"401 from upstream"}`
	bad := runWith([]string{"test", "provider", "openrouter"}, env, human)
	assert.Equal(t, exitFailed, bad.code)
	assert.Contains(t, bad.stderr, "401 from upstream")

	d := runWith([]string{"discover", "models", "openrouter"}, env, human)
	require.Equal(t, exitOK, d.code, d.stderr)
	assert.Regexp(t, `openai/gpt-5\.1\s+imported`, d.stdout)

	imp := runWith([]string{"import", "models", "openrouter", "--all"}, env, human)
	require.Equal(t, exitOK, imp.code, imp.stderr)
	assert.Contains(t, imp.stdout, "imported 1 model, skipped 1 that already exist")
	wr := f.onlyWrite(t)
	assert.Equal(t, "openai/new-a", wr.GetResource().GetModel().GetModelId())
	assert.Equal(t, agentrewire.CtlCaller_CTL_CALLER_SESSION, wr.GetCaller(), "the session's own approval rules apply")
}

func TestProviderProbes_Desktop_DeviceFlagNeedsAnAccount(t *testing.T) {
	_, srv := newFakeExecutor(t, "tok")
	accountDirs(t)
	for _, args := range [][]string{
		{"test", "provider", "openrouter", "--device", "x"},
		{"discover", "models", "openrouter", "--device", "x"},
		{"import", "models", "openrouter", "--all", "--device", "x"},
	} {
		r := runWith(args, envFor(srv, "tok"), human)
		assert.Equal(t, exitUsage, r.code, args)
		assert.True(t, strings.Contains(r.stderr, "--device needs an account"), r.stderr)
	}
}
