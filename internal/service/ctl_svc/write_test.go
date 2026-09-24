package ctl_svc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ---- fakes ----

// fakeWriter 记录执行者交给网关的写入；err 非空时写入失败。
type fakeWriter struct {
	mu     sync.Mutex
	calls  []string
	writes []Write
	newID  int64
	err    error
	// during 非 nil 时在每次写入里调用（模拟写入进行到一半）。
	during func(ctx context.Context)
}

func (f *fakeWriter) record(op string, w Write) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, op)
	f.writes = append(f.writes, w)
	return f.err
}

func (f *fakeWriter) Create(_ context.Context, w Write) (int64, error) {
	return f.newID, f.record("create", w)
}
func (f *fakeWriter) Update(ctx context.Context, w Write) error {
	if f.during != nil {
		f.during(ctx)
	}
	return f.record("update", w)
}
func (f *fakeWriter) Delete(_ context.Context, w Write) error { return f.record("delete", w) }

func (f *fakeWriter) onlyWrite(t *testing.T, op string) Write {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []string{op}, f.calls)
	return f.writes[0]
}

func (f *fakeWriter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeCascade struct{ depts, agents int }

func (f fakeCascade) CascadeImpact(context.Context, int64) (int, int, error) {
	return f.depts, f.agents, nil
}

// fakeSessionApprovals 是会话审批卡的假网关：answer 为 nil 时永不应答（走超时）。
type fakeSessionApprovals struct {
	mu       sync.Mutex
	answer   *bool
	beginErr error
	// onBegin 在卡片登记后、应答前调用（模拟审批挂起期间别处改了数据）。
	onBegin  func()
	sessions []int64
	begun    []*blocks.ToolApprovalBlock
	finished []string // status|result
}

func (f *fakeSessionApprovals) BeginToolApproval(_ context.Context, sessionID int64, blk *blocks.ToolApprovalBlock) (<-chan bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	f.sessions = append(f.sessions, sessionID)
	f.begun = append(f.begun, blk)
	if f.onBegin != nil {
		f.onBegin()
	}
	ch := make(chan bool, 1)
	if f.answer != nil {
		ch <- *f.answer
	}
	return ch, nil
}

func (f *fakeSessionApprovals) FinishToolApproval(_ context.Context, _ int64, requestID, status, result string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.begun) == 0 || f.begun[0].RequestID != requestID {
		return errors.New("unknown request")
	}
	f.finished = append(f.finished, status+"|"+result)
	return nil
}

func (f *fakeSessionApprovals) snapshot() ([]int64, []*blocks.ToolApprovalBlock, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions, f.begun, f.finished
}

// fakeExternalApprovals 扮演桌面端的待审批队列；测试自己当弹窗，调 Answer 作答。
type fakeExternalApprovals struct {
	mu        sync.Mutex
	pending   map[string]chan bool
	queued    []ExternalApproval
	withdrawn []string
	// autoAnswer 非 nil 时一入队就作答。
	autoAnswer *bool
	// answerAtDeadline 非 nil 时，撤下的那一刻发现请求刚被答了这个值。
	answerAtDeadline *bool
}

func (f *fakeExternalApprovals) Enqueue(_ context.Context, a ExternalApproval) (<-chan bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		f.pending = map[string]chan bool{}
	}
	ch := make(chan bool, 1)
	f.pending[a.RequestID] = ch
	f.queued = append(f.queued, a)
	if f.autoAnswer != nil {
		ch <- *f.autoAnswer
		delete(f.pending, a.RequestID)
	}
	return ch, nil
}

func (f *fakeExternalApprovals) Answer(requestID string, allow bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.pending[requestID]
	if !ok {
		return errors.New("request not pending")
	}
	delete(f.pending, requestID)
	ch <- allow
	return nil
}

func (f *fakeExternalApprovals) Withdraw(requestID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.pending[requestID]
	if ok && f.answerAtDeadline != nil { // 作答恰好抢在撤下之前拿到锁
		ch <- *f.answerAtDeadline
		ok = false
	}
	delete(f.pending, requestID)
	f.withdrawn = append(f.withdrawn, requestID)
	return ok
}

func (f *fakeExternalApprovals) Pending() []DesktopApprovalItem { return nil }

// ---- fixture ----

type writeFixture struct {
	data    *fakeResourceGateways
	writers map[agentrewire.CtlKind]*fakeWriter
	signer  *agenttool.TokenSigner
	session *fakeSessionApprovals
	desktop *fakeExternalApprovals
	h       *ctlHandler
}

func newWriteFixture() *writeFixture {
	data := &fakeResourceGateways{
		agents: []*agentrewire.CtlAgent{
			{Id: 3, Name: "architect", DepartmentId: 2, BackendIds: []int64{5}},
			{Id: 12, Name: "reviewer", DepartmentId: 2, BackendIds: []int64{5}, Description: "reviews"},
		},
		departments: []*agentrewire.CtlDepartment{
			{Id: 2, Name: "eng", LeadAgentId: 3},
			{Id: 6, Name: "qa"},
			{Id: 7, Name: "tmp", ParentId: 2},
		},
		projects: []*agentrewire.CtlProject{{Id: 1, Name: "agentre", Path: "/src/agentre", MemberAgentIds: []int64{3}}},
		providers: []*agentrewire.CtlProvider{
			{Id: 1, Name: "anthropic", Type: "anthropic", ApiKey: plaintextKey, ApiKeySet: true, Enabled: true, DefaultModelKey: "mk-1"},
		},
		models: []*agentrewire.CtlModel{
			{Id: 21, ProviderId: 1, Key: "mk-1", ModelId: "claude-opus-5", IsDefault: true, Enabled: true},
			{Id: 22, ProviderId: 1, Key: "mk-2", ModelId: "claude-sonnet-5", Enabled: true},
		},
		backends: []*agentrewire.CtlBackend{
			{Id: 5, Name: "cc", Type: "claudecode", ProviderId: 1, ConfigJson: `{"defaultPermissionMode":"plan","defaultModel":"x"}`},
			{Id: 9, Name: "claw", Type: "openclaw", Token: "gateway-secret-token", TokenState: agentrewire.CtlTokenState_CTL_TOKEN_STATE_SET},
		},
	}
	f := &writeFixture{
		data:    data,
		writers: map[agentrewire.CtlKind]*fakeWriter{},
		signer:  agenttool.NewTokenSigner(),
		session: &fakeSessionApprovals{},
		desktop: &fakeExternalApprovals{},
	}
	res := data.resources()
	res.Writers = map[agentrewire.CtlKind]KindWriter{}
	for kind := range kindNames {
		w := &fakeWriter{newID: 99}
		f.writers[kind] = w
		res.Writers[kind] = w
	}
	res.Cascade = fakeCascade{depts: 1, agents: 3}
	f.h = &ctlHandler{
		token: testToken, sessions: f.signer, resources: res,
		approvals: f.session, external: f.desktop, approvalTimeout: time.Minute,
	}
	return f
}

func (f *writeFixture) writer(kind agentrewire.CtlKind) *fakeWriter { return f.writers[kind] }

func writeBody(t *testing.T, w *agentrewire.CtlWriteRequest) string {
	t.Helper()
	raw, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Write{Write: w}})
	require.NoError(t, err)
	return string(raw)
}

func decodeWrite(t *testing.T, status int, body []byte) *agentrewire.CtlWriteResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, status, string(body))
	var resp agentrewire.CtlResponse
	require.NoError(t, protojson.Unmarshal(body, &resp))
	require.NotNil(t, resp.GetWrite(), string(body))
	return resp.GetWrite()
}

func str(s string) *string { return &s }

func agentDocRes(a *agentrewire.CtlAgent) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Agent{Agent: a}}
}

func fieldChange(field string, before, after *string) *agentrewire.CtlFieldChange {
	return &agentrewire.CtlFieldChange{Field: field, Before: before, After: after}
}

// realPost 经真实 HTTP 连接发请求，返回状态、响应体与 1xx 里的审批提示头。
func realPost(t *testing.T, h http.Handler, token, body string) (int, []byte, []string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var (
		mu      sync.Mutex
		pending []string
	)
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
			mu.Lock()
			defer mu.Unlock()
			if code == http.StatusProcessing {
				pending = append(pending, header.Get(ApprovalPendingHeader))
			}
			return nil
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/ctl/v1/resources", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	return resp.StatusCode, raw, pending
}

// ---- 人工调用：直接执行 ----

func TestWrite_GivenHumanUpdateThenExecutesWithExecutorComputedChanges(t *testing.T) {
	f := newWriteFixture()
	rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_AGENT, Id: 12,
		Caller: agentrewire.CtlCaller_CTL_CALLER_HUMAN,
		// 客户端给的文档里 name 故意是别的值：不在 fields 里的字段一律忽略。
		Resource: agentDocRes(&agentrewire.CtlAgent{Name: "ignored", DepartmentId: 6, Description: "reviews"}),
		Fields:   []string{"departmentId", "description"},
	}))
	resp := decodeWrite(t, rec.Code, rec.Body.Bytes())

	w := f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).onlyWrite(t, "update")
	assert.Equal(t, int64(12), w.Cur.GetAgent().GetId())
	assert.Equal(t, "reviewer", w.Next.GetAgent().GetName(), "未列出的字段沿用当前值")
	assert.Equal(t, int64(6), w.Next.GetAgent().GetDepartmentId())
	assert.True(t, w.Fields["departmentId"])

	assert.Equal(t, int64(12), resp.GetId())
	assert.Equal(t, "reviewer", resp.GetName())
	require.Len(t, resp.GetChanges(), 1)
	ch := resp.GetChanges()[0]
	assert.Equal(t, agentrewire.CtlOp_CTL_OP_UPDATE, ch.GetOp())
	assert.Equal(t, "reviewer", ch.GetName())
	assert.True(t, proto.Equal(fieldChange("departmentId", str("eng"), str("qa")), ch.GetFields()[0]),
		"引用写成名字；没变的 description 不列：%v", ch.GetFields())
	assert.Len(t, ch.GetFields(), 1)
}

func TestWrite_GivenCreateThenNewIDAndOnlyAfterValues(t *testing.T) {
	f := newWriteFixture()
	rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_CREATE, Kind: agentrewire.CtlKind_CTL_KIND_DEPARTMENT,
		Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Department{Department: &agentrewire.CtlDepartment{Name: "ops", ParentId: 2}}},
		Fields:   []string{"name", "parentId"},
	}))
	resp := decodeWrite(t, rec.Code, rec.Body.Bytes())
	assert.Equal(t, int64(99), resp.GetId())
	assert.Equal(t, "ops", resp.GetName())
	w := f.writer(agentrewire.CtlKind_CTL_KIND_DEPARTMENT).onlyWrite(t, "create")
	assert.Nil(t, w.Cur)
	assert.Equal(t, "ops", w.Next.GetDepartment().GetName())
	fields := resp.GetChanges()[0].GetFields()
	require.Len(t, fields, 2)
	assert.True(t, proto.Equal(fieldChange("name", nil, str("ops")), fields[0]))
	assert.True(t, proto.Equal(fieldChange("parentId", nil, str("eng")), fields[1]))
}

// TestWrite_SecretsNeverEchoed 钉死 Hard invariant 1 在写路径上：密钥明文只交给网关，
// 变更清单里只有 secret 标记；没写密钥时，当前的掩码值也不会被当成新值交下去。
func TestWrite_SecretsNeverEchoed(t *testing.T) {
	t.Run("写入 api key：网关拿到明文，响应里只有 secret 标记", func(t *testing.T) {
		f := newWriteFixture()
		const newKey = "sk-new-9876543210fedcba"
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_PROVIDER, Id: 1,
			Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
			Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: &agentrewire.CtlProvider{ApiKey: newKey}}},
			Fields:   []string{"apiKey"},
		}))
		resp := decodeWrite(t, rec.Code, rec.Body.Bytes())
		assert.NotContains(t, rec.Body.String(), newKey)
		assert.NotContains(t, rec.Body.String(), plaintextKey)
		assert.True(t, proto.Equal(&agentrewire.CtlFieldChange{Field: "apiKey", Secret: true}, resp.GetChanges()[0].GetFields()[0]))
		w := f.writer(agentrewire.CtlKind_CTL_KIND_PROVIDER).onlyWrite(t, "update")
		assert.Equal(t, newKey, w.Next.GetProvider().GetApiKey())
		assert.NotContains(t, w.Cur.GetProvider().GetApiKey(), "0123456789", "当前值只以掩码出现")
	})
	t.Run("没写 api key：网关拿到的是空（沿用），不是当前的掩码", func(t *testing.T) {
		f := newWriteFixture()
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_PROVIDER, Id: 1,
			Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
			Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: &agentrewire.CtlProvider{Name: "claude"}}},
			Fields:   []string{"name"},
		}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Empty(t, f.writer(agentrewire.CtlKind_CTL_KIND_PROVIDER).onlyWrite(t, "update").Next.GetProvider().GetApiKey())
	})
	t.Run("openclaw token：只有 secret 标记，当前 token 不下传", func(t *testing.T) {
		f := newWriteFixture()
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_BACKEND, Id: 9,
			Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
			Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Token: "new-gateway-token"}}},
			Fields:   []string{"token"},
		}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "new-gateway-token")
		assert.NotContains(t, rec.Body.String(), "gateway-secret-token")
		w := f.writer(agentrewire.CtlKind_CTL_KIND_BACKEND).onlyWrite(t, "update")
		assert.Equal(t, "new-gateway-token", w.Next.GetBackend().GetToken())
		assert.Empty(t, w.Cur.GetBackend().GetToken())
	})
	t.Run("空白 token：边界处 trim 一次，网关拿到空串，与显式清除同一路径", func(t *testing.T) {
		f := newWriteFixture()
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_BACKEND, Id: 9,
			Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
			Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Token: "   "}}},
			Fields:   []string{"token"},
		}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		w := f.writer(agentrewire.CtlKind_CTL_KIND_BACKEND).onlyWrite(t, "update")
		assert.Empty(t, w.Next.GetBackend().GetToken(), "空白不当成新令牌，trim 成空——走清除，不留下带空白的半成品")
	})
}

func TestWrite_GivenDepartmentCascadeDeleteThenCountsStated(t *testing.T) {
	f := newWriteFixture()
	rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_DELETE, Kind: agentrewire.CtlKind_CTL_KIND_DEPARTMENT, Id: 2,
		Caller: agentrewire.CtlCaller_CTL_CALLER_HUMAN, Cascade: true,
	}))
	resp := decodeWrite(t, rec.Code, rec.Body.Bytes())
	ch := resp.GetChanges()[0]
	assert.Equal(t, "eng", ch.GetName())
	assert.Equal(t, "also deletes 1 sub-department and 3 agents", ch.GetNote())
	w := f.writer(agentrewire.CtlKind_CTL_KIND_DEPARTMENT).onlyWrite(t, "delete")
	assert.True(t, w.Cascade)
	assert.Equal(t, int64(2), w.Cur.GetDepartment().GetId())
}

func TestWrite_GivenProjectMemberAddRemoveThenMergedMemberList(t *testing.T) {
	f := newWriteFixture()
	rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_PROJECT, Id: 1,
		Caller:               agentrewire.CtlCaller_CTL_CALLER_HUMAN,
		AddMemberAgentIds:    []int64{12},
		RemoveMemberAgentIds: []int64{3},
	}))
	resp := decodeWrite(t, rec.Code, rec.Body.Bytes())
	w := f.writer(agentrewire.CtlKind_CTL_KIND_PROJECT).onlyWrite(t, "update")
	assert.Equal(t, []int64{12}, w.Next.GetProject().GetMemberAgentIds())
	assert.True(t, w.Fields["memberAgentIds"])
	assert.True(t, proto.Equal(fieldChange("memberAgentIds", str("architect"), str("reviewer")), resp.GetChanges()[0].GetFields()[0]))
}

func TestWrite_GivenBackendConfigThenOnlyGivenKeysReplaced(t *testing.T) {
	f := newWriteFixture()
	rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_BACKEND, Id: 5,
		Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{ConfigJson: `{"defaultPermissionMode":"acceptEdits"}`}}},
		Fields:   []string{"configJson"},
	}))
	resp := decodeWrite(t, rec.Code, rec.Body.Bytes())
	w := f.writer(agentrewire.CtlKind_CTL_KIND_BACKEND).onlyWrite(t, "update")
	assert.JSONEq(t, `{"defaultPermissionMode":"acceptEdits","defaultModel":"x"}`, w.Next.GetBackend().GetConfigJson())
	fields := resp.GetChanges()[0].GetFields()
	require.Len(t, fields, 1)
	assert.True(t, proto.Equal(fieldChange("config.defaultPermissionMode", str("plan"), str("acceptEdits")), fields[0]))
}

func TestWrite_RequestErrors(t *testing.T) {
	t.Run("update 目标不存在 → 404，不写", func(t *testing.T) {
		f := newWriteFixture()
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_DELETE, Kind: agentrewire.CtlKind_CTL_KIND_AGENT, Id: 404,
			Caller: agentrewire.CtlCaller_CTL_CALLER_HUMAN,
		}))
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "agent id 404 not found")
		assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
	})
	t.Run("只读字段 → 400，不写", func(t *testing.T) {
		f := newWriteFixture()
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_AGENT, Id: 12,
			Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
			Resource: agentDocRes(&agentrewire.CtlAgent{SystemBadge: "system"}),
			Fields:   []string{"systemBadge"},
		}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "systemBadge")
		assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
	})
	t.Run("CLI 路径覆盖不在范围内：cliPath 只读 → 400，不写", func(t *testing.T) {
		for _, op := range []agentrewire.CtlOp{agentrewire.CtlOp_CTL_OP_CREATE, agentrewire.CtlOp_CTL_OP_UPDATE} {
			f := newWriteFixture()
			req := &agentrewire.CtlWriteRequest{
				Op: op, Kind: agentrewire.CtlKind_CTL_KIND_BACKEND,
				Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
				Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Name: "b", Type: "codex", CliPath: "/usr/bin/codex"}}},
				Fields:   []string{"cliPath"},
			}
			if op == agentrewire.CtlOp_CTL_OP_UPDATE {
				req.Id = 5
			} else {
				req.Fields = append(req.Fields, "name", "type")
			}
			rec := postResources(t, f.h, testToken, writeBody(t, req))
			assert.Equal(t, http.StatusBadRequest, rec.Code, op.String())
			assert.Contains(t, rec.Body.String(), "cliPath")
			assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_BACKEND).count())
		}
	})
	t.Run("创建后不可改的字段 → 400", func(t *testing.T) {
		f := newWriteFixture()
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_BACKEND, Id: 5,
			Caller:   agentrewire.CtlCaller_CTL_CALLER_HUMAN,
			Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Type: "codex"}}},
			Fields:   []string{"type"},
		}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("文档分支与 kind 不符 → 400", func(t *testing.T) {
		f := newWriteFixture()
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_CREATE, Kind: agentrewire.CtlKind_CTL_KIND_PROJECT,
			Caller: agentrewire.CtlCaller_CTL_CALLER_HUMAN, Resource: agentDocRes(&agentrewire.CtlAgent{Name: "x"}), Fields: []string{"name"},
		}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("不认识的 op → 400，绝不落到 delete", func(t *testing.T) {
		f := newWriteFixture()
		rec := postResources(t, f.h, testToken, `{"write":{"op":7,"kind":"CTL_KIND_AGENT","id":"12","caller":"CTL_CALLER_HUMAN"}}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
	})
	t.Run("服务层拒绝 → 消息原样透出", func(t *testing.T) {
		f := newWriteFixture()
		f.writer(agentrewire.CtlKind_CTL_KIND_PROVIDER).err = errors.New("供应商仍被后端引用")
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_DELETE, Kind: agentrewire.CtlKind_CTL_KIND_PROVIDER, Id: 1,
			Caller: agentrewire.CtlCaller_CTL_CALLER_HUMAN,
		}))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "供应商仍被后端引用")
	})
	t.Run("该类资源的写网关未接线 → 503", func(t *testing.T) {
		f := newWriteFixture()
		f.h.resources.Writers = nil
		rec := postResources(t, f.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
			Op: agentrewire.CtlOp_CTL_OP_DELETE, Kind: agentrewire.CtlKind_CTL_KIND_AGENT, Id: 12,
			Caller: agentrewire.CtlCaller_CTL_CALLER_HUMAN,
		}))
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}

// ---- 会话调用：在该会话里出一张 ctl 审批卡 ----

func sessionWrite(t *testing.T) string {
	return writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_AGENT, Id: 12,
		Caller:   agentrewire.CtlCaller_CTL_CALLER_SESSION,
		Command:  "agrctl update agent reviewer --department qa",
		Resource: agentDocRes(&agentrewire.CtlAgent{DepartmentId: 6}),
		Fields:   []string{"departmentId"},
	})
}

func TestWrite_GivenSessionCallerWhenApprovedThenCardThenExecute(t *testing.T) {
	f := newWriteFixture()
	yes := true
	f.session.answer = &yes
	status, body, pending := realPost(t, f.h, f.signer.MintToken(7, 42), sessionWrite(t))
	resp := decodeWrite(t, status, body)
	assert.Equal(t, []string{"session=42"}, pending, "阻塞前先以 102 告诉 agrctl 卡片在哪个会话")
	assert.Equal(t, int64(12), resp.GetId())
	assert.Equal(t, int64(6), f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).onlyWrite(t, "update").Next.GetAgent().GetDepartmentId(), "批准后才执行")

	sessions, begun, finished := f.session.snapshot()
	assert.Equal(t, []int64{42}, sessions, "卡片登记在 token 绑定的会话里")
	require.Len(t, begun, 1)
	blk := begun[0]
	assert.Equal(t, agenttool.KeyCtl, blk.ToolKey)
	assert.Equal(t, "ctl_update_agent", blk.ToolName)
	assert.Equal(t, "pending", blk.Status)
	in, err := blocks.ParseCtlApprovalInput(blk.ToolInput)
	require.NoError(t, err)
	assert.Equal(t, blocks.CtlApprovalInput{
		Command: "agrctl update agent reviewer --department qa",
		Changes: []blocks.CtlApprovalChange{{
			Op: "update", Kind: "agent", ID: 12, Name: "reviewer",
			Fields: []blocks.CtlApprovalField{{Field: "departmentId", Before: str("eng"), After: str("qa")}},
		}},
	}, in)
	assert.Equal(t, []string{"approved|已更新 agent reviewer"}, finished)
}

// TestWrite_CardCommandNeverCarriesPlaintextSecret：命令行由客户端给出，执行者不信它已
// 脱敏——请求里的密钥明文出现在命令行里时，卡片上换成 …。
func TestWrite_CardCommandNeverCarriesPlaintextSecret(t *testing.T) {
	f := newWriteFixture()
	no := false
	f.session.answer = &no
	const newKey = "sk-new-9876543210fedcba"
	_, _, _ = realPost(t, f.h, f.signer.MintToken(7, 42), writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_PROVIDER, Id: 1,
		Caller:   agentrewire.CtlCaller_CTL_CALLER_SESSION,
		Command:  "agrctl update provider anthropic --api-key=" + newKey,
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: &agentrewire.CtlProvider{ApiKey: newKey}}},
		Fields:   []string{"apiKey"},
	}))
	_, begun, _ := f.session.snapshot()
	require.Len(t, begun, 1)
	in, err := blocks.ParseCtlApprovalInput(begun[0].ToolInput)
	require.NoError(t, err)
	assert.Equal(t, "agrctl update provider anthropic --api-key=…", in.Command)
	assert.Equal(t, []blocks.CtlApprovalField{{Field: "apiKey", Secret: true}}, in.Changes[0].Fields)
}

func TestWrite_GivenSessionCallerWhenRejectedThenNothingWritten(t *testing.T) {
	f := newWriteFixture()
	no := false
	f.session.answer = &no
	status, body, pending := realPost(t, f.h, f.signer.MintToken(7, 42), sessionWrite(t))
	assert.Equal(t, http.StatusForbidden, status)
	assert.JSONEq(t, `{"error":"rejected in session #42"}`, string(body))
	assert.Equal(t, []string{"session=42"}, pending)
	assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
	_, _, finished := f.session.snapshot()
	assert.Equal(t, []string{"denied|"}, finished)
}

func TestWrite_GivenSessionCallerWhenNobodyAnswersThenTimesOut(t *testing.T) {
	f := newWriteFixture()
	f.h.approvalTimeout = 50 * time.Millisecond
	status, body, _ := realPost(t, f.h, f.signer.MintToken(7, 42), sessionWrite(t))
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.JSONEq(t, `{"error":"approval timed out"}`, string(body))
	assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
	_, _, finished := f.session.snapshot()
	assert.Equal(t, []string{"expired|"}, finished)
}

func TestWrite_GivenSessionCallerWhenApprovedButServiceFailsThenCardSaysSo(t *testing.T) {
	f := newWriteFixture()
	yes := true
	f.session.answer = &yes
	f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).err = errors.New("部门不存在")
	status, body, _ := realPost(t, f.h, f.signer.MintToken(7, 42), sessionWrite(t))
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.JSONEq(t, `{"error":"部门不存在"}`, string(body))
	_, _, finished := f.session.snapshot()
	assert.Equal(t, []string{"approved|执行失败：部门不存在"}, finished)
}

func TestWrite_GivenSessionCallerWhenCardCannotBeRaisedThenFailsWithoutWriting(t *testing.T) {
	f := newWriteFixture()
	f.session.beginErr = errors.New("no active turn for session 42")
	rec := postResources(t, f.h, f.signer.MintToken(7, 42), sessionWrite(t))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "no active turn for session 42")
	assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
}

// TestWrite_SessionTokenCannotClaimHuman：会话 token 自称人工调用也照样要审批。
func TestWrite_SessionTokenCannotClaimHuman(t *testing.T) {
	f := newWriteFixture()
	no := false
	f.session.answer = &no
	body := strings.Replace(sessionWrite(t), "CTL_CALLER_SESSION", "CTL_CALLER_HUMAN", 1)
	status, _, _ := realPost(t, f.h, f.signer.MintToken(7, 42), body)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
}

// ---- 外部调用：挂上桌面端待审批队列 ----

func externalWrite(t *testing.T) string {
	return strings.Replace(sessionWrite(t), "CTL_CALLER_SESSION", "CTL_CALLER_EXTERNAL", 1)
}

// answerWhenQueued 扮演桌面弹窗：等第一条请求入队后作答。
func (f *writeFixture) answerWhenQueued(allow bool) {
	for {
		f.desktop.mu.Lock()
		if len(f.desktop.queued) > 0 {
			id := f.desktop.queued[0].RequestID
			f.desktop.mu.Unlock()
			_ = f.desktop.Answer(id, allow)
			return
		}
		f.desktop.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWrite_GivenExternalCallerWhenDesktopApprovesThenExecute(t *testing.T) {
	f := newWriteFixture()
	go f.answerWhenQueued(true)
	status, body, pending := realPost(t, f.h, testToken, externalWrite(t))
	decodeWrite(t, status, body)
	assert.Equal(t, []string{"desktop"}, pending)
	require.Len(t, f.desktop.queued, 1)
	q := f.desktop.queued[0]
	assert.NotEmpty(t, q.RequestID)
	assert.Equal(t, "agrctl update agent reviewer --department qa", q.Input.Command)
	assert.Equal(t, "departmentId", q.Input.Changes[0].Fields[0].Field)
	assert.Equal(t, int64(6), f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).onlyWrite(t, "update").Next.GetAgent().GetDepartmentId())
}

// TestWrite_GivenDataChangedWhileAwaitingApprovalThenExecutesAgainstFreshData：审批最多挂 4
// 分钟，其间别处改了同一资源的其他字段；批准后执行必须基于当时的数据只写本次字段，不能
// 用挂起前的快照把别处的修改改回去。
func TestWrite_GivenDataChangedWhileAwaitingApprovalThenExecutesAgainstFreshData(t *testing.T) {
	f := newWriteFixture()
	go func() {
		for {
			f.desktop.mu.Lock()
			if len(f.desktop.queued) > 0 {
				id := f.desktop.queued[0].RequestID
				f.desktop.mu.Unlock()
				// 审批挂起期间，桌面端界面把 reviewer 改了名。
				f.data.agents[1] = &agentrewire.CtlAgent{Id: 12, Name: "reviewer-2", DepartmentId: 2, BackendIds: []int64{5}, Description: "reviews"}
				_ = f.desktop.Answer(id, true)
				return
			}
			f.desktop.mu.Unlock()
			time.Sleep(5 * time.Millisecond)
		}
	}()
	status, body, _ := realPost(t, f.h, testToken, externalWrite(t))
	resp := decodeWrite(t, status, body)
	w := f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).onlyWrite(t, "update")
	assert.Equal(t, int64(6), w.Next.GetAgent().GetDepartmentId(), "本次字段照写")
	assert.Equal(t, "reviewer-2", w.Next.GetAgent().GetName(), "未列出的字段取批准时的当前值，不回滚别处的修改")
	assert.Equal(t, "reviewer-2", w.Cur.GetAgent().GetName())
	assert.Equal(t, "reviewer-2", resp.GetName())
}

// TestWrite_GivenTargetDeletedWhileAwaitingApprovalThenNotFoundAndCardSaysSo：批准前目标
// 已被删掉 → 不写，按 404 回，会话卡片写明执行失败。
func TestWrite_GivenTargetDeletedWhileAwaitingApprovalThenNotFoundAndCardSaysSo(t *testing.T) {
	f := newWriteFixture()
	f.session.answer = new(bool)
	*f.session.answer = true
	f.session.onBegin = func() { f.data.agents = f.data.agents[:1] }
	status, body, _ := realPost(t, f.h, f.signer.MintToken(7, 42), sessionWrite(t))
	assert.Equal(t, http.StatusNotFound, status, string(body))
	assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
	_, _, finished := f.session.snapshot()
	assert.Equal(t, []string{"approved|执行失败：agent id 12 not found"}, finished)
}

func TestWrite_GivenExternalCallerWithCallerInfoThenQueueCarriesItUnchanged(t *testing.T) {
	f := newWriteFixture()
	yes := true
	f.desktop.autoAnswer = &yes
	body := writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_AGENT, Id: 12,
		Caller:     agentrewire.CtlCaller_CTL_CALLER_EXTERNAL,
		CallerInfo: &agentrewire.CtlCallerInfo{ParentProcess: "codex", Pid: 48213, Cwd: "/Users/me/Code/agentre"},
		Command:    "agrctl update agent reviewer --department qa",
		Resource:   agentDocRes(&agentrewire.CtlAgent{DepartmentId: 6}),
		Fields:     []string{"departmentId"},
	})
	status, resp, _ := realPost(t, f.h, testToken, body)
	decodeWrite(t, status, resp)
	require.Len(t, f.desktop.queued, 1)
	assert.Equal(t, &CallerInfo{ParentProcess: "codex", Pid: 48213, WorkingDir: "/Users/me/Code/agentre"}, f.desktop.queued[0].Caller)
}

func TestWrite_GivenExternalCallerWithoutCallerInfoThenNoCallerRow(t *testing.T) {
	f := newWriteFixture()
	yes := true
	f.desktop.autoAnswer = &yes
	status, resp, _ := realPost(t, f.h, testToken, externalWrite(t))
	decodeWrite(t, status, resp)
	require.Len(t, f.desktop.queued, 1)
	assert.Nil(t, f.desktop.queued[0].Caller)
}

func TestWrite_GivenExternalCallerWhenDesktopRejectsThenNothingWritten(t *testing.T) {
	f := newWriteFixture()
	go f.answerWhenQueued(false)
	status, body, _ := realPost(t, f.h, testToken, externalWrite(t))
	assert.Equal(t, http.StatusForbidden, status)
	assert.JSONEq(t, `{"error":"rejected in the Agentre desktop"}`, string(body))
	assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
}

func TestWrite_GivenExternalCallerWhenNobodyAnswersThenWithdrawnAndTimesOut(t *testing.T) {
	f := newWriteFixture()
	f.h.approvalTimeout = 50 * time.Millisecond
	status, body, _ := realPost(t, f.h, testToken, externalWrite(t))
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.JSONEq(t, `{"error":"approval timed out"}`, string(body))
	require.Len(t, f.desktop.queued, 1)
	id := f.desktop.queued[0].RequestID
	assert.Equal(t, []string{id}, f.desktop.withdrawn, "超时即撤下，等同拒绝")
	assert.Error(t, f.desktop.Answer(id, true), "撤下后再答无效")
	assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
}

// TestWrite_GivenDesktopAnswerLandsAtTheDeadlineThenItIsHonoured：弹窗的批准恰好在超时
// 那一刻抢先入账——Answer 已经对用户报了成功，执行者不能再回 504、什么都不做。
func TestWrite_GivenDesktopAnswerLandsAtTheDeadlineThenItIsHonoured(t *testing.T) {
	f := newWriteFixture()
	f.h.approvalTimeout = 20 * time.Millisecond
	yes := true
	f.desktop.answerAtDeadline = &yes
	status, body, _ := realPost(t, f.h, testToken, externalWrite(t))
	decodeWrite(t, status, body)
	assert.Equal(t, int64(6), f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).onlyWrite(t, "update").Next.GetAgent().GetDepartmentId())
}

// TestWrite_GivenCallerGoneAfterApprovalThenWriteStillCompletes：人已经批准了，写入就要
// 完整落下——agrctl 这时被杀掉，不能让一次多步写入停在半路。
func TestWrite_GivenCallerGoneAfterApprovalThenWriteStillCompletes(t *testing.T) {
	f := newWriteFixture()
	yes := true
	f.desktop.autoAnswer = &yes
	entered := make(chan struct{})
	canceled := make(chan bool, 1)
	f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).during = func(ctx context.Context) {
		close(entered)
		select {
		case <-ctx.Done():
			canceled <- true
		case <-time.After(500 * time.Millisecond):
			canceled <- false
		}
	}
	srv := httptest.NewServer(f.h)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/ctl/v1/resources", strings.NewReader(externalWrite(t)))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testToken)
	go func() {
		<-entered
		cancel() // agrctl 在写入进行中被杀
	}()
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
	assert.False(t, <-canceled, "an approved write runs to completion even if the caller goes away")
}

func TestWrite_GivenExternalCallerWithoutDesktopQueueThen503(t *testing.T) {
	f := newWriteFixture()
	f.h.external = nil
	rec := postResources(t, f.h, testToken, externalWrite(t))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Zero(t, f.writer(agentrewire.CtlKind_CTL_KIND_AGENT).count())
}

func TestApprovalTimeoutIsFourMinutes(t *testing.T) {
	assert.Equal(t, 4*time.Minute, newCtlSvc().approvalTimeout, "spec 决策 12，与 orgtool 一致")
}

// 清除 token（空值）只在确定没设置时才不算变更；状态未知（绑定设备离线）时照样出 secret 行，
// 审批的人不能因为看不到状态就漏看一次清除。
func TestSecretChange_GivenClearingTokenThenChangeUnlessKnownUnset(t *testing.T) {
	backend := func(state agentrewire.CtlTokenState) *agentrewire.CtlResource {
		return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Type: "openclaw", TokenState: state}}}
	}
	assert.Nil(t, secretChange("token", backend(agentrewire.CtlTokenState_CTL_TOKEN_STATE_UNSET), ""))
	for _, state := range []agentrewire.CtlTokenState{agentrewire.CtlTokenState_CTL_TOKEN_STATE_SET, agentrewire.CtlTokenState_CTL_TOKEN_STATE_UNKNOWN} {
		assert.Equal(t, &agentrewire.CtlFieldChange{Field: "token", Secret: true}, secretChange("token", backend(state), ""), state.String())
	}
}
