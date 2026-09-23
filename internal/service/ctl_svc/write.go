package ctl_svc

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// writePlan 是一次写请求在执行前就定下来的全部内容：交给网关的写入与变更清单。
type writePlan struct {
	req *agentrewire.CtlWriteRequest
	// command 是给审批卡的命令行：客户端给的那一行，再抹掉请求里出现的密钥明文。
	command string
	writer  KindWriter
	write   Write
	change  *agentrewire.CtlChange
	cascade *blocks.CtlApprovalCascade
}

// serveWrite 先对照当前数据算出变更清单，再按调用方决定审批方式（spec「Routing and
// approval」）：人工直接执行；会话在该会话里出审批卡；外部调用挂到桌面端待审批队列。
func (h *ctlHandler) serveWrite(w http.ResponseWriter, r *http.Request, cred credential, req *agentrewire.CtlWriteRequest) {
	caller := cred.caller(req.GetCaller())
	plan, err := h.planWrite(r.Context(), req)
	if err != nil {
		h.writeResourceErr(w, r, err)
		return
	}
	switch caller.Class {
	case CallerHuman:
		_ = h.executeAndRespond(w, r, plan, caller) // 结果已写进响应
	case CallerSession:
		h.approveInSession(w, r, plan, caller)
	default:
		h.approveInDesktop(w, r, plan, caller)
	}
}

func (h *ctlHandler) planWrite(ctx context.Context, req *agentrewire.CtlWriteRequest) (*writePlan, error) {
	kind, op := req.GetKind(), req.GetOp()
	if _, ok := kindNames[kind]; !ok {
		return nil, errUnknownKind(kind)
	}
	switch op {
	case agentrewire.CtlOp_CTL_OP_CREATE, agentrewire.CtlOp_CTL_OP_UPDATE, agentrewire.CtlOp_CTL_OP_DELETE:
	default:
		return nil, errBadRequest("write needs an op: create, update or delete")
	}
	writer := h.resources.Writers[kind]
	if writer == nil {
		return nil, errNotReady("control service not ready")
	}
	plan := &writePlan{req: req, command: redactCommand(req), writer: writer, write: Write{Fields: map[string]bool{}}}
	if op != agentrewire.CtlOp_CTL_OP_CREATE {
		cur, err := h.getResource(ctx, kind, req.GetId())
		if err != nil {
			return nil, err
		}
		plan.write.Cur = cur
	}
	fields := append([]string(nil), req.GetFields()...)
	nm := h.newNamer(ctx)
	change := &agentrewire.CtlChange{Op: op, Kind: kind, Id: req.GetId()}
	if op == agentrewire.CtlOp_CTL_OP_DELETE {
		plan.write.Cascade, plan.write.Force = req.GetCascade(), req.GetForce()
		change.Name = nm.label(plan.write.Cur)
		if kind == agentrewire.CtlKind_CTL_KIND_DEPARTMENT && req.GetCascade() && h.resources.Cascade != nil {
			depts, agents, err := h.resources.Cascade.CascadeImpact(ctx, req.GetId())
			if err != nil {
				return nil, err
			}
			plan.cascade = &blocks.CtlApprovalCascade{Departments: depts, Agents: agents}
			change.Note = cascadeNote(plan.cascade)
		}
		plan.change = change
		return plan, nil
	}

	next, err := mergeDoc(kind, op, plan.write.Cur, req.GetResource(), fields)
	if err != nil {
		return nil, err
	}
	if p := next.GetProject(); p != nil && op == agentrewire.CtlOp_CTL_OP_UPDATE &&
		(len(req.GetAddMemberAgentIds()) > 0 || len(req.GetRemoveMemberAgentIds()) > 0) {
		p.MemberAgentIds = mergeMembers(p.GetMemberAgentIds(), req.GetAddMemberAgentIds(), req.GetRemoveMemberAgentIds())
		fields = append(fields, "memberAgentIds")
	}
	for _, f := range fields {
		plan.write.Fields[f] = true
	}
	plan.write.Next = next
	change.Fields, err = nm.fieldChanges(kind, plan.write.Cur, next, fields)
	if err != nil {
		return nil, err
	}
	if plan.write.Cur != nil {
		change.Name = nm.label(plan.write.Cur)
	} else {
		change.Name = nm.label(next)
	}
	plan.change = change
	return plan, nil
}

// redactCommand 不信客户端已经脱敏：请求里带的密钥明文若出现在命令行里，一律换成 …。
func redactCommand(req *agentrewire.CtlWriteRequest) string {
	command := req.GetCommand()
	for _, secret := range []string{req.GetResource().GetProvider().GetApiKey(), req.GetResource().GetBackend().GetToken()} {
		if secret != "" {
			command = strings.ReplaceAll(command, secret, "…")
		}
	}
	return command
}

// execute 经网关落库，返回 ctl 响应。
func (h *ctlHandler) execute(ctx context.Context, plan *writePlan) (*agentrewire.CtlWriteResponse, error) {
	var err error
	id := plan.req.GetId()
	switch plan.req.GetOp() {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		id, err = plan.writer.Create(ctx, plan.write)
		plan.change.Id = id
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		err = plan.writer.Update(ctx, plan.write)
	default:
		err = plan.writer.Delete(ctx, plan.write)
	}
	if err != nil {
		return nil, err
	}
	return &agentrewire.CtlWriteResponse{Id: id, Name: plan.change.GetName(), Changes: []*agentrewire.CtlChange{plan.change}}, nil
}

// resultText 是审批卡批准后的结果行（spec「审批卡（会话内）」）。
func resultText(c *agentrewire.CtlChange) string {
	subject := kindNames[c.GetKind()] + " " + c.GetName()
	switch c.GetOp() {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		return fmt.Sprintf("已创建 %s（id %d）", subject, c.GetId())
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		return "已更新 " + subject
	default:
		return "已删除 " + subject
	}
}

// executeAndRespond 执行并写响应；返回执行错误（nil = 成功）供审批卡回写结果。
func (h *ctlHandler) executeAndRespond(w http.ResponseWriter, r *http.Request, plan *writePlan, caller Caller) error {
	resp, err := h.execute(r.Context(), plan)
	log := logger.Ctx(r.Context()).With(
		zap.String("op", plan.req.GetOp().String()),
		zap.String("kind", plan.req.GetKind().String()),
		zap.Int64("id", plan.change.GetId()),
		zap.Int("callerClass", int(caller.Class)),
		zap.Int64("sessionId", caller.Session.SessionID))
	if err != nil {
		log.Warn("ctl_svc.serveWrite: write failed", zap.Error(err))
		h.writeResourceErr(w, r, err)
		return err
	}
	log.Info("ctl_svc.serveWrite: written")
	writeCtl(w, &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_Write{Write: resp}})
	return nil
}

func (h *ctlHandler) timeout() time.Duration {
	if h.approvalTimeout > 0 {
		return h.approvalTimeout
	}
	return approvalTimeout
}

// announcePending 在挂起等审批前先发 102，告诉 agrctl 去哪里批准；最终响应不带这个头。
func announcePending(w http.ResponseWriter, where string) {
	w.Header().Set(ApprovalPendingHeader, where)
	w.WriteHeader(http.StatusProcessing)
	w.Header().Del(ApprovalPendingHeader)
}

type approvalOutcome int

const (
	outcomeApproved approvalOutcome = iota
	outcomeRejected
	outcomeTimedOut
	outcomeGone // 调用方断开
)

func (h *ctlHandler) await(ctx context.Context, ch <-chan bool) approvalOutcome {
	timer := time.NewTimer(h.timeout())
	defer timer.Stop()
	select {
	case allow := <-ch:
		if allow {
			return outcomeApproved
		}
		return outcomeRejected
	case <-timer.C:
		return outcomeTimedOut
	case <-ctx.Done():
		return outcomeGone
	}
}

// approveInSession 在 token 绑定的会话里出一张 toolKey=ctl 的审批卡，批准后才执行。
func (h *ctlHandler) approveInSession(w http.ResponseWriter, r *http.Request, plan *writePlan, caller Caller) {
	if h.approvals == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	sessionID := caller.Session.SessionID
	requestID := uuid.NewString()
	ch, err := h.approvals.BeginToolApproval(r.Context(), sessionID, &blocks.ToolApprovalBlock{
		ToolKey:   agenttool.KeyCtl,
		RequestID: requestID,
		ToolName:  "ctl_" + opName(plan.req.GetOp()) + "_" + kindNames[plan.req.GetKind()],
		ToolInput: approvalInput(plan.command, plan.change, plan.cascade).ToolInput(),
		Status:    "pending",
	})
	if err != nil {
		writeErr(w, http.StatusConflict, fmt.Sprintf("cannot ask for approval in session #%d: %v", sessionID, err))
		return
	}
	finish := func(ctx context.Context, status, result string) {
		if err := h.approvals.FinishToolApproval(ctx, sessionID, requestID, status, result); err != nil {
			logger.Ctx(ctx).Warn("ctl_svc.approveInSession: finish approval failed",
				zap.Int64("sessionId", sessionID), zap.String("requestId", requestID), zap.Error(err))
		}
	}
	announcePending(w, fmt.Sprintf("session=%d", sessionID))
	switch h.await(r.Context(), ch) {
	case outcomeApproved:
		if err := h.executeAndRespond(w, r, plan, caller); err != nil {
			finish(r.Context(), "approved", "执行失败："+err.Error())
			return
		}
		finish(r.Context(), "approved", resultText(plan.change))
	case outcomeRejected:
		finish(r.Context(), "denied", "")
		writeErr(w, http.StatusForbidden, fmt.Sprintf("rejected in session #%d", sessionID))
	case outcomeTimedOut:
		finish(r.Context(), "expired", "")
		writeErr(w, http.StatusGatewayTimeout, "approval timed out")
	case outcomeGone:
		finish(context.Background(), "expired", "") // 请求 ctx 已死
	}
}

// approveInDesktop 把外部调用挂到桌面端待审批队列；无人处理到超时即撤下，等同拒绝。
func (h *ctlHandler) approveInDesktop(w http.ResponseWriter, r *http.Request, plan *writePlan, caller Caller) {
	if h.external == nil {
		writeErr(w, http.StatusServiceUnavailable, "approval in the Agentre desktop is not available")
		return
	}
	requestID := uuid.NewString()
	ch, err := h.external.Enqueue(r.Context(), ExternalApproval{
		RequestID: requestID,
		Input:     approvalInput(plan.command, plan.change, plan.cascade),
	})
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot ask for approval in the Agentre desktop: "+err.Error())
		return
	}
	announcePending(w, "desktop")
	switch h.await(r.Context(), ch) {
	case outcomeApproved:
		_ = h.executeAndRespond(w, r, plan, caller) // 结果已写进响应
	case outcomeRejected:
		writeErr(w, http.StatusForbidden, "rejected in the Agentre desktop")
	case outcomeTimedOut:
		h.external.Withdraw(requestID)
		writeErr(w, http.StatusGatewayTimeout, "approval timed out")
	case outcomeGone:
		h.external.Withdraw(requestID)
	}
}
