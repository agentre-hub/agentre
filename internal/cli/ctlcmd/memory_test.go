package ctlcmd

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 记忆命令（spec 2026-11-11「Agent 读写记忆」）在会话里由 agent 发出：列出 / 取一条 /
// 新增 / 修改 / 归档。下面的执行者扮演桌面端：会话（agent reviewer，部门 研发部，项目
// agentre）看得到 agent 层两条（一条超出预算没带上）和项目层一条。

func memDoc(m *agentrewire.CtlMemory) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Memory{Memory: m}}
}

// midday20261102 是 2026-11-02 12:00 UTC（毫秒）：任何时区下都还是 11-02 或相邻日。
const midday20261102 = 1_793_620_800_000

func withMemories(f *fakeExecutor) {
	f.items[agentrewire.CtlKind_CTL_KIND_MEMORY] = []*agentrewire.CtlResource{
		memDoc(&agentrewire.CtlMemory{Id: 58, Layer: "agent", OwnerId: 12, Summary: "提交前先跑 lint", Detail: "流水线不跑 Lint", ChangedAt: midday20261102}),
		memDoc(&agentrewire.CtlMemory{Id: 61, Layer: "agent", OwnerId: 12, Summary: "生产库版本比开发机低", OverBudget: true, ChangedAt: midday20261102}),
		memDoc(&agentrewire.CtlMemory{Id: 77, Layer: "project", OwnerId: 1, Summary: "联调机在内网", ChangedAt: midday20261102}),
	}
}

// memoryWritten 让执行者像桌面端那样带回写入后的那一条。
func memoryWritten(id int64, layer string, ownerID int64, existed bool, over int32) func(*agentrewire.CtlWriteRequest) *agentrewire.CtlWriteResponse {
	return func(*agentrewire.CtlWriteRequest) *agentrewire.CtlWriteResponse {
		return &agentrewire.CtlWriteResponse{Id: id, Existed: existed, OverBudgetChars: over,
			Resource: memDoc(&agentrewire.CtlMemory{Id: id, Layer: layer, OwnerId: ownerID})}
	}
}

func memorySession(t *testing.T) (*fakeExecutor, func(string) (string, bool)) {
	t.Helper()
	f, srv := newFakeExecutor(t, tok)
	withMemories(f)
	return f, envFor(srv, tok)
}

// ─── 写入：结果行是稳定格式（对话里的记忆卡片据此识别） ─────────────────────

func TestMemory_GivenCreateWithoutLayerThenSentAsAgentLayerAndCreatedLine(t *testing.T) {
	f, env := memorySession(t)
	f.writeResp = memoryWritten(83, "agent", 12, false, 0)
	r := runWith([]string{"create", "memory", "--summary", "用 pnpm", "--detail", "不用 npm"}, env, term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if w.GetKind() != agentrewire.CtlKind_CTL_KIND_MEMORY || w.GetOp() != agentrewire.CtlOp_CTL_OP_CREATE {
		t.Fatalf("request = %v", w)
	}
	m := w.GetResource().GetMemory()
	if m.GetSummary() != "用 pnpm" || m.GetDetail() != "不用 npm" || m.GetLayer() != "" {
		t.Fatalf("memory doc = %v", m)
	}
	if !slices.Equal(w.GetFields(), []string{"summary", "detail"}) {
		t.Fatalf("fields = %v", w.GetFields())
	}
	if r.stdout != "memory m83 created (agent reviewer)\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if r.stderr != "" {
		t.Fatalf("a memory write waits for nobody, stderr = %q", r.stderr)
	}
}

func TestMemory_GivenNamedUpperDepartmentThenDepartmentLayerWithThatID(t *testing.T) {
	f, env := memorySession(t)
	f.writeResp = memoryWritten(90, "department", 1, false, 0)
	r := runWith([]string{"create", "memory", "--summary", "迁移只追加", "--department", "总部"}, env, term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if m := w.GetResource().GetMemory(); m.GetLayer() != "department" || m.GetOwnerId() != 1 {
		t.Fatalf("memory doc = %v", m)
	}
	if !slices.Contains(w.GetFields(), "layer") || !slices.Contains(w.GetFields(), "ownerId") {
		t.Fatalf("fields = %v", w.GetFields())
	}
	if r.stdout != "memory m90 created (department 总部)\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

// 会话以外（终端里的人、外部调用方）没有可推断的层：--agent / --project 指明是哪一个，与
// --department 同一写法。
func TestMemory_GivenNamedAgentOrProjectThenThatLayerWithItsID(t *testing.T) {
	for name, c := range map[string]struct {
		args    []string
		layer   string
		ownerID int64
	}{
		"--agent":   {[]string{"--agent", "reviewer"}, "agent", 12},
		"--project": {[]string{"--project", "agentre"}, "project", 1},
	} {
		t.Run(name, func(t *testing.T) {
			f, env := memorySession(t)
			f.writeResp = memoryWritten(92, c.layer, c.ownerID, false, 0)
			r := runWith(append([]string{"create", "memory", "--summary", "x"}, c.args...), env, term{})
			wantCode(t, r, 0)
			w := f.onlyWrite(t)
			if m := w.GetResource().GetMemory(); m.GetLayer() != c.layer || m.GetOwnerId() != c.ownerID {
				t.Fatalf("memory doc = %v", m)
			}
			if !slices.Contains(w.GetFields(), "layer") || !slices.Contains(w.GetFields(), "ownerId") {
				t.Fatalf("fields = %v", w.GetFields())
			}
		})
	}
}

func TestMemory_GivenBadLayerFlagsThenUsageErrorAndNoWrite(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown layer":                   {"create", "memory", "--summary", "x", "--layer", "team"},
		"--department with another layer": {"create", "memory", "--summary", "x", "--layer", "project", "--department", "总部"},
		"--agent with another layer":      {"create", "memory", "--summary", "x", "--layer", "global", "--agent", "reviewer"},
		"--agent and --project together":  {"create", "memory", "--summary", "x", "--agent", "reviewer", "--project", "agentre"},
		"missing summary":                 {"create", "memory", "--layer", "global"},
	} {
		t.Run(name, func(t *testing.T) {
			f, env := memorySession(t)
			r := runWith(args, env, term{})
			wantCode(t, r, exitUsage)
			if f.writeCount() != 0 {
				t.Fatal("a usage error must not reach the executor")
			}
		})
	}
}

func TestMemory_GivenSameSummaryAlreadyThereThenAlreadyExistsLine(t *testing.T) {
	f, env := memorySession(t)
	f.writeResp = memoryWritten(58, "agent", 12, true, 0)
	r := runWith([]string{"create", "memory", "--summary", "提交前先跑 lint"}, env, term{})
	wantCode(t, r, 0)
	if r.stdout != "memory m58 already exists (agent reviewer)\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestMemory_GivenWriteLeavesLayerOverBudgetThenSecondLineSaysByHowMuch(t *testing.T) {
	f, env := memorySession(t)
	f.writeResp = memoryWritten(84, "project", 1, false, 120)
	r := runWith([]string{"create", "memory", "--summary", "发布包走内部制品库", "--layer", "project"}, env, term{})
	wantCode(t, r, 0)
	want := "memory m84 created (project agentre)\n" +
		"over budget: project agentre is 120 characters over its memory budget; merge or shorten the memories there\n"
	if r.stdout != want {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestMemory_GivenUpdateByMRefThenOnlyThatFieldAndUpdatedLine(t *testing.T) {
	f, env := memorySession(t)
	f.writeResp = memoryWritten(58, "agent", 12, false, 0)
	r := runWith([]string{"update", "memory", "m58", "--detail", "lint 要在本地跑"}, env, term{})
	wantCode(t, r, 0)
	w := f.onlyWrite(t)
	if w.GetId() != 58 || !slices.Equal(w.GetFields(), []string{"detail"}) || w.GetResource().GetMemory().GetDetail() != "lint 要在本地跑" {
		t.Fatalf("request = %v", w)
	}
	if r.stdout != "memory m58 updated (agent reviewer)\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestMemory_GivenDeleteThenArchivedLineWithoutConfirmation(t *testing.T) {
	f, env := memorySession(t)
	f.writeResp = memoryWritten(77, "project", 1, false, 0)
	r := runWith([]string{"delete", "memory", "77"}, env, term{tty: true})
	wantCode(t, r, 0)
	if w := f.onlyWrite(t); w.GetOp() != agentrewire.CtlOp_CTL_OP_DELETE || w.GetId() != 77 {
		t.Fatalf("request = %v", w)
	}
	if r.stdout != "memory m77 archived (project agentre)\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestMemory_GivenRefThatIsNotAMemoryIDThenUsageErrorOrNotFound(t *testing.T) {
	_, env := memorySession(t)
	r := runWith([]string{"get", "memory", "lint"}, env, term{})
	wantCode(t, r, exitUsage)
	r = runWith([]string{"get", "memory", "m500"}, env, term{})
	wantCode(t, r, exitFailed)
	if !strings.Contains(r.stderr, `memory "m500" not found`) {
		t.Fatalf("stderr = %q", r.stderr)
	}
}

// ─── 读 ─────────────────────────────────────────────────────────────────

func TestMemory_GivenListThenLayerNamesAndWhichAreNotCarried(t *testing.T) {
	_, env := memorySession(t)
	r := runWith([]string{"list", "memory"}, env, term{})
	wantCode(t, r, 0)
	rows := tableRows(r.stdout)
	if got := strings.Join(rows[0], " "); got != "ID LAYER SUMMARY DETAIL CARRIED UPDATED" {
		t.Fatalf("header = %q", got)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %v", rows)
	}
	if want := []string{"m58", "agent", "reviewer", "提交前先跑", "lint", "yes", "yes"}; !slices.Equal(rows[1][:7], want) {
		t.Fatalf("row 1 = %v", rows[1])
	}
	if rows[2][0] != "m61" || rows[2][len(rows[2])-2] != "no" {
		t.Fatalf("an over-budget entry is listed as not carried: %v", rows[2])
	}
}

func TestMemory_GivenQueryThenMatchesSummaryOrDetailIgnoringCase(t *testing.T) {
	_, env := memorySession(t)
	r := runWith([]string{"list", "memories", "--query", "lint", "-o", "json"}, env, term{})
	wantCode(t, r, 0)
	var got []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.stdout)
	}
	if len(got) != 1 || got[0].ID != "m58" {
		t.Fatalf("got = %v", got)
	}

	r = runWith([]string{"list", "memory", "--query", "流水线不跑 lint"}, env, term{})
	if rows := tableRows(r.stdout); len(rows) != 2 || rows[1][0] != "m58" {
		t.Fatalf("a query found only in the detail (different case) still matches: %q", r.stdout)
	}
}

func TestMemory_GivenLayerFilterThenOnlyThatLayer(t *testing.T) {
	_, env := memorySession(t)
	r := runWith([]string{"list", "memory", "--layer", "project"}, env, term{})
	wantCode(t, r, 0)
	if rows := tableRows(r.stdout); len(rows) != 2 || rows[1][0] != "m77" {
		t.Fatalf("stdout = %q", r.stdout)
	}
	r = runWith([]string{"list", "memory", "--layer", "team"}, env, term{})
	wantCode(t, r, exitUsage)
}

func TestMemory_GivenGetThenFullEntryAsJSON(t *testing.T) {
	_, env := memorySession(t)
	r := runWith([]string{"get", "memory", "m58"}, env, term{})
	wantCode(t, r, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.stdout)
	}
	if got["id"] != "m58" || got["layer"] != "agent reviewer" || got["detail"] != "流水线不跑 Lint" || got["carried"] != true {
		t.Fatalf("got = %v", got)
	}
}

func TestMemory_GivenHelpThenUsageLayersAndWhatToRemember(t *testing.T) {
	r := runWith([]string{"help", "memory"}, noEnv, term{})
	wantCode(t, r, 0)
	for _, want := range []string{"agrctl create memory --summary <text>", "--layer", "--department", "--agent", "--project", "--query",
		"archives", "no approval", "Outside an Agentre session", "Worth remembering", "Do not remember"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("help memory lacks %q:\n%s", want, r.stdout)
		}
	}
}
