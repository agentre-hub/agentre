package ctl_svc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 资源读写（`POST /ctl/v1/resources`，契约见 pkg/wire 的 Ctl* 消息）：执行者只接受按
// id 的操作；名字 / 路径解析、输出格式都在 agrctl 里。读经下面四个网关落到现有服务层。

// OrgResources 读 Agent 与部门（department_svc 的组织视图）。
type OrgResources interface {
	ListAgents(ctx context.Context) ([]*agentrewire.CtlAgent, error)
	ListDepartments(ctx context.Context) ([]*agentrewire.CtlDepartment, error)
}

// ProjectResources 读项目（含直接成员与各设备路径）。
type ProjectResources interface {
	ListProjects(ctx context.Context) ([]*agentrewire.CtlProject, error)
}

// ProviderResources 读 LLM 提供方与其下的模型（含被后端引用的次数）。
type ProviderResources interface {
	ListProviders(ctx context.Context) ([]*agentrewire.CtlProvider, error)
	ListModels(ctx context.Context) ([]*agentrewire.CtlModel, error)
}

// BackendResources 读 Agent 后端。
type BackendResources interface {
	ListBackends(ctx context.Context) ([]*agentrewire.CtlBackend, error)
}

// Write 是执行者合并好的一次按 id 写入，交给该类资源的 KindWriter 经服务层落库。
type Write struct {
	// Cur 是 update / delete 目标的当前文档（与 get 同形：密钥已脱敏）；create 时为 nil。
	Cur *agentrewire.CtlResource
	// Next 是 create / update 写入后的完整文档：Cur 叠加本次字段。密钥字段只有本次写入时
	// 才非空（明文），否则为空——网关据此沿用原值。delete 时为 nil。
	Next *agentrewire.CtlResource
	// Fields 是本次写入的字段（文档字段的 JSON 名）；create 时没列出的字段取服务层默认值。
	Fields map[string]bool
	// Cascade / Force 是 delete 的选项（部门级联；提供方 / 模型仍被引用时也删）。
	Cascade, Force bool
}

// KindWriter 按 id 写入一类资源，只经现有服务层（不绕过校验与同步通知）；服务层的错误
// 原样返回。
type KindWriter interface {
	Create(ctx context.Context, w Write) (int64, error)
	Update(ctx context.Context, w Write) error
	Delete(ctx context.Context, w Write) error
}

// CascadeCounter 统计级联删除一个部门会连带删除的子部门与 Agent 数（给审批卡）。
type CascadeCounter interface {
	CascadeImpact(ctx context.Context, departmentID int64) (departments, agents int, err error)
}

// Resources 是资源接口依赖的全部网关；读网关任一为 nil 视为未就绪（503）。
type Resources struct {
	Org       OrgResources
	Projects  ProjectResources
	Providers ProviderResources
	Backends  BackendResources
	// Writers 按资源类型写入；某类缺席时该类的写请求 503。
	Writers map[agentrewire.CtlKind]KindWriter
	Cascade CascadeCounter
}

func (r Resources) ready() bool {
	return r.Org != nil && r.Projects != nil && r.Providers != nil && r.Backends != nil
}

// kindNames 是错误消息里的资源名，与 agrctl 的资源名一致。
var kindNames = map[agentrewire.CtlKind]string{
	agentrewire.CtlKind_CTL_KIND_AGENT:      "agent",
	agentrewire.CtlKind_CTL_KIND_DEPARTMENT: "department",
	agentrewire.CtlKind_CTL_KIND_PROJECT:    "project",
	agentrewire.CtlKind_CTL_KIND_PROVIDER:   "provider",
	agentrewire.CtlKind_CTL_KIND_MODEL:      "model",
	agentrewire.CtlKind_CTL_KIND_BACKEND:    "backend",
}

// errUnknownKind 是请求里的资源类型不认识（含未指定）。
type errUnknownKind agentrewire.CtlKind

func (e errUnknownKind) Error() string {
	return fmt.Sprintf("unknown resource kind %s", agentrewire.CtlKind(e))
}

func (h *ctlHandler) serveResources(w http.ResponseWriter, r *http.Request, cred credential) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "resources requires POST")
		return
	}
	if !h.resources.ready() {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read request body failed")
		return
	}
	var req agentrewire.CtlRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch op := req.GetOp().(type) {
	case *agentrewire.CtlRequest_List:
		items, err := h.listResources(r.Context(), op.List.GetKind())
		if err != nil {
			h.writeResourceErr(w, r, err)
			return
		}
		writeCtl(w, &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_List{List: &agentrewire.CtlListResponse{Items: items}}})
	case *agentrewire.CtlRequest_Get:
		res, err := h.getResource(r.Context(), op.Get.GetKind(), op.Get.GetId())
		if err != nil {
			h.writeResourceErr(w, r, err)
			return
		}
		writeCtl(w, &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_Get{Get: &agentrewire.CtlGetResponse{Resource: res}}})
	case *agentrewire.CtlRequest_Write:
		h.serveWrite(w, r, cred, op.Write)
	default:
		writeErr(w, http.StatusBadRequest, "empty request: one of list, get, write is required")
	}
}

// errNotFound 是按 id 取不到资源。
type errNotFound struct {
	kind agentrewire.CtlKind
	id   int64
}

func (e errNotFound) Error() string {
	return fmt.Sprintf("%s id %d not found", kindNames[e.kind], e.id)
}

func (h *ctlHandler) writeResourceErr(w http.ResponseWriter, r *http.Request, err error) {
	switch err.(type) {
	case errUnknownKind:
		writeErr(w, http.StatusBadRequest, err.Error())
	case errNotFound:
		writeErr(w, http.StatusNotFound, err.Error())
	case errBadRequest:
		writeErr(w, http.StatusBadRequest, err.Error())
	case errNotReady:
		writeErr(w, http.StatusServiceUnavailable, err.Error())
	default:
		// 服务层的拒绝原样透出（spec「Command surface」）。
		logger.Ctx(r.Context()).Warn("ctl_svc.serveResources: request failed", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// listResources 取某类资源的全部条目，并在出口统一去掉密钥明文。
func (h *ctlHandler) listResources(ctx context.Context, kind agentrewire.CtlKind) ([]*agentrewire.CtlResource, error) {
	var out []*agentrewire.CtlResource
	switch kind {
	case agentrewire.CtlKind_CTL_KIND_AGENT:
		items, err := h.resources.Org.ListAgents(ctx)
		for _, it := range items {
			out = append(out, &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Agent{Agent: it}})
		}
		return out, err
	case agentrewire.CtlKind_CTL_KIND_DEPARTMENT:
		items, err := h.resources.Org.ListDepartments(ctx)
		for _, it := range items {
			out = append(out, &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Department{Department: it}})
		}
		return out, err
	case agentrewire.CtlKind_CTL_KIND_PROJECT:
		items, err := h.resources.Projects.ListProjects(ctx)
		for _, it := range items {
			out = append(out, &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Project{Project: it}})
		}
		return out, err
	case agentrewire.CtlKind_CTL_KIND_PROVIDER:
		items, err := h.resources.Providers.ListProviders(ctx)
		for _, it := range items {
			it.ApiKey = MaskSecret(it.GetApiKey())
			out = append(out, &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: it}})
		}
		return out, err
	case agentrewire.CtlKind_CTL_KIND_MODEL:
		items, err := h.resources.Providers.ListModels(ctx)
		for _, it := range items {
			out = append(out, &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Model{Model: it}})
		}
		return out, err
	case agentrewire.CtlKind_CTL_KIND_BACKEND:
		items, err := h.resources.Backends.ListBackends(ctx)
		for _, it := range items {
			it.Token = "" // 只写字段：响应里只有 token_set。
			out = append(out, &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: it}})
		}
		return out, err
	default:
		return nil, errUnknownKind(kind)
	}
}

// getResource 按 id 取一条。列表本身已带齐关联信息，详情就是其中那一条。
func (h *ctlHandler) getResource(ctx context.Context, kind agentrewire.CtlKind, id int64) (*agentrewire.CtlResource, error) {
	items, err := h.listResources(ctx, kind)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if resourceID(it) == id {
			return it, nil
		}
	}
	return nil, errNotFound{kind: kind, id: id}
}

func resourceID(r *agentrewire.CtlResource) int64 {
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

// maskBullets 是掩码中间的圆点数，与服务层 LLMProvider.MaskedAPIKey 同形。
const maskBullets = 6

// MaskSecret 把密钥收成「前 4 位、圆点、后 4 位」；8 位以内全部换成圆点，不露任何字符。
// 按字符（rune）计，并且幂等：服务层已掩码的值再过一遍不变，所以 ctl 出口可以无条件套用。
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= 8 {
		return strings.Repeat("•", len(runes))
	}
	return string(runes[:4]) + strings.Repeat("•", maskBullets) + string(runes[len(runes)-4:])
}

// writeCtl 以 protojson 写成功响应。
func writeCtl(w http.ResponseWriter, resp *agentrewire.CtlResponse) {
	body, err := protojson.Marshal(resp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "encode response failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
