package handlers

import (
	"context"
	"net/http"
	"slices"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 控制台会话里的 OpenClaw token(规格 2026-09-22 agrctl-resource-management「server 执行者」):
// server 路径不接 token 写入,「除非该后端就绑定在发起请求的这台 agentred 上,这时由
// agentred 在本地写入」。token 存在绑定设备的设备本地凭据里(规格 2026-09-17
// device-local-backend-credentials,按后端 syncId 存),这里复用同一个存储
// (CtlProxyDeps.OpenClawTokens = BackendCredentialHandlers),不另起一份。
//
// token 的明文只在请求正文 → 本地存储之间走一趟:不交 server、不进审批卡、不进日志。

// ctlTokenField 是 CtlBackend.token 在写请求 fields 里的 JSON 名。
const ctlTokenField = "token"

// consoleWritePlan 是一次控制台会话写入要做的事。
type consoleWritePlan struct {
	// req 是原请求(审批卡的工具名与命令行取自它)。
	req *agentrewire.CtlWriteRequest
	// serverBody 是交给 server 的请求正文;nil = 没有要交 server 的字段。
	serverBody []byte
	// local 非 nil = token 写进本机的设备本地凭据。
	local *localOpenClawToken
}

// localOpenClawToken 是一次要在本机写入的 OpenClaw token;token 为空表示清除。
type localOpenClawToken struct {
	backendID int64
	name      string
	syncID    string
	token     string
}

// planConsoleWrite 决定写入的去向。只有「更新一个绑在本机的 openclaw 后端、且带 token」
// 才把 token 拆出来本地写;其余原样交 server(绑在别处的后端带 token,server 照旧拒绝)。
func (p *ctlProxy) planConsoleWrite(ctx context.Context, write *agentrewire.CtlWriteRequest, body []byte) consoleWritePlan {
	plan := consoleWritePlan{req: write, serverBody: body}
	if p.deps.OpenClawTokens == nil || p.deps.Self == "" ||
		write.GetKind() != agentrewire.CtlKind_CTL_KIND_BACKEND || write.GetOp() != agentrewire.CtlOp_CTL_OP_UPDATE ||
		!slices.Contains(write.GetFields(), ctlTokenField) {
		return plan
	}
	b := p.serverBackend(ctx, write.GetId())
	if !p.boundHere(b) {
		return plan
	}
	plan.local = &localOpenClawToken{
		backendID: b.GetId(), name: b.GetName(), syncID: b.GetSyncId(),
		token: write.GetResource().GetBackend().GetToken(),
	}
	rest := proto.CloneOf(write)
	rest.Fields = slices.DeleteFunc(rest.Fields, func(f string) bool { return f == ctlTokenField })
	if be := rest.GetResource().GetBackend(); be != nil {
		be.Token = ""
	}
	rest.Command = redactCtlCommand(write)
	plan.serverBody = nil
	if len(rest.GetFields()) > 0 {
		raw, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Write{Write: rest}})
		if err != nil {
			// 刚解开的消息重新编码不会失败;真失败就退回原样交 server,由它拒绝 token。
			return consoleWritePlan{req: write, serverBody: body}
		}
		plan.serverBody = raw
	}
	return plan
}

// serverBackend 向 server 取一个后端(拿 syncId 与绑定设备)。取不到返回 nil,写入照原样
// 交 server,错误由那一步如实报出。
func (p *ctlProxy) serverBackend(ctx context.Context, id int64) *agentrewire.CtlBackend {
	body, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Get{
		Get: &agentrewire.CtlGetRequest{Kind: agentrewire.CtlKind_CTL_KIND_BACKEND, Id: id},
	}})
	if err != nil {
		return nil
	}
	status, raw, err := p.postServer(ctx, serverResourcesPath, body)
	if err != nil || status != http.StatusOK {
		return nil
	}
	var resp agentrewire.CtlResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &resp); err != nil {
		return nil
	}
	return resp.GetGet().GetResource().GetBackend()
}

// boundHere:openclaw 后端、有 syncId、绑定设备就是这台 agentred。
func (p *ctlProxy) boundHere(b *agentrewire.CtlBackend) bool {
	return b != nil && p.deps.Self != "" &&
		b.GetType() == string(agent_backend_entity.TypeOpenClaw) && b.GetSyncId() != "" &&
		b.GetDeviceFingerprint() == string(p.deps.Self)
}

// markChanged 在变更清单里给这个后端补一行 secret 的 token(只说「已更新」,不带值);
// 没有要交 server 的字段时,清单只有这一条。
func (l *localOpenClawToken) markChanged(preview *agentrewire.CtlWriteResponse) *agentrewire.CtlWriteResponse {
	if preview == nil {
		preview = &agentrewire.CtlWriteResponse{Id: l.backendID, Name: l.name}
	}
	secret := &agentrewire.CtlFieldChange{Field: ctlTokenField, Secret: true}
	for _, c := range preview.GetChanges() {
		if c.GetKind() == agentrewire.CtlKind_CTL_KIND_BACKEND && c.GetId() == l.backendID {
			c.Fields = append(c.Fields, secret)
			return preview
		}
	}
	preview.Changes = append(preview.Changes, &agentrewire.CtlChange{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_BACKEND,
		Id: l.backendID, Name: l.name, Fields: []*agentrewire.CtlFieldChange{secret},
	})
	return preview
}

// saveLocalToken 写入(或清除)本机的 OpenClaw token。
func (p *ctlProxy) saveLocalToken(ctx context.Context, l *localOpenClawToken) error {
	req := &agentrewire.OpenClawTokenSetRequest{SyncId: l.syncID, Token: l.token, Clear: l.token == ""}
	if _, err := p.deps.OpenClawTokens.SetOpenClawToken(ctx, req); err != nil {
		logger.Ctx(ctx).Warn("handlers.ctlProxy.saveLocalToken: save openclaw token failed",
			zap.Int64("backendId", l.backendID), zap.String("syncId", l.syncID), zap.Error(err))
		return err
	}
	return nil
}

// overlayLocalTokenState 把 server 读应答里绑在本机的 openclaw 后端的 token 状态换成本机
// 设备本地凭据的 set / unset;其它后端保持 server 报的值。本机问不到时也保持原值。
func (p *ctlProxy) overlayLocalTokenState(ctx context.Context, raw []byte) []byte {
	if p.deps.OpenClawTokens == nil || p.deps.Self == "" {
		return raw
	}
	var resp agentrewire.CtlResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &resp); err != nil {
		return raw
	}
	var backends []*agentrewire.CtlBackend
	for _, it := range resp.GetList().GetItems() {
		backends = append(backends, it.GetBackend())
	}
	backends = append(backends, resp.GetGet().GetResource().GetBackend())
	changed := false
	for _, b := range backends {
		if !p.boundHere(b) {
			continue
		}
		st, err := p.deps.OpenClawTokens.Status(ctx, &agentrewire.BackendCredentialStatusRequest{
			BackendType: string(agent_backend_entity.TypeOpenClaw), SyncId: b.GetSyncId(),
		})
		if err != nil {
			logger.Ctx(ctx).Warn("handlers.ctlProxy.overlayLocalTokenState: local credential status unavailable",
				zap.Int64("backendId", b.GetId()), zap.Error(err))
			continue
		}
		b.TokenState = agentrewire.CtlTokenState_CTL_TOKEN_STATE_UNSET
		if st.GetOpenclawTokenSaved() {
			b.TokenState = agentrewire.CtlTokenState_CTL_TOKEN_STATE_SET
		}
		changed = true
	}
	if !changed {
		return raw
	}
	out, err := protojson.Marshal(&resp)
	if err != nil {
		return raw
	}
	return out
}
