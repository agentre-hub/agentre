package ctl_svc

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/service/memory_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 记忆（spec 2026-11-11「Agent 读写记忆」）只对会话级 token 开放：读只见本会话可见的正常
// 条目（含因超出预算没带上的）；写入以该会话的 agent 身份直接生效，不出审批卡、不要求
// 有人值守（决策 6、10）。决策 7 的两条硬规则：只写所属的层由 memory_svc 把关，删除即
// 归档——这里的 delete 只会调 Archive，MemoryService 上也没有彻底删除。
// 记忆的摘要与详情不进日志。

// errNotInSession 是非会话调用方碰记忆。
const errNotInSession = "memory is only available to an agent inside an Agentre session; people edit memories on the Memory page of the Agentre desktop"

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
	if !cred.session {
		writeErr(w, http.StatusForbidden, errNotInSession)
		return
	}
	if h.resources.Memory == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	ctx := r.Context()
	actor := memory_entity.AIActor(cred.ref.AgentID, cred.ref.SessionID)
	switch op := req.GetOp().(type) {
	case *agentrewire.CtlRequest_List:
		items, err := h.visibleMemories(ctx, actor)
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
		m, err := h.visibleMemory(ctx, actor, op.Get.GetId())
		if err != nil {
			h.writeResourceErr(w, r, err)
			return
		}
		writeCtl(w, &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_Get{Get: &agentrewire.CtlGetResponse{Resource: memoryResource(m)}}})
	case *agentrewire.CtlRequest_Write:
		resp, err := h.writeMemory(ctx, actor, op.Write)
		if err != nil {
			h.writeResourceErr(w, r, err)
			return
		}
		logger.Ctx(ctx).Info("ctl_svc.serveMemory: memory written",
			zap.String("op", op.Write.GetOp().String()),
			zap.Int64("memoryId", resp.GetId()),
			zap.Int64("agentId", actor.AgentID),
			zap.Int64("sessionId", actor.SessionID),
			zap.Bool("existed", resp.GetExisted()),
			zap.Int32("overBudgetChars", resp.GetOverBudgetChars()))
		writeCtl(w, &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_Write{Write: resp}})
	}
}

// visibleMemories 是本会话可见的正常条目：按层（全局、部门、agent、项目）排列，层内先带上
// 的、后因超出预算没带上的。
func (h *ctlHandler) visibleMemories(ctx context.Context, actor memory_entity.Actor) ([]*agentrewire.CtlMemory, error) {
	vis, err := h.resources.Memory.SessionVisible(ctx, actor)
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

// visibleMemory 按 id 取本会话可见的一条；看不到的（别的层、已归档）与不存在的一样是 404。
func (h *ctlHandler) visibleMemory(ctx context.Context, actor memory_entity.Actor, id int64) (*agentrewire.CtlMemory, error) {
	items, err := h.visibleMemories(ctx, actor)
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

func (h *ctlHandler) writeMemory(ctx context.Context, actor memory_entity.Actor, req *agentrewire.CtlWriteRequest) (*agentrewire.CtlWriteResponse, error) {
	mem := h.resources.Memory
	set, err := memoryFields(req)
	if err != nil {
		return nil, err
	}
	doc := req.GetResource().GetMemory()
	var item *memory_svc.MemoryItem
	existed := false
	switch req.GetOp() {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		owners, err := mem.SessionOwners(ctx, actor)
		if err != nil {
			return nil, err
		}
		owner, err := memoryOwnerFor(owners, actor.AgentID, doc.GetLayer(), doc.GetOwnerId())
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
		cur, err := h.visibleMemory(ctx, actor, req.GetId())
		if err != nil {
			return nil, err
		}
		if len(set) == 0 {
			return nil, errBadRequest("update memory: nothing to update (give --summary and/or --detail)")
		}
		item, err = mem.Update(ctx, &memory_svc.UpdateRequest{
			ID: cur.GetId(), Summary: pick(set, "summary", doc.GetSummary(), cur.GetSummary()),
			Detail: pick(set, "detail", doc.GetDetail(), cur.GetDetail()), Actor: actor,
		})
		if err != nil {
			return nil, err
		}
	default: // delete 即归档（决策 7）
		cur, err := h.visibleMemory(ctx, actor, req.GetId())
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

// memoryOwnerFor 把 create 给的层解析成归属：空 = agent 自己；部门层没指明时取直属部门
// （owners 里最后一个部门）；项目层是会话所在的项目。指明的归属不在 owners 里时原样交给
// memory_svc，由它以「只能写所属的层」拒绝——这条规则只在那里写一份。
func memoryOwnerFor(owners []memory_entity.Owner, agentID int64, layer string, ownerID int64) (memory_entity.Owner, error) {
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
			ownerID = agentID
		}
		return memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: ownerID}, nil
	case memory_entity.OwnerGlobal:
		return memory_entity.Owner{Kind: kind, ID: ownerID}, nil
	case memory_entity.OwnerDepartment, memory_entity.OwnerProject:
		if ownerID > 0 {
			return memory_entity.Owner{Kind: kind, ID: ownerID}, nil
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
