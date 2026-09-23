package ctlcmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// fakeExecutor 是 /ctl/v1/resources 的假执行者：只按 id 读写一份内存数据，记录收到的
// 写请求。它和真执行者一样不认识名字——名字解析是 agrctl 客户端的事。
type fakeExecutor struct {
	t     *testing.T
	token string

	mu     sync.Mutex
	items  map[agentrewire.CtlKind][]*agentrewire.CtlResource
	writes []*agentrewire.CtlWriteRequest
	// writeErr 非空时，写请求一律以 409 + 这条消息失败（审批之后，如被拒、超时、服务层拒绝）。
	writeErr string
	// failBeforeApproval 非空时，写请求在出审批之前就以 404 + 这条消息失败（如目标不存在）。
	failBeforeApproval string
	// nextID 是 create 返回的新 id。
	nextID int64
}

func deptDoc(d *agentrewire.CtlDepartment) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Department{Department: d}}
}

func projectDoc(p *agentrewire.CtlProject) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Project{Project: p}}
}

func providerDoc(p *agentrewire.CtlProvider) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: p}}
}

func modelDoc(m *agentrewire.CtlModel) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Model{Model: m}}
}

func backendDoc(b *agentrewire.CtlBackend) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: b}}
}

// newFakeExecutor 起一个带固定数据集的假执行者。数据集里故意放了两个同名的 docs 项目
// （在不同父项目下），用来走歧义路径。
func newFakeExecutor(t *testing.T, token string) (*fakeExecutor, *httptest.Server) {
	t.Helper()
	f := &fakeExecutor{t: t, token: token, nextID: 99, items: map[agentrewire.CtlKind][]*agentrewire.CtlResource{
		agentrewire.CtlKind_CTL_KIND_DEPARTMENT: {
			deptDoc(&agentrewire.CtlDepartment{Id: 1, Name: "总部"}),
			deptDoc(&agentrewire.CtlDepartment{Id: 2, Name: "研发部", ParentId: 1, LeadAgentId: 3}),
			deptDoc(&agentrewire.CtlDepartment{Id: 6, Name: "质量部", ParentId: 1}),
			deptDoc(&agentrewire.CtlDepartment{Id: 7, Name: "临时小组", ParentId: 2}),
		},
		agentrewire.CtlKind_CTL_KIND_AGENT: {
			agentDoc(&agentrewire.CtlAgent{Id: 3, Name: "architect", DepartmentId: 2, BackendIds: []int64{5}, Pinned: true}),
			agentDoc(&agentrewire.CtlAgent{Id: 12, Name: "reviewer", DepartmentId: 2, BackendIds: []int64{5}}),
			agentDoc(&agentrewire.CtlAgent{Id: 20, Name: "tester", DepartmentId: 6, BackendIds: []int64{9}}),
		},
		agentrewire.CtlKind_CTL_KIND_PROJECT: {
			projectDoc(&agentrewire.CtlProject{Id: 1, Name: "agentre", Path: "/src/agentre", MemberAgentIds: []int64{3}}),
			projectDoc(&agentrewire.CtlProject{Id: 2, Name: "agentre-hub", Path: "/src/hub"}),
			projectDoc(&agentrewire.CtlProject{Id: 8, Name: "docs", ParentId: 1, Path: "/src/agentre/docs"}),
			projectDoc(&agentrewire.CtlProject{Id: 15, Name: "docs", ParentId: 2, Path: "/src/hub/docs"}),
		},
		agentrewire.CtlKind_CTL_KIND_PROVIDER: {
			providerDoc(&agentrewire.CtlProvider{Id: 1, Name: "anthropic", Type: "anthropic", Enabled: true, ApiKey: "sk-a••••••••1111", ApiKeySet: true, DefaultModelKey: "0b6c-23"}),                                                             //nolint:gosec // 掩码后的假数据，不是凭据。
			providerDoc(&agentrewire.CtlProvider{Id: 4, Name: "openrouter", Type: "openai-chat", BaseUrl: "https://openrouter.ai/api/v1", Enabled: true, ApiKey: "sk-o••••••••3f9a", ApiKeySet: true, DefaultModelKey: "0b6c-21", BackendRefs: 2}), //nolint:gosec // 掩码后的假数据，不是凭据。
		},
		agentrewire.CtlKind_CTL_KIND_MODEL: {
			// Key 是服务生成的 ModelKey（UUID）；用户看得到、用来定位的是 ModelId。
			modelDoc(&agentrewire.CtlModel{Id: 21, ProviderId: 4, Key: "0b6c-21", ModelId: "openai/gpt-5.1", ContextWindow: 400000, Enabled: true, IsDefault: true}),
			modelDoc(&agentrewire.CtlModel{Id: 22, ProviderId: 4, Key: "0b6c-22", ModelId: "openai/gpt-5.1-mini", ContextWindow: 400000, Enabled: true}),
			modelDoc(&agentrewire.CtlModel{Id: 23, ProviderId: 1, Key: "0b6c-23", ModelId: "claude-opus-5", Enabled: true, IsDefault: true}),
			// 同一提供方下 ModelId 重复：走歧义路径。
			modelDoc(&agentrewire.CtlModel{Id: 24, ProviderId: 1, Key: "0b6c-24", ModelId: "claude/dup", Enabled: true}),
			modelDoc(&agentrewire.CtlModel{Id: 25, ProviderId: 1, Key: "0b6c-25", ModelId: "claude/dup", Enabled: true}),
		},
		agentrewire.CtlKind_CTL_KIND_BACKEND: {
			backendDoc(&agentrewire.CtlBackend{Id: 5, Name: "claude-local", Type: "claudecode", ProviderId: 1}),
			backendDoc(&agentrewire.CtlBackend{Id: 9, Name: "codex-remote", Type: "codex", Device: "build-box", ProviderId: 4, ModelId: 21}),
			backendDoc(&agentrewire.CtlBackend{Id: 10, Name: "claw", Type: "openclaw", TokenState: agentrewire.CtlTokenState_CTL_TOKEN_STATE_SET}),
		},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/ctl/v1/resources", f.serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeExecutor) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid control token"}`)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var req agentrewire.CtlRequest
	if err := protojson.Unmarshal(raw, &req); err != nil {
		f.t.Errorf("fake executor: request is not a protojson CtlRequest: %v (%s)", err, raw)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var resp agentrewire.CtlResponse
	switch op := req.GetOp().(type) {
	case *agentrewire.CtlRequest_List:
		resp.Result = &agentrewire.CtlResponse_List{List: &agentrewire.CtlListResponse{Items: f.items[op.List.GetKind()]}}
	case *agentrewire.CtlRequest_Get:
		doc := f.byID(op.Get.GetKind(), op.Get.GetId())
		if doc == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not found"}`)
			return
		}
		resp.Result = &agentrewire.CtlResponse_Get{Get: &agentrewire.CtlGetResponse{Resource: doc}}
	case *agentrewire.CtlRequest_Write:
		f.writes = append(f.writes, op.Write)
		if f.failBeforeApproval != "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"`+f.failBeforeApproval+`"}`)
			return
		}
		// 和真执行者一样：要审批的调用方先收到 102，告诉它去哪里批准。
		switch op.Write.GetCaller() {
		case agentrewire.CtlCaller_CTL_CALLER_SESSION:
			w.Header().Set(approvalPendingHeader, "session=42")
			w.WriteHeader(http.StatusProcessing)
		case agentrewire.CtlCaller_CTL_CALLER_EXTERNAL:
			w.Header().Set(approvalPendingHeader, "desktop")
			w.WriteHeader(http.StatusProcessing)
		}
		w.Header().Del(approvalPendingHeader)
		if f.writeErr != "" {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":"`+f.writeErr+`"}`)
			return
		}
		id := op.Write.GetId()
		if op.Write.GetOp() == agentrewire.CtlOp_CTL_OP_CREATE {
			id = f.nextID
		}
		resp.Result = &agentrewire.CtlResponse_Write{Write: &agentrewire.CtlWriteResponse{Id: id}}
	default:
		f.t.Errorf("fake executor: request without op: %s", raw)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	out, err := protojson.Marshal(&resp)
	if err != nil {
		f.t.Fatalf("marshal: %v", err)
	}
	_, _ = w.Write(out)
}

func (f *fakeExecutor) byID(kind agentrewire.CtlKind, id int64) *agentrewire.CtlResource {
	for _, it := range f.items[kind] {
		if docID(it) == id {
			return it
		}
	}
	return nil
}

func docID(r *agentrewire.CtlResource) int64 {
	switch d := r.GetDoc().(type) {
	case *agentrewire.CtlResource_Agent:
		return d.Agent.GetId()
	case *agentrewire.CtlResource_Department:
		return d.Department.GetId()
	case *agentrewire.CtlResource_Project:
		return d.Project.GetId()
	case *agentrewire.CtlResource_Provider:
		return d.Provider.GetId()
	case *agentrewire.CtlResource_Model:
		return d.Model.GetId()
	case *agentrewire.CtlResource_Backend:
		return d.Backend.GetId()
	}
	return 0
}

// onlyWrite 断言恰好收到一条写请求并返回它。
func (f *fakeExecutor) onlyWrite(t *testing.T) *agentrewire.CtlWriteRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 1 {
		t.Fatalf("executor received %d write requests, want exactly 1", len(f.writes))
	}
	return proto.Clone(f.writes[0]).(*agentrewire.CtlWriteRequest)
}

func (f *fakeExecutor) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

// term 描述一次调用的终端环境：stdin 是否为 TTY、无回显读到的密钥、stdin 的内容。
type term struct {
	tty     bool
	secrets []string
	stdin   string
}

// cliResult 是一次 agrctl 调用的可观察结果。
type cliResult struct {
	code           int
	stdout, stderr string
	// prompts 是无回显读取时给出的提示。
	prompts []string
}

func runWith(args []string, lookupEnv func(string) (string, bool), tm term) cliResult {
	var out, errb bytes.Buffer
	res := cliResult{}
	secrets := append([]string(nil), tm.secrets...)
	s := &sys{
		stdin:      strings.NewReader(tm.stdin),
		stdout:     &out,
		stderr:     &errb,
		lookupEnv:  lookupEnv,
		stdinIsTTY: tm.tty,
		readSecret: func(prompt string) (string, error) {
			res.prompts = append(res.prompts, prompt)
			if len(secrets) == 0 {
				return "", io.EOF
			}
			v := secrets[0]
			secrets = secrets[1:]
			return v, nil
		},
	}
	res.code = run(args, s)
	res.stdout, res.stderr = out.String(), errb.String()
	return res
}
