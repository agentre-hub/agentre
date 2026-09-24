package handlers

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 控制台会话里的 OpenClaw token(规格 2026-09-22 agrctl-resource-management「server 执行者」):
// server 路径不接 token 写入,「除非该后端就绑定在发起请求的这台 agentred 上,这时由
// agentred 在本地写入」。token 存在绑定设备的设备本地凭据里(规格 2026-09-17
// device-local-backend-credentials,按后端 syncId 存),这里复用同一个存储
// (CtlProxyDeps.OpenClawTokens = BackendCredentialHandlers),不另起一份。
//
// token 的明文只在请求正文 → 本地存储之间走一趟:不交 server、不进审批卡、不进日志。

// ctlTokenField / ctlDeviceField 是 CtlBackend.token / device 在写请求 fields 里的 JSON 名。
const (
	ctlTokenField  = "token"
	ctlDeviceField = "device"
)

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
	// created = 后端由这次 create 新建:id 与 syncId 要等 server 写完再取,并且要再确认
	// 它确实绑在本机。
	created bool
}

// planConsoleWrite 决定写入的去向。带 token、且后端绑在本机的 openclaw 写入才把 token
// 拆出来本地写:
//   - update:按 id 向 server 取后端,当前就绑在本机;同一条命令还改 device 时不拆 ——
//     token 该落在哪台机器要写完才知道,交 server 照旧拒绝;
//   - create:不给 device(server 会绑到发起请求的这台机器)或 device 就是本机指纹。
//
// 其余原样交 server(绑在别处的后端带 token,server 照旧拒绝)。
func (p *ctlProxy) planConsoleWrite(ctx context.Context, write *agentrewire.CtlWriteRequest, body []byte) consoleWritePlan {
	plan := consoleWritePlan{req: write, serverBody: body}
	if p.deps.OpenClawTokens == nil || p.deps.Self == "" ||
		write.GetKind() != agentrewire.CtlKind_CTL_KIND_BACKEND || !slices.Contains(write.GetFields(), ctlTokenField) {
		return plan
	}
	doc := write.GetResource().GetBackend()
	// 边界处 trim 一次：空白等同空——与显式清除走同一路径，不当成新令牌写入本地凭据；
	// 下游（saveLocalToken 的 Clear 判定）一律按 == "" 判定，不用到处补 trim。
	token := strings.TrimSpace(doc.GetToken())
	switch write.GetOp() {
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		if slices.Contains(write.GetFields(), ctlDeviceField) {
			return plan
		}
		b := p.serverBackend(ctx, write.GetId())
		if !p.boundHere(b) {
			return plan
		}
		plan.local = &localOpenClawToken{backendID: b.GetId(), name: b.GetName(), syncID: b.GetSyncId(), token: token}
	case agentrewire.CtlOp_CTL_OP_CREATE:
		if doc.GetType() != string(agent_backend_entity.TypeOpenClaw) || (doc.GetDevice() != "" && doc.GetDevice() != string(p.deps.Self)) {
			return plan
		}
		plan.local = &localOpenClawToken{name: doc.GetName(), token: token, created: true}
	default:
		return plan
	}
	rest := proto.CloneOf(write)
	rest.Fields = slices.DeleteFunc(rest.Fields, func(f string) bool { return f == ctlTokenField })
	if be := rest.GetResource().GetBackend(); be != nil {
		be.Token = ""
	}
	rest.Command = transcriptblocks.RedactCtlCommand(write)
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

// errCreatedNotBoundHere:create 写完后 server 报的绑定设备不是本机,token 不写。
var errCreatedNotBoundHere = errors.New("the new backend is not bound to this agentred")

// resolveCreated 取 create 刚建出的后端的 id 与 syncId,并确认它绑在本机。
func (p *ctlProxy) resolveCreated(ctx context.Context, l *localOpenClawToken, written []byte) error {
	var resp agentrewire.CtlResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(written, &resp); err != nil || resp.GetWrite().GetId() == 0 {
		return errCreatedNotBoundHere
	}
	b := p.serverBackend(ctx, resp.GetWrite().GetId())
	if !p.boundHere(b) {
		return errCreatedNotBoundHere
	}
	l.backendID, l.syncID = b.GetId(), b.GetSyncId()
	return nil
}

// saveLocalToken 写入(或清除)本机的 OpenClaw token。create 先按写入应答取新后端。
func (p *ctlProxy) saveLocalToken(ctx context.Context, l *localOpenClawToken, written []byte) error {
	if l.created {
		if err := p.resolveCreated(ctx, l, written); err != nil {
			logger.Ctx(ctx).Warn("handlers.ctlProxy.saveLocalToken: created backend not resolvable as bound here",
				zap.String("name", l.name), zap.Error(err))
			return err
		}
	}
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
