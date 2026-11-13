package ctl_svc

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/service/memory_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 记忆（spec 2026-11-11「Agent 读写记忆」）。会话级 token：读只见本会话可见的正常条目（含因
// 超出预算没带上的）；写入以该会话的 agent 身份直接生效，不出审批卡、不要求有人值守（决策 6、
// 10）。非会话调用方（握手 token）沿用现有规则：终端里的人直接执行，外部调用方在桌面端审批；
// 两者都以用户身份写，看得到所有层的正常条目，新增时层要写明（没有会话可以推断）。
// 决策 7 的两条硬规则：只写所属的层由 memory_svc 把关，删除即归档——这里的 delete 只会调
// Archive，MemoryService 上也没有彻底删除。记忆的摘要与详情不进日志。

// errLayerOutsideSession 是会话以外新增记忆却没写明层。
const errLayerOutsideSession = "outside an Agentre session a memory needs a named layer: " +
	"--layer global, --department <department>, --agent <agent> or --project <project>"

// errNothingToUpdate 是 update memory 没给任何字段。
const errNothingToUpdate = errBadRequest("update memory: nothing to update (give --summary and/or --detail)")

// memoryScope 是一次记忆请求的范围：会话里是那个 agent（AI 写入者，只见本会话的层），
// 会话以外是用户本人（看得到所有层）。
type memoryScope struct {
	actor   memory_entity.Actor
	session bool
}

func scopeOf(cred credential) memoryScope {
	if cred.session {
		return memoryScope{actor: memory_entity.AIActor(cred.ref.AgentID, cred.ref.SessionID), session: true}
	}
	return memoryScope{actor: memory_entity.UserActor()}
}

// memoryWritable 是每个 op 能写的记忆字段。
var memoryWritable = map[agentrewire.CtlOp]map[string]bool{
	agentrewire.CtlOp_CTL_OP_CREATE: {"summary": true, "detail": true, "layer": true, "ownerId": true},
	agentrewire.CtlOp_CTL_OP_UPDATE: {"summary": true, "detail": true},
}

// requestKind 是请求针对的资源类型。
func requestKind(req *agentrewire.CtlRequest) agentrewire.CtlKind {
	switch op := req.GetOp().(type) {
	case *agentrewire.CtlRequest_List:
		return op.List.GetKind()
	case *agentrewire.CtlRequest_Get:
		return op.Get.GetKind()
	case *agentrewire.CtlRequest_Write:
		return op.Write.GetKind()
	}
	return agentrewire.CtlKind_CTL_KIND_UNSPECIFIED
}

func (h *ctlHandler) serveMemory(w http.ResponseWriter, r *http.Request, cred credential, req *agentrewire.CtlRequest) {
	if h.resources.Memory == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	ctx := r.Context()
	scope := scopeOf(cred)
	switch op := req.GetOp().(type) {
	case *agentrewire.CtlRequest_List:
		items, err := h.visibleMemories(ctx, scope)
		if err != nil {
			h.writeResourceErr(w, r, err)
			return
		}
		out := make([]*agentrewire.CtlResource, 0, len(items))
		for _, m := range items {
			out = append(out, memoryResource(m))
		}
		writeCtl(w, &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_List{List: &agentrewire.CtlListResponse{Items: out}}})
	case *agentrewire.CtlRequest_Get:
		m, err := h.visibleMemory(ctx, scope, op.Get.GetId())
		if err != nil {
			h.writeResourceErr(w, r, err)
			return
		}
		writeCtl(w, &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_Get{Get: &agentrewire.CtlGetResponse{Resource: memoryResource(m)}}})
	case *agentrewire.CtlRequest_Write:
		switch cred.caller(op.Write.GetCaller()).Class {
		case CallerSession, CallerHuman:
			h.respondMemoryWrite(ctx, w, r, scope, op.Write)
		default:
			h.approveMemoryInDesktop(w, r, scope, op.Write)
		}
	default:
		writeErr(w, http.StatusBadRequest, "empty request: one of list, get, write is required")
	}
}

// respondMemoryWrite 执行一次记忆写入并写响应。
func (h *ctlHandler) respondMemoryWrite(ctx context.Context, w http.ResponseWriter, r *http.Request, scope memoryScope, req *agentrewire.CtlWriteRequest) {
	resp, err := h.writeMemory(ctx, scope, req)
	if err != nil {
		h.writeResourceErr(w, r, err)
		return
	}
	logger.Ctx(ctx).Info("ctl_svc.serveMemory: memory written",
		zap.String("op", req.GetOp().String()),
		zap.Int64("memoryId", resp.GetId()),
		zap.String("actorKind", string(scope.actor.Kind)),
		zap.Int64("agentId", scope.actor.AgentID),
		zap.Int64("sessionId", scope.actor.SessionID),
		zap.Bool("existed", resp.GetExisted()),
		zap.Int32("overBudgetChars", resp.GetOverBudgetChars()))
	writeCtl(w, &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_Write{Write: resp}})
}

// approveMemoryInDesktop 把外部调用方的记忆写入挂到桌面端待审批队列。请求先对照当前数据
// 校验（看不到的条目、没写明的层在入队前就拒绝），批准后再按批准时的数据执行。
func (h *ctlHandler) approveMemoryInDesktop(w http.ResponseWriter, r *http.Request, scope memoryScope, req *agentrewire.CtlWriteRequest) {
	change, err := h.memoryChange(r.Context(), scope, req)
	if err != nil {
		h.writeResourceErr(w, r, err)
		return
	}
	h.approveInDesktop(w, r, approvalInput(blocks.RedactCtlCommand(req), change, nil), req.GetCallerInfo(),
		func(ctx context.Context) { h.respondMemoryWrite(ctx, w, r, scope, req) })
}

// memoryChange 是审批卡上的那条变更：名字是条目的摘要，字段是本次写入的摘要 / 详情
// （新增时另有所在层）的前后值。
func (h *ctlHandler) memoryChange(ctx context.Context, scope memoryScope, req *agentrewire.CtlWriteRequest) (*agentrewire.CtlChange, error) {
	set, err := memoryFields(req)
	if err != nil {
		return nil, err
	}
	doc := req.GetResource().GetMemory()
	change := &agentrewire.CtlChange{Op: req.GetOp(), Kind: agentrewire.CtlKind_CTL_KIND_MEMORY, Id: req.GetId()}
	var before *agentrewire.CtlMemory
	if req.GetOp() == agentrewire.CtlOp_CTL_OP_CREATE {
		owners, err := h.scopeOwners(ctx, scope)
		if err != nil {
			return nil, err
		}
		owner, err := memoryOwnerFor(owners, scope, doc.GetLayer(), doc.GetOwnerId())
		if err != nil {
			return nil, err
		}
		change.Name = doc.GetSummary()
		change.Fields = append(change.Fields, &agentrewire.CtlFieldChange{Field: "layer", After: nonEmpty(h.ownerLabel(ctx, owner))})
		before = &agentrewire.CtlMemory{}
	} else {
		if before, err = h.visibleMemory(ctx, scope, req.GetId()); err != nil {
			return nil, err
		}
		if req.GetOp() == agentrewire.CtlOp_CTL_OP_UPDATE && len(set) == 0 {
			return nil, errNothingToUpdate
		}
		change.Name = before.GetSummary()
	}
	for _, f := range []string{"summary", "detail"} {
		if !set[f] {
			continue
		}
		from, to := before.GetSummary(), doc.GetSummary()
		if f == "detail" {
			from, to = before.GetDetail(), doc.GetDetail()
		}
		if from != to {
			change.Fields = append(change.Fields, &agentrewire.CtlFieldChange{Field: f, Before: nonEmpty(from), After: nonEmpty(to)})
		}
	}
	return change, nil
}

// ownerLabel 是审批卡上的层：global，或 <层> <部门 / agent / 项目名>。
func (h *ctlHandler) ownerLabel(ctx context.Context, owner memory_entity.Owner) string {
	kind, ok := map[memory_entity.OwnerKind]agentrewire.CtlKind{
		memory_entity.OwnerDepartment: agentrewire.CtlKind_CTL_KIND_DEPARTMENT,
		memory_entity.OwnerAgent:      agentrewire.CtlKind_CTL_KIND_AGENT,
		memory_entity.OwnerProject:    agentrewire.CtlKind_CTL_KIND_PROJECT,
	}[owner.Kind]
	if !ok {
		return string(owner.Kind)
	}
	return string(owner.Kind) + " " + h.newNamer(ctx).refName(kind, owner.ID)
}

// scopeOwners 是会话里 agent 所属的层；会话以外没有所属的层（层由请求写明）。
func (h *ctlHandler) scopeOwners(ctx context.Context, scope memoryScope) ([]memory_entity.Owner, error) {
	if !scope.session {
		return nil, nil
	}
	return h.resources.Memory.SessionOwners(ctx, scope.actor)
}

// visibleMemories 是范围内可见的正常条目（会话里是本会话的层，会话以外是所有层）：按层（全局、
// 部门、agent、项目）排列，层内先带上的、后因超出预算没带上的。
func (h *ctlHandler) visibleMemories(ctx context.Context, scope memoryScope) ([]*agentrewire.CtlMemory, error) {
	var (
		vis *memory_svc.VisibleMemory
		err error
	)
	if scope.session {
		vis, err = h.resources.Memory.SessionVisible(ctx, scope.actor)
	} else {
		vis, err = h.resources.Memory.AllLayers(ctx)
	}
	if err != nil {
		return nil, err
	}
	var out []*agentrewire.CtlMemory
	for _, l := range vis.Layers {
		for _, it := range l.Carried {
			out = append(out, memoryDoc(it, false))
		}
		for _, it := range l.OverBudget {
			out = append(out, memoryDoc(it, true))
		}
	}
	return out, nil
}

// visibleMemory 按 id 取范围内可见的一条；看不到的（别的层、已归档）与不存在的一样是 404。
func (h *ctlHandler) visibleMemory(ctx context.Context, scope memoryScope, id int64) (*agentrewire.CtlMemory, error) {
	items, err := h.visibleMemories(ctx, scope)
	if err != nil {
		return nil, err
	}
	for _, m := range items {
		if m.GetId() == id {
			return m, nil
		}
	}
	return nil, errNotFound{kind: agentrewire.CtlKind_CTL_KIND_MEMORY, id: id}
}

func (h *ctlHandler) writeMemory(ctx context.Context, scope memoryScope, req *agentrewire.CtlWriteRequest) (*agentrewire.CtlWriteResponse, error) {
	mem, actor := h.resources.Memory, scope.actor
	set, err := memoryFields(req)
	if err != nil {
		return nil, err
	}
	doc := req.GetResource().GetMemory()
	var item *memory_svc.MemoryItem
	existed := false
	switch req.GetOp() {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		owners, err := h.scopeOwners(ctx, scope)
		if err != nil {
			return nil, err
		}
		owner, err := memoryOwnerFor(owners, scope, doc.GetLayer(), doc.GetOwnerId())
		if err != nil {
			return nil, err
		}
		res, err := mem.Create(ctx, &memory_svc.CreateRequest{
			Owner: owner, Summary: pick(set, "summary", doc.GetSummary(), ""), Detail: pick(set, "detail", doc.GetDetail(), ""), Actor: actor,
		})
		if err != nil {
			return nil, err
		}
		item, existed = res.Item, res.Existed
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		cur, err := h.visibleMemory(ctx, scope, req.GetId())
		if err != nil {
			return nil, err
		}
		if len(set) == 0 {
			return nil, errNothingToUpdate
		}
		item, err = mem.Update(ctx, &memory_svc.UpdateRequest{
			ID: cur.GetId(), Summary: pick(set, "summary", doc.GetSummary(), cur.GetSummary()),
			Detail: pick(set, "detail", doc.GetDetail(), cur.GetDetail()), Actor: actor,
		})
		if err != nil {
			return nil, err
		}
	default: // delete 即归档（决策 7）
		cur, err := h.visibleMemory(ctx, scope, req.GetId())
		if err != nil {
			return nil, err
		}
		if item, err = mem.Archive(ctx, cur.GetId(), actor); err != nil {
			return nil, err
		}
	}
	usage, err := mem.ListByOwner(ctx, item.Owner)
	if err != nil {
		return nil, err
	}
	written := memoryDoc(item, containsItem(usage.OverBudget, item.ID))
	return &agentrewire.CtlWriteResponse{
		Id: item.ID, Name: item.Summary, Existed: existed,
		OverBudgetChars: int32(max(0, usage.UsedChars-usage.Budget)),
		Resource:        memoryResource(written),
		Changes:         []*agentrewire.CtlChange{{Op: req.GetOp(), Kind: agentrewire.CtlKind_CTL_KIND_MEMORY, Id: item.ID, Name: item.Summary}},
	}, nil
}

// memoryFields 校验 op 与字段集合，返回本次写入的字段。
func memoryFields(req *agentrewire.CtlWriteRequest) (map[string]bool, error) {
	op := req.GetOp()
	switch op {
	case agentrewire.CtlOp_CTL_OP_CREATE, agentrewire.CtlOp_CTL_OP_UPDATE, agentrewire.CtlOp_CTL_OP_DELETE:
	default:
		return nil, errBadRequest("write needs an op: create, update or delete")
	}
	set := map[string]bool{}
	if op == agentrewire.CtlOp_CTL_OP_DELETE {
		return set, nil
	}
	if len(req.GetFields()) > 0 && req.GetResource().GetMemory() == nil {
		return nil, errBadRequest("the request document is not a memory")
	}
	for _, f := range req.GetFields() {
		if !memoryWritable[op][f] {
			return nil, errBadRequest(fmt.Sprintf("memory field %q cannot be written by %s", f, opName(op)))
		}
		set[f] = true
	}
	return set, nil
}

// memoryOwnerFor 把 create 给的层解析成归属。会话里：空 = agent 自己；部门层没指明时取直属
// 部门（owners 里最后一个部门）；项目层是会话所在的项目。会话以外没有可推断的层：除全局外
// 都要指明是哪一个。指明的归属不在 owners 里时原样交给 memory_svc，由它以「只能写所属的
// 层」拒绝——这条规则只在那里写一份。
func memoryOwnerFor(owners []memory_entity.Owner, scope memoryScope, layer string, ownerID int64) (memory_entity.Owner, error) {
	last := func(kind memory_entity.OwnerKind) (memory_entity.Owner, bool) {
		for i := len(owners) - 1; i >= 0; i-- {
			if owners[i].Kind == kind {
				return owners[i], true
			}
		}
		return memory_entity.Owner{}, false
	}
	kind := memory_entity.OwnerKind(layer)
	switch kind {
	case "", memory_entity.OwnerAgent:
		if ownerID == 0 {
			ownerID = scope.actor.AgentID
		}
		if ownerID == 0 {
			return memory_entity.Owner{}, errBadRequest(errLayerOutsideSession)
		}
		return memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: ownerID}, nil
	case memory_entity.OwnerGlobal:
		return memory_entity.Owner{Kind: kind, ID: ownerID}, nil
	case memory_entity.OwnerDepartment, memory_entity.OwnerProject:
		if ownerID > 0 {
			return memory_entity.Owner{Kind: kind, ID: ownerID}, nil
		}
		if !scope.session {
			return memory_entity.Owner{}, errBadRequest(errLayerOutsideSession)
		}
		if o, ok := last(kind); ok {
			return o, nil
		}
		if kind == memory_entity.OwnerDepartment {
			return memory_entity.Owner{}, errBadRequest("this agent is not in any department, so there is no department layer to write to")
		}
		return memory_entity.Owner{}, errBadRequest("this session is not in a project, so there is no project layer to write to")
	}
	return memory_entity.Owner{}, errBadRequest(fmt.Sprintf("unknown memory layer %q (one of: global, department, agent, project)", layer))
}

// pick 是字段在本次写入里的值：列在字段集合里取请求的值，否则取 fallback。
func pick(set map[string]bool, field, value, fallback string) string {
	if set[field] {
		return value
	}
	return fallback
}

func containsItem(items []*memory_svc.MemoryItem, id int64) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func memoryDoc(it *memory_svc.MemoryItem, overBudget bool) *agentrewire.CtlMemory {
	return &agentrewire.CtlMemory{
		Id: it.ID, Layer: string(it.Owner.Kind), OwnerId: it.Owner.ID, Summary: it.Summary, Detail: it.Detail,
		Pinned: it.Pinned, OverBudget: overBudget, ChangedAt: it.LastChange.At,
	}
}

func memoryResource(m *agentrewire.CtlMemory) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Memory{Memory: m}}
}
