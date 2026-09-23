package ctl_svc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// plaintextKey 是假的明文 API key；任何响应里都不该出现它。
const plaintextKey = "sk-live-0123456789abcdef"

// ---- fakes ----

type fakeResourceGateways struct {
	agents      []*agentrewire.CtlAgent
	departments []*agentrewire.CtlDepartment
	projects    []*agentrewire.CtlProject
	providers   []*agentrewire.CtlProvider
	models      []*agentrewire.CtlModel
	backends    []*agentrewire.CtlBackend
	err         error
}

func (f *fakeResourceGateways) ListAgents(context.Context) ([]*agentrewire.CtlAgent, error) {
	return f.agents, f.err
}
func (f *fakeResourceGateways) ListDepartments(context.Context) ([]*agentrewire.CtlDepartment, error) {
	return f.departments, f.err
}
func (f *fakeResourceGateways) ListProjects(context.Context) ([]*agentrewire.CtlProject, error) {
	return f.projects, f.err
}
func (f *fakeResourceGateways) ListProviders(context.Context) ([]*agentrewire.CtlProvider, error) {
	return f.providers, f.err
}
func (f *fakeResourceGateways) ListModels(context.Context) ([]*agentrewire.CtlModel, error) {
	return f.models, f.err
}
func (f *fakeResourceGateways) ListBackends(context.Context) ([]*agentrewire.CtlBackend, error) {
	return f.backends, f.err
}

func (f *fakeResourceGateways) resources() Resources {
	return Resources{Org: f, Projects: f, Providers: f, Backends: f}
}

// fakeResourceData 是每类各一两条的样本；网关故意回明文密钥，验证 handler 这道边界。
func fakeResourceData() *fakeResourceGateways {
	return &fakeResourceGateways{
		agents: []*agentrewire.CtlAgent{
			{Id: 3, Name: "architect", DepartmentId: 2, BackendIds: []int64{5, 9}, Pinned: true},
			{Id: 4, Name: "reviewer"},
		},
		departments: []*agentrewire.CtlDepartment{{Id: 2, Name: "eng", LeadAgentId: 3}},
		projects: []*agentrewire.CtlProject{{
			Id: 1, Name: "agentre", Path: "/src/agentre", MemberAgentIds: []int64{3},
			Locations: []*agentrewire.CtlProjectLocation{{DeviceId: "sha256:ab", DeviceName: "box", Path: "/srv/agentre"}},
		}},
		providers: []*agentrewire.CtlProvider{
			{Id: 1, Name: "anthropic", Type: "anthropic", ApiKey: plaintextKey, ApiKeySet: true, BackendRefs: 2},
		},
		models: []*agentrewire.CtlModel{{Id: 21, ProviderId: 1, Key: "mk-1", ModelId: "claude-opus-5", IsDefault: true, BackendRefs: 1}},
		backends: []*agentrewire.CtlBackend{
			{Id: 5, Name: "claw", Type: "openclaw", Token: "gateway-secret-token", TokenSet: true, Device: "box"},
		},
	}
}

func fakeResources() Resources { return fakeResourceData().resources() }

func newResourceHandler(r Resources) *ctlHandler {
	return &ctlHandler{token: testToken, agents: &fakeAgents{}, projects: &fakeProjects{}, chat: &fakeChat{}, resources: r}
}

func postResources(t *testing.T, h http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, http.MethodPost, "/ctl/v1/resources", token, body)
}

func decodeCtlResponse(t *testing.T, rec *httptest.ResponseRecorder) *agentrewire.CtlResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp agentrewire.CtlResponse
	require.NoError(t, protojson.Unmarshal(rec.Body.Bytes(), &resp))
	return &resp
}

// ---- list / get ----

func TestResources_ListEveryKind(t *testing.T) {
	h := newResourceHandler(fakeResources())
	cases := []struct {
		kind string
		want []int64
	}{
		{"CTL_KIND_AGENT", []int64{3, 4}},
		{"CTL_KIND_DEPARTMENT", []int64{2}},
		{"CTL_KIND_PROJECT", []int64{1}},
		{"CTL_KIND_PROVIDER", []int64{1}},
		{"CTL_KIND_MODEL", []int64{21}},
		{"CTL_KIND_BACKEND", []int64{5}},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			resp := decodeCtlResponse(t, postResources(t, h, testToken, `{"list":{"kind":"`+c.kind+`"}}`))
			var ids []int64
			for _, it := range resp.GetList().GetItems() {
				ids = append(ids, resourceID(it))
			}
			assert.Equal(t, c.want, ids)
		})
	}
}

func TestResources_GetByID(t *testing.T) {
	h := newResourceHandler(fakeResources())

	t.Run("项目详情带成员与各设备路径", func(t *testing.T) {
		resp := decodeCtlResponse(t, postResources(t, h, testToken, `{"get":{"kind":"CTL_KIND_PROJECT","id":"1"}}`))
		p := resp.GetGet().GetResource().GetProject()
		require.NotNil(t, p)
		assert.Equal(t, []int64{3}, p.GetMemberAgentIds())
		require.Len(t, p.GetLocations(), 1)
		assert.Equal(t, "/srv/agentre", p.GetLocations()[0].GetPath())
	})
	t.Run("agent 详情带执行目标", func(t *testing.T) {
		resp := decodeCtlResponse(t, postResources(t, h, testToken, `{"get":{"kind":"CTL_KIND_AGENT","id":"3"}}`))
		assert.Equal(t, []int64{5, 9}, resp.GetGet().GetResource().GetAgent().GetBackendIds())
	})
	t.Run("提供方详情带引用数", func(t *testing.T) {
		resp := decodeCtlResponse(t, postResources(t, h, testToken, `{"get":{"kind":"CTL_KIND_PROVIDER","id":"1"}}`))
		assert.Equal(t, int32(2), resp.GetGet().GetResource().GetProvider().GetBackendRefs())
	})
	t.Run("找不到 → 404，消息指明资源与 id", func(t *testing.T) {
		rec := postResources(t, h, testToken, `{"get":{"kind":"CTL_KIND_BACKEND","id":"99"}}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "backend id 99 not found")
	})
}

// TestResources_NeverEchoSecrets 钉死 Hard invariant 1：API key 只回掩码（前 4、圆点、
// 后 4），token 只回「是否已设置」——即便网关交上来的是明文。
func TestResources_NeverEchoSecrets(t *testing.T) {
	h := newResourceHandler(fakeResources())
	for _, body := range []string{
		`{"list":{"kind":"CTL_KIND_PROVIDER"}}`,
		`{"get":{"kind":"CTL_KIND_PROVIDER","id":"1"}}`,
		`{"list":{"kind":"CTL_KIND_BACKEND"}}`,
		`{"get":{"kind":"CTL_KIND_BACKEND","id":"5"}}`,
	} {
		rec := postResources(t, h, testToken, body)
		require.Equal(t, http.StatusOK, rec.Code, body)
		assert.NotContains(t, rec.Body.String(), plaintextKey, body)
		assert.NotContains(t, rec.Body.String(), "gateway-secret-token", body)
	}

	resp := decodeCtlResponse(t, postResources(t, h, testToken, `{"get":{"kind":"CTL_KIND_PROVIDER","id":"1"}}`))
	p := resp.GetGet().GetResource().GetProvider()
	assert.Equal(t, "sk-l••••••cdef", p.GetApiKey())
	assert.True(t, p.GetApiKeySet())

	resp = decodeCtlResponse(t, postResources(t, h, testToken, `{"get":{"kind":"CTL_KIND_BACKEND","id":"5"}}`))
	b := resp.GetGet().GetResource().GetBackend()
	assert.Empty(t, b.GetToken())
	assert.True(t, b.GetTokenSet())
}

func TestMaskSecret(t *testing.T) {
	assert.Equal(t, "", MaskSecret(""))
	assert.Equal(t, "sk-l••••••cdef", MaskSecret(plaintextKey))
	assert.Equal(t, "•••••", MaskSecret("short"), "8 位以内不露任何字符")
	assert.Equal(t, "••••••••", MaskSecret("12345678"))
	for _, k := range []string{plaintextKey, "short", "12345678", "123456789"} {
		assert.Equal(t, MaskSecret(k), MaskSecret(MaskSecret(k)), "掩码幂等：服务层已掩码的值再过一遍不变 (%q)", k)
	}
}

// ---- 错误路径 ----

func TestResources_RequestErrors(t *testing.T) {
	h := newResourceHandler(fakeResources())

	t.Run("GET → 405", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/ctl/v1/resources", testToken, "")
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
	t.Run("坏 JSON → 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, postResources(t, h, testToken, `{`).Code)
	})
	t.Run("空请求 → 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, postResources(t, h, testToken, `{}`).Code)
	})
	t.Run("未指定资源类型 → 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, postResources(t, h, testToken, `{"list":{}}`).Code)
	})
	t.Run("网关出错 → 500，消息原样透出", func(t *testing.T) {
		broken := fakeResourceData()
		broken.err = errors.New("project not found")
		rec := postResources(t, newResourceHandler(broken.resources()), testToken, `{"list":{"kind":"CTL_KIND_PROJECT"}}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "project not found")
	})
	t.Run("资源网关未接线 → 503", func(t *testing.T) {
		rec := postResources(t, newResourceHandler(Resources{}), testToken, `{"list":{"kind":"CTL_KIND_AGENT"}}`)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
	t.Run("没带 token → 401", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, postResources(t, h, "", `{"list":{"kind":"CTL_KIND_AGENT"}}`).Code)
	})
}

// ---- 会话级 token 的签发 ----

// TestSessionCredentials 钉死签发与校验用的是同一个实例：ctl_svc 签给子进程的 token 能过
// 它自己的 handler、识别成会话调用；另一个实例签的过不了。端点没发布前不签。
func TestSessionCredentials(t *testing.T) {
	t.Cleanup(func() { agentruntime.RegisterCtlCredentialSource(nil) })
	svc := newCtlSvc()
	svc.RegisterDeps(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	svc.RegisterResources(fakeResources())

	assert.Equal(t, agentruntime.CtlCredentials{}, svc.SessionCredentials(7, 42), "端点未发布 → 不签")

	svc.PublishSessionEndpoint("http://127.0.0.1:60080")
	creds := agentruntime.RunRequest{AgentID: 7, SessionID: 42}.CtlCredentials()
	assert.Equal(t, "http://127.0.0.1:60080", creds.Endpoint, "发布后 runtime 从 ctl_svc 取凭证")
	assert.Equal(t, svc.SessionToken(7, 42), creds.Token)
	ref, ok := svc.VerifySessionToken(creds.Token)
	assert.True(t, ok)
	assert.Equal(t, int64(42), ref.SessionID)

	h := svc.ControlHandler()
	assert.Equal(t, http.StatusOK, postResources(t, h, creds.Token, `{"list":{"kind":"CTL_KIND_AGENT"}}`).Code)
	assert.Equal(t, http.StatusForbidden, do(t, h, http.MethodPost, "/ctl/v1/stop", creds.Token, `{"sessionId":42}`).Code)
	assert.Equal(t, http.StatusUnauthorized,
		do(t, h, http.MethodGet, "/ctl/v1/agents", newCtlSvc().SessionToken(7, 42), "").Code)
}
