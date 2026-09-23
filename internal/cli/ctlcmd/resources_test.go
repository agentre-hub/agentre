package ctlcmd

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/ctlendpoint"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

const tok = "tok"

// noEnv 是一个什么环境变量都没有的 lookupEnv。
func noEnv(string) (string, bool) { return "", false }

// handshakeEnv 让 agrctl 只能从握手文件拿到端点与 token（不是会话调用）。
func handshakeEnv(t *testing.T, srv *httptest.Server) func(string) (string, bool) {
	t.Helper()
	dir := t.TempDir()
	if err := ctlendpoint.Write(dir, ctlendpoint.Endpoint{URL: srv.URL, Token: tok}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_DATA_DIR", dir)
	return noEnv
}

// tableRows 把表格输出拆成「行 → 按空白切开的列」。
func tableRows(out string) [][]string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, strings.Fields(line))
	}
	return rows
}

func wantCode(t *testing.T, r cliResult, code int) {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit code = %d, want %d (stdout=%q stderr=%q)", r.code, code, r.stdout, r.stderr)
	}
}

// ─── list ───────────────────────────────────────────────────────────────

func TestList_GivenAgentsWhenListedThenTableStartsWithIDAndShowsNamesNotIDs(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"list", "agents"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	rows := tableRows(r.stdout)
	if got := rows[0]; !slices.Equal(got, []string{"ID", "NAME", "DEPARTMENT", "BACKENDS", "PINNED"}) {
		t.Fatalf("header = %v", got)
	}
	if got := rows[1]; !slices.Equal(got, []string{"3", "architect", "研发部", "claude-local", "yes"}) {
		t.Fatalf("first row = %v", got)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want header + 3 agents", len(rows))
	}
}

func TestList_GivenDepartmentFilterWhenListedThenOnlyThatDepartmentsAgents(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"list", "agents", "--department", "质量部"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	rows := tableRows(r.stdout)
	if len(rows) != 2 || rows[1][1] != "tester" {
		t.Fatalf("rows = %v, want only tester", rows)
	}
}

func TestList_GivenModelsOfProviderWhenListedThenDefaultMarked(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"list", "models", "--provider", "openrouter"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	rows := tableRows(r.stdout)
	if len(rows) != 3 {
		t.Fatalf("rows = %v, want header + 2 openrouter models", rows)
	}
	if rows[0][0] != "ID" || rows[1][1] != "openai/gpt-5.1" || rows[1][len(rows[1])-1] != "*" {
		t.Fatalf("rows = %v, want ID first and openai/gpt-5.1 marked default", rows)
	}
	if strings.Contains(r.stdout, "claude-opus-5") || strings.Contains(r.stdout, "0b6c-") {
		t.Fatalf("stdout = %q, must not include other providers' models", r.stdout)
	}
}

func TestList_GivenJSONOutputWhenListedThenArrayWithMaskedKeys(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"list", "providers", "-o", "json"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	var got []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("stdout is not a JSON array: %v (%q)", err, r.stdout)
	}
	if len(got) != 2 || got[1]["name"] != "openrouter" || got[1]["apiKey"] != "sk-o••••••••3f9a" {
		t.Fatalf("json = %v", got)
	}
}

func TestList_GivenUnknownFormatOrResourceThenUsageError(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	for _, args := range [][]string{
		{"list", "providers", "-o", "yaml"},
		{"list", "widgets"},
		{"list"},
		{"list", "agents", "--bogus"},
	} {
		r := runWith(args, envFor(srv, tok), term{})
		wantCode(t, r, 2)
		if !strings.HasPrefix(r.stderr, "Error:") {
			t.Fatalf("%v: stderr = %q, want Error: prefix", args, r.stderr)
		}
	}
}

// ─── get and locating ──────────────────────────────────────────────────

func TestGet_GivenParentChildPathWhenGotThenThatProjectAsJSON(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"get", "project", "agentre/docs"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, r.stdout)
	}
	if got["id"] != float64(8) || got["parent"] != "agentre" {
		t.Fatalf("json = %v, want project 8 under agentre", got)
	}
}

func TestGet_GivenNumericIDWhenGotThenThatResource(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"get", "project", "15"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	if !strings.Contains(r.stdout, `"parent": "agentre-hub"`) {
		t.Fatalf("stdout = %q, want project 15", r.stdout)
	}
}

func TestGet_GivenAmbiguousNameWhenGotThenListsCandidatesAndExampleExit2(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"get", "project", "docs"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
	for _, want := range []string{
		`Error: project "docs" is ambiguous — 2 matches:`,
		"8", "agentre/docs",
		"15", "agentre-hub/docs",
		"e.g. agrctl get project agentre/docs",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("stderr = %q, want %q", r.stderr, want)
		}
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty", r.stdout)
	}
}

func TestGet_GivenUnknownNameThenNotFoundExit1(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"delete", "agent", "nobody"}, envFor(srv, tok), term{})
	wantCode(t, r, 1)
	if strings.TrimSpace(r.stderr) != `Error: agent "nobody" not found` {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

func TestGet_GivenProviderSlashModelIDWhenGotThenSplitOnFirstSlash(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"get", "model", "openrouter/openai/gpt-5.1"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	var got map[string]any
	_ = json.Unmarshal([]byte(r.stdout), &got)
	if got["id"] != float64(21) || got["provider"] != "openrouter" || got["modelId"] != "openai/gpt-5.1" || got["key"] != "0b6c-21" {
		t.Fatalf("json = %v, want model 21 with its ModelKey shown", got)
	}
}

func TestGet_GivenModelKeyInsteadOfModelIDThenNotFound(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"get", "model", "openrouter/0b6c-21"}, envFor(srv, tok), term{})
	wantCode(t, r, 1)
	if !strings.Contains(r.stderr, `model "openrouter/0b6c-21" not found`) {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

func TestGet_GivenRepeatedModelIDUnderOneProviderThenAmbiguousWithIDExample(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"get", "model", "anthropic/claude/dup"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
	for _, want := range []string{
		`Error: model "anthropic/claude/dup" is ambiguous — 2 matches:`,
		"24", "25",
		"e.g. agrctl get model 24",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("stderr = %q, want %q", r.stderr, want)
		}
	}
	// 数字 id 仍然可用。
	r = runWith([]string{"get", "model", "25"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
}

func TestGet_GivenProviderThenMaskedKeyModelsAndReferences(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"get", "provider", "openrouter"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	var got struct {
		APIKey       string `json:"apiKey"`
		DefaultModel string `json:"defaultModel"`
		Models       []struct {
			Key string `json:"key"`
		} `json:"models"`
		References struct {
			AgentBackends int `json:"agentBackends"`
		} `json:"references"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "sk-o••••••••3f9a" || got.DefaultModel != "openai/gpt-5.1" || len(got.Models) != 2 || got.References.AgentBackends != 2 {
		t.Fatalf("json = %+v", got)
	}
}

func TestGet_GivenMissingLocatorThenUsageError(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"get", "project"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
}

// ─── create / update / delete ──────────────────────────────────────────

func TestUpdate_GivenOneFlagWhenUpdatedThenOnlyThatFieldSentByID(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "agent", "reviewer", "--department", "质量部"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if w.GetOp() != agentrewire.CtlOp_CTL_OP_UPDATE || w.GetKind() != agentrewire.CtlKind_CTL_KIND_AGENT || w.GetId() != 12 {
		t.Fatalf("write = %v", w)
	}
	if !slices.Equal(w.GetFields(), []string{"departmentId"}) || w.GetResource().GetAgent().GetDepartmentId() != 6 {
		t.Fatalf("write fields = %v resource = %v", w.GetFields(), w.GetResource())
	}
	if strings.TrimSpace(r.stdout) != "agent reviewer updated" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestUpdate_GivenNoFieldFlagsThenUsageErrorAndNoWrite(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "agent", "reviewer"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
	if f.writeCount() != 0 {
		t.Fatal("no write must be sent")
	}
}

func TestCreate_GivenDepartmentWithReferencesWhenCreatedThenResolvedIDsAndCreatedLine(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"create", "department", "--name", "研发二部", "--parent", "总部", "--lead", "architect"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	d := w.GetResource().GetDepartment()
	if w.GetOp() != agentrewire.CtlOp_CTL_OP_CREATE || d.GetName() != "研发二部" || d.GetParentId() != 1 || d.GetLeadAgentId() != 3 {
		t.Fatalf("write = %v", w)
	}
	for _, f := range []string{"name", "parentId", "leadAgentId"} {
		if !slices.Contains(w.GetFields(), f) {
			t.Fatalf("fields = %v, want %s", w.GetFields(), f)
		}
	}
	if strings.TrimSpace(r.stdout) != "department 研发二部 created (id 99)" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestCreate_GivenAgentWithRepeatedBackendThenAllExecTargetsInOrder(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"create", "agent", "--name", "helper", "--backend", "codex-remote", "--backend", "claude-local"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	if got := f.onlyWrite(t).GetResource().GetAgent().GetBackendIds(); !slices.Equal(got, []int64{9, 5}) {
		t.Fatalf("backendIds = %v, want [9 5]", got)
	}
}

func TestCreate_GivenAmbiguousFlagReferenceThenExit2AndNoWrite(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"create", "project", "--name", "guide", "--parent", "docs"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
	if !strings.Contains(r.stderr, `project "docs" is ambiguous`) || !strings.Contains(r.stderr, "--parent agentre/docs") {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if f.writeCount() != 0 {
		t.Fatal("no write must be sent")
	}
}

func TestUpdate_GivenProjectMemberFlagsThenAddRemoveIDs(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "project", "agentre", "--add-member", "reviewer", "--remove-member", "architect", "--color", "green"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if !slices.Equal(w.GetAddMemberAgentIds(), []int64{12}) || !slices.Equal(w.GetRemoveMemberAgentIds(), []int64{3}) {
		t.Fatalf("write = %v", w)
	}
	if !slices.Equal(w.GetFields(), []string{"color"}) || w.GetResource().GetProject().GetColor() != "green" {
		t.Fatalf("fields = %v", w.GetFields())
	}
}

func TestModel_GivenCreateWithoutKeyThenCreatedAsProviderSlashModelID(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"create", "model", "--provider", "openrouter", "--model-id", "qwen/qwen3-coder"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	m := w.GetResource().GetModel()
	if m.GetProviderId() != 4 || m.GetModelId() != "qwen/qwen3-coder" || m.GetKey() != "" || slices.Contains(w.GetFields(), "key") {
		t.Fatalf("write = %v", w)
	}
	if strings.TrimSpace(r.stdout) != "model openrouter/qwen/qwen3-coder created (id 99)" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestModel_GivenKeyFlagThenUsageErrorAndNoWrite(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	for _, args := range [][]string{
		{"create", "model", "--provider", "openrouter", "--key", "qwen3", "--model-id", "qwen/qwen3-coder"},
		{"update", "model", "openrouter/openai/gpt-5.1", "--key", "x"},
	} {
		r := runWith(args, envFor(srv, tok), term{})
		wantCode(t, r, 2)
	}
	if f.writeCount() != 0 {
		t.Fatal("no write must be sent")
	}
}

func TestProvider_GivenDefaultModelByModelIDThenSendsThatModelsKey(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "provider", "openrouter", "--default-model", "openai/gpt-5.1-mini"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	if got := f.onlyWrite(t).GetResource().GetProvider().GetDefaultModelKey(); got != "0b6c-22" {
		t.Fatalf("defaultModelKey = %q, want the ModelKey of openai/gpt-5.1-mini", got)
	}
}

func TestModel_GivenDefaultFlagThenIsDefaultField(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "model", "openrouter/openai/gpt-5.1-mini", "--default"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if w.GetId() != 22 || !slices.Equal(w.GetFields(), []string{"isDefault"}) || !w.GetResource().GetModel().GetIsDefault() {
		t.Fatalf("write = %v", w)
	}
}

func TestModel_GivenEnableAndDisableTogetherThenUsageError(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "model", "openrouter/openai/gpt-5.1", "--enable", "--disable"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
}

func TestDelete_GivenCascadeAndForceThenCarriedOnRequest(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"delete", "department", "临时小组", "--cascade"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if w.GetOp() != agentrewire.CtlOp_CTL_OP_DELETE || w.GetId() != 7 || !w.GetCascade() || w.GetForce() {
		t.Fatalf("write = %v", w)
	}
	if strings.TrimSpace(r.stdout) != "department 临时小组 deleted" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestDelete_GivenCascadeOnNonDepartmentThenUsageError(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"delete", "agent", "reviewer", "--cascade"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
}

func TestWrite_GivenExecutorRejectsThenMessagePassedThroughExit1(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	f.writeErr = "provider openrouter is referenced by 2 backends; pass --force to delete anyway"
	r := runWith([]string{"delete", "provider", "openrouter"}, envFor(srv, tok), term{})
	wantCode(t, r, 1)
	if strings.TrimSpace(r.stderr) != "waiting for approval in session #42 …\nError: "+f.writeErr {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

// ─── backends and --config ─────────────────────────────────────────────

func TestBackend_GivenConfigOfItsTypeThenSentAsConfigJSON(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"create", "backend", "--type", "codex", "--name", "codex-2", "--device", "build-box", "--provider", "openrouter", "--model", "openai/gpt-5.1",
		"--env", "A=1", "--config", `{"sandbox":"workspace-write","approval":"on-request"}`}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	b := f.onlyWrite(t).GetResource().GetBackend()
	if b.GetType() != "codex" || b.GetDevice() != "build-box" || b.GetProviderId() != 4 || b.GetModelId() != 21 || b.GetEnv()["A"] != "1" {
		t.Fatalf("backend = %v", b)
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(b.GetConfigJson()), &cfg); err != nil || cfg["sandbox"] != "workspace-write" {
		t.Fatalf("configJson = %q", b.GetConfigJson())
	}
}

func TestBackend_GivenConfigFileThenReadFromFile(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, []byte(`{"approval":"never"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runWith([]string{"update", "backend", "codex-remote", "--config-file", path}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if !slices.Equal(w.GetFields(), []string{"configJson"}) || !strings.Contains(w.GetResource().GetBackend().GetConfigJson(), "never") {
		t.Fatalf("write = %v", w)
	}
}

func TestBackend_GivenConfigKeyOfAnotherTypeThenUsageErrorAndNoWrite(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	for _, args := range [][]string{
		{"update", "backend", "codex-remote", "--config", `{"hermesUrl":"http://x"}`},
		{"create", "backend", "--type", "codex", "--name", "x", "--config", `not json`},
		{"create", "backend", "--type", "nope", "--name", "x"},
	} {
		r := runWith(args, envFor(srv, tok), term{})
		wantCode(t, r, 2)
	}
	if f.writeCount() != 0 {
		t.Fatal("no write must be sent")
	}
}

// ─── secrets ───────────────────────────────────────────────────────────

func TestSecret_GivenBareAPIKeyOnTTYThenReadWithoutEchoAndNeverEchoed(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "provider", "openrouter", "--api-key"}, handshakeEnv(t, srv), term{tty: true, secrets: []string{"sk-secret-value"}})
	wantCode(t, r, 0)
	if len(r.prompts) != 1 || !strings.Contains(r.prompts[0], "API key") {
		t.Fatalf("prompts = %v, want one API key prompt", r.prompts)
	}
	w := f.onlyWrite(t)
	if w.GetResource().GetProvider().GetApiKey() != "sk-secret-value" || !slices.Equal(w.GetFields(), []string{"apiKey"}) {
		t.Fatalf("write = %v", w)
	}
	if strings.Contains(w.GetCommand(), "sk-secret") || strings.Contains(r.stdout+r.stderr, "sk-secret") {
		t.Fatalf("secret leaked: command=%q stdout=%q stderr=%q", w.GetCommand(), r.stdout, r.stderr)
	}
}

func TestSecret_GivenBareAPIKeyWithoutTTYThenNeedsTTYExit3AndNoWrite(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "provider", "openrouter", "--api-key"}, envFor(srv, tok), term{tty: false})
	wantCode(t, r, 3)
	if !strings.HasPrefix(r.stderr, "NEEDS TTY: --api-key") || !strings.Contains(r.stderr, "own terminal") {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if f.writeCount() != 0 || len(r.prompts) != 0 {
		t.Fatal("no prompt and no write expected")
	}
}

func TestSecret_GivenInlineAPIKeyWithoutTTYThenWrittenAndRedactedInCommand(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "provider", "openrouter", "--api-key=sk-inline"}, envFor(srv, tok), term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if w.GetResource().GetProvider().GetApiKey() != "sk-inline" {
		t.Fatalf("write = %v", w)
	}
	if strings.Contains(w.GetCommand(), "sk-inline") || !strings.Contains(w.GetCommand(), "--api-key=…") {
		t.Fatalf("command = %q, want redacted", w.GetCommand())
	}
}

func TestSecret_GivenSpaceSeparatedAPIKeyThenUsageError(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "provider", "openrouter", "--api-key", "sk-x"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
	if f.writeCount() != 0 {
		t.Fatal("no write must be sent")
	}
}

func TestSecret_GivenBareTokenOnOpenClawWithoutTTYThenExit3(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "backend", "claw", "--token"}, envFor(srv, tok), term{})
	wantCode(t, r, 3)
	if !strings.HasPrefix(r.stderr, "NEEDS TTY: --token") {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

func TestSecret_GivenTokenOnNonOpenClawBackendThenUsageError(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "backend", "codex-remote", "--token=abc"}, envFor(srv, tok), term{})
	wantCode(t, r, 2)
}

// ─── caller classification ─────────────────────────────────────────────

func TestCaller_GivenSessionTokenInEnvThenSessionCallerAndWaitingLine(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "agent", "reviewer", "--description", "x"}, envFor(srv, tok), term{tty: true})
	wantCode(t, r, 0)
	if got := f.onlyWrite(t).GetCaller(); got != agentrewire.CtlCaller_CTL_CALLER_SESSION {
		t.Fatalf("caller = %v", got)
	}
	if !strings.Contains(r.stderr, "waiting for approval in session #42 …") {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

func TestCaller_GivenHandshakeTokenWithoutTTYThenExternalCaller(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "agent", "reviewer", "--description", "x"}, handshakeEnv(t, srv), term{tty: false})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if w.GetCaller() != agentrewire.CtlCaller_CTL_CALLER_EXTERNAL {
		t.Fatalf("caller = %v", w.GetCaller())
	}
	if w.GetCommand() != "agrctl update agent reviewer --description x" {
		t.Fatalf("command = %q", w.GetCommand())
	}
	if !strings.Contains(r.stderr, "waiting for approval in the Agentre desktop …") {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

func TestApproval_GivenRejectedOrTimedOutThenExecutorMessageExit1(t *testing.T) {
	for _, msg := range []string{"rejected in session #42", "approval timed out"} {
		f, srv := newFakeExecutor(t, tok)
		f.writeErr = msg
		r := runWith([]string{"update", "agent", "reviewer", "--description", "x"}, envFor(srv, tok), term{})
		wantCode(t, r, 1)
		if r.stderr != "waiting for approval in session #42 …\nError: "+msg+"\n" {
			t.Fatalf("stderr = %q", r.stderr)
		}
	}
}

func TestApproval_GivenExternalRejectedThenExit1(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	f.writeErr = "rejected in the Agentre desktop"
	r := runWith([]string{"delete", "agent", "reviewer"}, handshakeEnv(t, srv), term{tty: false})
	wantCode(t, r, 1)
	if r.stderr != "waiting for approval in the Agentre desktop …\nError: rejected in the Agentre desktop\n" {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

// TestApproval_GivenExecutorFailsBeforeApprovalThenNoWaitingLine：waiting 行只在执行者真的
// 挂起等审批时才出现，请求本身就不成立时不说「在等审批」。
func TestApproval_GivenExecutorFailsBeforeApprovalThenNoWaitingLine(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	f.failBeforeApproval = "agent id 12 not found"
	r := runWith([]string{"update", "agent", "reviewer", "--description", "x"}, envFor(srv, tok), term{})
	wantCode(t, r, 1)
	if r.stderr != "Error: agent id 12 not found\n" {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

func TestCaller_GivenHandshakeTokenOnTTYThenHumanCallerWithoutWaiting(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"update", "agent", "reviewer", "--description", "x"}, handshakeEnv(t, srv), term{tty: true})
	wantCode(t, r, 0)
	if got := f.onlyWrite(t).GetCaller(); got != agentrewire.CtlCaller_CTL_CALLER_HUMAN {
		t.Fatalf("caller = %v", got)
	}
	if strings.Contains(r.stderr, "waiting") {
		t.Fatalf("stderr = %q, a human caller does not wait for approval", r.stderr)
	}
}

func TestCaller_GivenHumanDeleteWhenNotConfirmedThenNoWriteExit1(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"delete", "agent", "reviewer"}, handshakeEnv(t, srv), term{tty: true, stdin: "n\n"})
	wantCode(t, r, 1)
	if f.writeCount() != 0 {
		t.Fatal("declined delete must not be sent")
	}
	if !strings.Contains(r.stderr, "[y/N]") {
		t.Fatalf("stderr = %q, want a y/N question", r.stderr)
	}
}

func TestCaller_GivenHumanDeleteWhenConfirmedThenDeleted(t *testing.T) {
	f, srv := newFakeExecutor(t, tok)
	r := runWith([]string{"delete", "agent", "reviewer"}, handshakeEnv(t, srv), term{tty: true, stdin: "y\n"})
	wantCode(t, r, 0)
	if f.onlyWrite(t).GetId() != 12 {
		t.Fatal("confirmed delete must be sent")
	}
}

// ─── connection ────────────────────────────────────────────────────────

func TestConnection_GivenUnreachableExecutorThenExit1(t *testing.T) {
	_, srv := newFakeExecutor(t, tok)
	env := envFor(srv, tok)
	srv.Close()
	r := runWith([]string{"list", "agents"}, env, term{})
	wantCode(t, r, 1)
	if !strings.HasPrefix(r.stderr, "Error:") {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

// ─── help ──────────────────────────────────────────────────────────────

func TestHelp_GivenResourceThenFlagsAndSecretsWithoutConnecting(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	r := runWith([]string{"help", "provider"}, noEnv, term{})
	wantCode(t, r, 0)
	for _, want := range []string{"--name", "--base-url", "--api-key", "shell history", "Example"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("stdout = %q, want %q", r.stdout, want)
		}
	}
}

func TestHelp_GivenBackendTypeThenItsConfigFieldsOnly(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	r := runWith([]string{"help", "backend", "codex"}, noEnv, term{})
	wantCode(t, r, 0)
	if !strings.Contains(r.stdout, "sandbox") || !strings.Contains(r.stdout, "approval") {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if strings.Contains(r.stdout, "hermesUrl") {
		t.Fatalf("stdout = %q, must not list other types' config", r.stdout)
	}
}

func TestHelp_GivenUnknownResourceOrTypeThenUsageError(t *testing.T) {
	for _, args := range [][]string{{"help", "widget"}, {"help", "backend", "nope"}} {
		r := runWith(args, noEnv, term{})
		wantCode(t, r, 2)
	}
}
