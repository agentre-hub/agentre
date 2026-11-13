package ctl_svc

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/ctl_svc/mock_ctl_svc"
	"github.com/agentre-hub/agentre/internal/service/memory_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 会话里 agent 的记忆命令（spec「Agent 读写记忆」、决策 6/7/10）：会话级 token 绑定
// (agent 7, session 42)；agent 7 在部门 2，部门 2 的上级是部门 1；会话在项目 9。

var (
	memAI      = memory_entity.AIActor(7, 42)
	memGlobal  = memory_entity.GlobalOwner()
	memRoot    = memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 1}
	memDept    = memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 2}
	memAgent   = memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: 7}
	memProject = memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 9}
	memOwners  = []memory_entity.Owner{memGlobal, memRoot, memDept, memAgent, memProject}
)

type memoryRig struct {
	mem      *mock_ctl_svc.MockMemoryService
	signer   *agenttool.TokenSigner
	sessions *fakeSessionApprovals
	desktop  *fakeExternalApprovals
	h        *ctlHandler
}

func newMemoryRig(t *testing.T) *memoryRig {
	t.Helper()
	r := &memoryRig{
		mem:      mock_ctl_svc.NewMockMemoryService(gomock.NewController(t)),
		signer:   agenttool.NewTokenSigner(),
		sessions: &fakeSessionApprovals{},
		desktop:  &fakeExternalApprovals{},
	}
	res := fakeResources()
	res.Memory = r.mem
	r.h = &ctlHandler{
		token: testToken, sessions: r.signer, resources: res,
		approvals: r.sessions, external: r.desktop, approvalTimeout: time.Minute,
	}
	return r
}

func (r *memoryRig) sessionToken() string { return r.signer.MintToken(7, 42) }

func memItem(id int64, owner memory_entity.Owner, summary, detail string) *memory_svc.MemoryItem {
	return &memory_svc.MemoryItem{
		ID: id, Owner: owner, Summary: summary, Detail: detail, State: memory_entity.StateNormal,
		LastChange: memory_svc.ChangeDTO{Action: memory_entity.ActionCreated, Actor: memory_entity.UserActor(), At: 1_800_000_000_000},
	}
}

// visibleFixture：agent 层一条带上、一条超出预算没带上；项目层一条用户写的。
func visibleFixture() *memory_svc.VisibleMemory {
	return &memory_svc.VisibleMemory{Layers: []*memory_svc.VisibleLayer{
		{Owner: memGlobal},
		{Owner: memRoot},
		{Owner: memDept},
		{Owner: memAgent, Carried: []*memory_svc.MemoryItem{memItem(58, memAgent, "提交前先跑 lint", "流水线不跑 lint")},
			OverBudget: []*memory_svc.MemoryItem{memItem(61, memAgent, "生产库版本低", "")}},
		{Owner: memProject, Carried: []*memory_svc.MemoryItem{memItem(77, memProject, "联调机在内网", "")}},
	}}
}

func memoryWrite(t *testing.T, op agentrewire.CtlOp, id int64, m *agentrewire.CtlMemory, fields ...string) string {
	t.Helper()
	return writeBody(t, &agentrewire.CtlWriteRequest{
		Op: op, Kind: agentrewire.CtlKind_CTL_KIND_MEMORY, Id: id, Caller: agentrewire.CtlCaller_CTL_CALLER_SESSION,
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Memory{Memory: m}}, Fields: fields,
		Command: "agrctl create memory --summary x",
	})
}

func usage(owner memory_entity.Owner, used int, over ...*memory_svc.MemoryItem) *memory_svc.OwnerList {
	return &memory_svc.OwnerList{Owner: owner, Budget: memory_entity.LayerBudgetChars, UsedChars: used, OverBudget: over}
}

func listBody(t *testing.T, kind agentrewire.CtlKind) string {
	t.Helper()
	raw, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_List{List: &agentrewire.CtlListRequest{Kind: kind}}})
	require.NoError(t, err)
	return string(raw)
}

func getBody(t *testing.T, kind agentrewire.CtlKind, id int64) string {
	t.Helper()
	raw, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Get{Get: &agentrewire.CtlGetRequest{Kind: kind, Id: id}}})
	require.NoError(t, err)
	return string(raw)
}

// assertNoApproval：记忆写入不出审批卡、不进桌面端待审批队列。
func (r *memoryRig) assertNoApproval(t *testing.T, pending []string) {
	t.Helper()
	assert.Empty(t, pending, "免审批的写入不发 102 等待提示")
	_, begun, _ := r.sessions.snapshot()
	assert.Empty(t, begun, "不在会话里出审批卡")
	assert.Empty(t, r.desktop.queued, "不进桌面端待审批队列")
}

// ---- 写入：免审批、直接生效 ----

func TestMemory_GivenSessionCreateWithoutLayerThenWrittenToTheAgentWithoutApproval(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().SessionOwners(gomock.Any(), memAI).Return(memOwners, nil)
	r.mem.EXPECT().Create(gomock.Any(), &memory_svc.CreateRequest{Owner: memAgent, Summary: "用 pnpm", Detail: "不用 npm", Actor: memAI}).
		Return(&memory_svc.CreateResult{Item: memItem(83, memAgent, "用 pnpm", "不用 npm")}, nil)
	r.mem.EXPECT().ListByOwner(gomock.Any(), memAgent).Return(usage(memAgent, 120), nil)

	status, body, pending := realPost(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_CREATE, 0,
		&agentrewire.CtlMemory{Summary: "用 pnpm", Detail: "不用 npm"}, "summary", "detail"))
	resp := decodeWrite(t, status, body)

	r.assertNoApproval(t, pending)
	assert.Equal(t, int64(83), resp.GetId())
	assert.False(t, resp.GetExisted())
	assert.Zero(t, resp.GetOverBudgetChars(), "没超预算")
	m := resp.GetResource().GetMemory()
	assert.Equal(t, "agent", m.GetLayer())
	assert.Equal(t, int64(7), m.GetOwnerId())
	assert.Equal(t, "用 pnpm", m.GetSummary())
}

func TestMemory_GivenDepartmentLayerThenDirectDepartmentByDefaultOrANamedUpperOne(t *testing.T) {
	cases := []struct {
		name    string
		ownerID int64
		want    memory_entity.Owner
	}{
		{"没指明部门：agent 直属的部门", 0, memDept},
		{"指明上级部门：那一个", 1, memRoot},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newMemoryRig(t)
			r.mem.EXPECT().SessionOwners(gomock.Any(), memAI).Return(memOwners, nil)
			r.mem.EXPECT().Create(gomock.Any(), &memory_svc.CreateRequest{Owner: c.want, Summary: "迁移只追加", Actor: memAI}).
				Return(&memory_svc.CreateResult{Item: memItem(90, c.want, "迁移只追加", "")}, nil)
			r.mem.EXPECT().ListByOwner(gomock.Any(), c.want).Return(usage(c.want, 10), nil)
			rec := postResources(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_CREATE, 0,
				&agentrewire.CtlMemory{Layer: "department", OwnerId: c.ownerID, Summary: "迁移只追加"}, "summary", "layer", "ownerId"))
			assert.Equal(t, c.want.ID, decodeWrite(t, rec.Code, rec.Body.Bytes()).GetResource().GetMemory().GetOwnerId())
		})
	}
}

func TestMemory_GivenProjectOrGlobalLayerThenThatLayer(t *testing.T) {
	for layer, want := range map[string]memory_entity.Owner{"project": memProject, "global": memGlobal} {
		t.Run(layer, func(t *testing.T) {
			r := newMemoryRig(t)
			r.mem.EXPECT().SessionOwners(gomock.Any(), memAI).Return(memOwners, nil)
			r.mem.EXPECT().Create(gomock.Any(), &memory_svc.CreateRequest{Owner: want, Summary: "x", Actor: memAI}).
				Return(&memory_svc.CreateResult{Item: memItem(91, want, "x", "")}, nil)
			r.mem.EXPECT().ListByOwner(gomock.Any(), want).Return(usage(want, 1), nil)
			rec := postResources(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_CREATE, 0,
				&agentrewire.CtlMemory{Layer: layer, Summary: "x"}, "summary", "layer"))
			assert.Equal(t, layer, decodeWrite(t, rec.Code, rec.Body.Bytes()).GetResource().GetMemory().GetLayer())
		})
	}
}

func TestMemory_GivenALayerTheSessionDoesNotHaveThenRejectedWithAReason(t *testing.T) {
	cases := []struct {
		layer  string
		owners []memory_entity.Owner
		want   string
	}{
		{"department", []memory_entity.Owner{memGlobal, memAgent, memProject}, "not in any department"},
		{"project", []memory_entity.Owner{memGlobal, memDept, memAgent}, "not in a project"},
		{"team", memOwners, `unknown memory layer "team"`},
	}
	for _, c := range cases {
		t.Run(c.layer, func(t *testing.T) {
			r := newMemoryRig(t)
			r.mem.EXPECT().SessionOwners(gomock.Any(), memAI).Return(c.owners, nil)
			rec := postResources(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_CREATE, 0,
				&agentrewire.CtlMemory{Layer: c.layer, Summary: "x"}, "summary", "layer"))
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var body struct{ Error string }
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Contains(t, body.Error, c.want)
		})
	}
}

func TestMemory_GivenServiceRejectsTheLayerThenItsMessagePassesThrough(t *testing.T) {
	r := newMemoryRig(t)
	other := memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 6}
	r.mem.EXPECT().SessionOwners(gomock.Any(), memAI).Return(memOwners, nil)
	r.mem.EXPECT().Create(gomock.Any(), &memory_svc.CreateRequest{Owner: other, Summary: "x", Actor: memAI}).
		Return(nil, errors.New("an agent can only write memories in its own layers"))
	rec := postResources(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_CREATE, 0,
		&agentrewire.CtlMemory{Layer: "department", OwnerId: 6, Summary: "x"}, "summary", "layer", "ownerId"))
	assert.NotEqual(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "only write memories in its own layers")
}

func TestMemory_GivenSameSummaryAlreadyInTheLayerThenExistingOneReturned(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().SessionOwners(gomock.Any(), memAI).Return(memOwners, nil)
	r.mem.EXPECT().Create(gomock.Any(), gomock.Any()).
		Return(&memory_svc.CreateResult{Item: memItem(58, memAgent, "提交前先跑 lint", ""), Existed: true}, nil)
	r.mem.EXPECT().ListByOwner(gomock.Any(), memAgent).Return(usage(memAgent, 50), nil)
	rec := postResources(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_CREATE, 0,
		&agentrewire.CtlMemory{Summary: "提交前先跑 lint"}, "summary"))
	resp := decodeWrite(t, rec.Code, rec.Body.Bytes())
	assert.True(t, resp.GetExisted())
	assert.Equal(t, int64(58), resp.GetId())
}

func TestMemory_GivenWriteLeavesTheLayerOverBudgetThenResultSaysByHowMuch(t *testing.T) {
	r := newMemoryRig(t)
	created := memItem(84, memProject, "发布包走内部制品库", "")
	r.mem.EXPECT().SessionOwners(gomock.Any(), memAI).Return(memOwners, nil)
	r.mem.EXPECT().Create(gomock.Any(), gomock.Any()).Return(&memory_svc.CreateResult{Item: created}, nil)
	r.mem.EXPECT().ListByOwner(gomock.Any(), memProject).Return(usage(memProject, 2120, created), nil)
	rec := postResources(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_CREATE, 0,
		&agentrewire.CtlMemory{Layer: "project", Summary: "发布包走内部制品库"}, "summary", "layer"))
	resp := decodeWrite(t, rec.Code, rec.Body.Bytes())
	assert.Equal(t, int32(120), resp.GetOverBudgetChars())
	assert.True(t, resp.GetResource().GetMemory().GetOverBudget(), "新写的这条落在预算之外，本会话之后也不带")
}

func TestMemory_GivenUpdateOfOneFieldThenTheOtherKeptAndWrittenAsTheAgent(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().SessionVisible(gomock.Any(), memAI).Return(visibleFixture(), nil)
	r.mem.EXPECT().Update(gomock.Any(), &memory_svc.UpdateRequest{ID: 77, Summary: "联调机在内网", Detail: "192.168.8.141", Actor: memAI}).
		Return(memItem(77, memProject, "联调机在内网", "192.168.8.141"), nil)
	r.mem.EXPECT().ListByOwner(gomock.Any(), memProject).Return(usage(memProject, 6), nil)
	status, body, pending := realPost(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_UPDATE, 77,
		&agentrewire.CtlMemory{Detail: "192.168.8.141"}, "detail"))
	resp := decodeWrite(t, status, body)
	r.assertNoApproval(t, pending)
	assert.Equal(t, int64(77), resp.GetId())
	assert.Equal(t, "192.168.8.141", resp.GetResource().GetMemory().GetDetail())
}

func TestMemory_GivenUpdateOfAnEntryThisSessionCannotSeeThenNotFound(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().SessionVisible(gomock.Any(), memAI).Return(visibleFixture(), nil)
	rec := postResources(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_UPDATE, 500,
		&agentrewire.CtlMemory{Summary: "x"}, "summary"))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"error":"memory id 500 not found"}`, rec.Body.String())
}

func TestMemory_GivenUpdateOfTheLayerOrNothingThenBadRequest(t *testing.T) {
	for name, fields := range map[string][]string{"move": {"layer"}, "nothing": nil} {
		t.Run(name, func(t *testing.T) {
			r := newMemoryRig(t)
			r.mem.EXPECT().SessionVisible(gomock.Any(), memAI).Return(visibleFixture(), nil).AnyTimes()
			rec := postResources(t, r.h, r.sessionToken(), memoryWrite(t, agentrewire.CtlOp_CTL_OP_UPDATE, 58,
				&agentrewire.CtlMemory{Layer: "global"}, fields...))
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

func TestMemory_GivenDeleteThenArchivedNotErased(t *testing.T) {
	r := newMemoryRig(t)
	archived := memItem(58, memAgent, "提交前先跑 lint", "")
	archived.State = memory_entity.StateArchived
	r.mem.EXPECT().SessionVisible(gomock.Any(), memAI).Return(visibleFixture(), nil)
	r.mem.EXPECT().Archive(gomock.Any(), int64(58), memAI).Return(archived, nil)
	r.mem.EXPECT().ListByOwner(gomock.Any(), memAgent).Return(usage(memAgent, 6), nil)
	status, body, pending := realPost(t, r.h, r.sessionToken(), writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_DELETE, Kind: agentrewire.CtlKind_CTL_KIND_MEMORY, Id: 58, Caller: agentrewire.CtlCaller_CTL_CALLER_SESSION,
	}))
	resp := decodeWrite(t, status, body)
	r.assertNoApproval(t, pending)
	assert.Equal(t, int64(58), resp.GetId())
	assert.Equal(t, agentrewire.CtlOp_CTL_OP_DELETE, resp.GetChanges()[0].GetOp())
}

// ---- 读：只见本会话可见的正常条目 ----

func TestMemory_GivenSessionListThenVisibleEntriesIncludingOverBudget(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().SessionVisible(gomock.Any(), memAI).Return(visibleFixture(), nil)
	resp := decodeCtlResponse(t, postResources(t, r.h, r.sessionToken(), listBody(t, agentrewire.CtlKind_CTL_KIND_MEMORY)))
	ids := make([]int64, 0, len(resp.GetList().GetItems()))
	over := map[int64]bool{}
	for _, it := range resp.GetList().GetItems() {
		m := it.GetMemory()
		ids = append(ids, m.GetId())
		over[m.GetId()] = m.GetOverBudget()
	}
	assert.Equal(t, []int64{58, 61, 77}, ids, "按层排列，层内先带上的、后超出预算的")
	assert.Equal(t, map[int64]bool{58: false, 61: true, 77: false}, over)
}

func TestMemory_GivenGetThenFullEntryOrNotFoundOutsideTheSession(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().SessionVisible(gomock.Any(), memAI).Return(visibleFixture(), nil).Times(2)
	got := decodeCtlResponse(t, postResources(t, r.h, r.sessionToken(), getBody(t, agentrewire.CtlKind_CTL_KIND_MEMORY, 58)))
	assert.Equal(t, "流水线不跑 lint", got.GetGet().GetResource().GetMemory().GetDetail())

	rec := postResources(t, r.h, r.sessionToken(), getBody(t, agentrewire.CtlKind_CTL_KIND_MEMORY, 500))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ---- 非会话调用方：沿用现有规则（spec「Agent 读写记忆」前提） ----
//
// 握手 token：终端里的人直接执行，外部调用方在桌面端审批；两者都以用户身份写，看得到
// 所有层。层没有会话可以推断，必须写明。

func callerWrite(t *testing.T, caller agentrewire.CtlCaller, op agentrewire.CtlOp, id int64, m *agentrewire.CtlMemory, fields ...string) string {
	t.Helper()
	return writeBody(t, &agentrewire.CtlWriteRequest{
		Op: op, Kind: agentrewire.CtlKind_CTL_KIND_MEMORY, Id: id, Caller: caller,
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Memory{Memory: m}}, Fields: fields,
		Command: "agrctl create memory --summary x --layer global",
	})
}

// answerWhenQueued 扮演桌面弹窗：等第一条请求入队后作答。
func (r *memoryRig) answerWhenQueued(allow bool) {
	for {
		r.desktop.mu.Lock()
		if len(r.desktop.queued) > 0 {
			id := r.desktop.queued[0].RequestID
			r.desktop.mu.Unlock()
			_ = r.desktop.Answer(id, allow)
			return
		}
		r.desktop.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMemory_GivenHumanOutsideASessionThenListsEveryLayer(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().AllLayers(gomock.Any()).Return(visibleFixture(), nil)
	resp := decodeCtlResponse(t, postResources(t, r.h, testToken, listBody(t, agentrewire.CtlKind_CTL_KIND_MEMORY)))
	ids := make([]int64, 0, len(resp.GetList().GetItems()))
	for _, it := range resp.GetList().GetItems() {
		ids = append(ids, it.GetMemory().GetId())
	}
	assert.Equal(t, []int64{58, 61, 77}, ids)
}

func TestMemory_GivenHumanCreateOutsideASessionThenWrittenDirectlyAsTheUser(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().Create(gomock.Any(), &memory_svc.CreateRequest{Owner: memGlobal, Summary: "用 pnpm", Actor: memory_entity.UserActor()}).
		Return(&memory_svc.CreateResult{Item: memItem(83, memGlobal, "用 pnpm", "")}, nil)
	r.mem.EXPECT().ListByOwner(gomock.Any(), memGlobal).Return(usage(memGlobal, 6), nil)
	status, body, pending := realPost(t, r.h, testToken, callerWrite(t, agentrewire.CtlCaller_CTL_CALLER_HUMAN,
		agentrewire.CtlOp_CTL_OP_CREATE, 0, &agentrewire.CtlMemory{Layer: "global", Summary: "用 pnpm"}, "summary", "layer"))
	resp := decodeWrite(t, status, body)
	r.assertNoApproval(t, pending)
	assert.Equal(t, int64(83), resp.GetId())
	assert.Equal(t, "global", resp.GetResource().GetMemory().GetLayer())
}

func TestMemory_GivenHumanCreateOfANamedAgentOrProjectThenThatOwner(t *testing.T) {
	for layer, want := range map[string]memory_entity.Owner{"agent": {Kind: memory_entity.OwnerAgent, ID: 12}, "project": {Kind: memory_entity.OwnerProject, ID: 3}} {
		t.Run(layer, func(t *testing.T) {
			r := newMemoryRig(t)
			r.mem.EXPECT().Create(gomock.Any(), &memory_svc.CreateRequest{Owner: want, Summary: "x", Actor: memory_entity.UserActor()}).
				Return(&memory_svc.CreateResult{Item: memItem(91, want, "x", "")}, nil)
			r.mem.EXPECT().ListByOwner(gomock.Any(), want).Return(usage(want, 1), nil)
			rec := postResources(t, r.h, testToken, callerWrite(t, agentrewire.CtlCaller_CTL_CALLER_HUMAN, agentrewire.CtlOp_CTL_OP_CREATE, 0,
				&agentrewire.CtlMemory{Layer: layer, OwnerId: want.ID, Summary: "x"}, "summary", "layer", "ownerId"))
			assert.Equal(t, want.ID, decodeWrite(t, rec.Code, rec.Body.Bytes()).GetResource().GetMemory().GetOwnerId())
		})
	}
}

func TestMemory_GivenHumanCreateWithoutANamedLayerOutsideASessionThenBadRequest(t *testing.T) {
	cases := map[string]*agentrewire.CtlMemory{
		"no layer":                {Summary: "x"},
		"agent layer, no agent":   {Layer: "agent", Summary: "x"},
		"department, none named":  {Layer: "department", Summary: "x"},
		"project layer, no named": {Layer: "project", Summary: "x"},
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newMemoryRig(t) // 不该碰记忆服务
			fields := []string{"summary"}
			if doc.GetLayer() != "" {
				fields = append(fields, "layer")
			}
			rec := postResources(t, r.h, testToken, callerWrite(t, agentrewire.CtlCaller_CTL_CALLER_HUMAN, agentrewire.CtlOp_CTL_OP_CREATE, 0, doc, fields...))
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "outside an Agentre session")
		})
	}
}

func TestMemory_GivenHumanDeleteOutsideASessionThenArchivedAsTheUser(t *testing.T) {
	r := newMemoryRig(t)
	archived := memItem(58, memAgent, "提交前先跑 lint", "")
	archived.State = memory_entity.StateArchived
	r.mem.EXPECT().AllLayers(gomock.Any()).Return(visibleFixture(), nil)
	r.mem.EXPECT().Archive(gomock.Any(), int64(58), memory_entity.UserActor()).Return(archived, nil)
	r.mem.EXPECT().ListByOwner(gomock.Any(), memAgent).Return(usage(memAgent, 6), nil)
	status, body, pending := realPost(t, r.h, testToken, writeBody(t, &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_DELETE, Kind: agentrewire.CtlKind_CTL_KIND_MEMORY, Id: 58, Caller: agentrewire.CtlCaller_CTL_CALLER_HUMAN,
	}))
	resp := decodeWrite(t, status, body)
	r.assertNoApproval(t, pending)
	assert.Equal(t, int64(58), resp.GetId())
}

func TestMemory_GivenExternalCallerWhenDesktopApprovesThenWrittenAsTheUser(t *testing.T) {
	r := newMemoryRig(t)
	r.mem.EXPECT().AllLayers(gomock.Any()).Return(visibleFixture(), nil).AnyTimes()
	r.mem.EXPECT().Update(gomock.Any(), &memory_svc.UpdateRequest{ID: 77, Summary: "联调机在内网", Detail: "192.168.8.141", Actor: memory_entity.UserActor()}).
		Return(memItem(77, memProject, "联调机在内网", "192.168.8.141"), nil)
	r.mem.EXPECT().ListByOwner(gomock.Any(), memProject).Return(usage(memProject, 6), nil)
	go r.answerWhenQueued(true)
	status, body, pending := realPost(t, r.h, testToken, callerWrite(t, agentrewire.CtlCaller_CTL_CALLER_EXTERNAL,
		agentrewire.CtlOp_CTL_OP_UPDATE, 77, &agentrewire.CtlMemory{Detail: "192.168.8.141"}, "detail"))
	resp := decodeWrite(t, status, body)
	assert.Equal(t, int64(77), resp.GetId())
	assert.Equal(t, []string{"desktop"}, pending)
	_, begun, _ := r.sessions.snapshot()
	assert.Empty(t, begun, "外部调用方不在会话里出卡")
	require.Len(t, r.desktop.queued, 1)
	in := r.desktop.queued[0].Input
	require.Len(t, in.Changes, 1)
	ch := in.Changes[0]
	assert.Equal(t, "update", ch.Op)
	assert.Equal(t, "memory", ch.Kind)
	assert.Equal(t, int64(77), ch.ID)
	assert.Equal(t, "联调机在内网", ch.Name)
	require.Len(t, ch.Fields, 1)
	assert.Equal(t, "detail", ch.Fields[0].Field)
	assert.Nil(t, ch.Fields[0].Before)
	assert.Equal(t, "192.168.8.141", *ch.Fields[0].After)
}

func TestMemory_GivenExternalCallerWhenDesktopRejectsThenNothingWritten(t *testing.T) {
	r := newMemoryRig(t) // 没有 Create 期望：写了就判红
	go r.answerWhenQueued(false)
	status, body, pending := realPost(t, r.h, testToken, callerWrite(t, agentrewire.CtlCaller_CTL_CALLER_EXTERNAL,
		agentrewire.CtlOp_CTL_OP_CREATE, 0, &agentrewire.CtlMemory{Layer: "global", Summary: "x"}, "summary", "layer"))
	assert.Equal(t, http.StatusForbidden, status, string(body))
	assert.Equal(t, []string{"desktop"}, pending)
	require.Len(t, r.desktop.queued, 1)
	assert.Equal(t, "create", r.desktop.queued[0].Input.Changes[0].Op)
	assert.Equal(t, "x", r.desktop.queued[0].Input.Changes[0].Name)
}

func TestMemory_GivenExternalCallerWithAnInvalidRequestThenRefusedBeforeAnyApproval(t *testing.T) {
	for name, body := range map[string]func(t *testing.T) string{
		"create without a layer": func(t *testing.T) string {
			return callerWrite(t, agentrewire.CtlCaller_CTL_CALLER_EXTERNAL, agentrewire.CtlOp_CTL_OP_CREATE, 0,
				&agentrewire.CtlMemory{Summary: "x"}, "summary")
		},
		"update with nothing": func(t *testing.T) string {
			return callerWrite(t, agentrewire.CtlCaller_CTL_CALLER_EXTERNAL, agentrewire.CtlOp_CTL_OP_UPDATE, 58, &agentrewire.CtlMemory{})
		},
		"update of a memory that does not exist": func(t *testing.T) string {
			return callerWrite(t, agentrewire.CtlCaller_CTL_CALLER_EXTERNAL, agentrewire.CtlOp_CTL_OP_UPDATE, 500,
				&agentrewire.CtlMemory{Summary: "x"}, "summary")
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newMemoryRig(t)
			r.mem.EXPECT().AllLayers(gomock.Any()).Return(visibleFixture(), nil).AnyTimes()
			status, _, pending := realPost(t, r.h, testToken, body(t))
			assert.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, status)
			assert.Empty(t, pending, "不该挂上审批")
			assert.Empty(t, r.desktop.queued)
		})
	}
}
