package sync_svc

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/department_entity"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/department_repo"
	"github.com/agentre-ai/agentre/internal/repository/project_repo"
	"github.com/agentre-ai/agentre/internal/repository/syncstate_repo"
)

// ── 部门 ────────────────────────────────────────────────────────────────────

type departmentPayload struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Icon          string `json:"icon"`
	AccentColor   string `json:"accent_color"`
	ParentSyncID  string `json:"parent_sync_id,omitempty"`
	LeadAgentSync string `json:"lead_agent_sync_id,omitempty"`
	SortOrder     int    `json:"sort_order"`
}

type departmentAdapter struct{ baseAdapter }

func (departmentAdapter) kind() string { return syncwire.KindDepartment }

func (departmentAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &department_entity.Department{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindDepartment, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	parentSyncID := ""
	if row.ParentID > 0 {
		parent, ferr := department_repo.Department().Find(ctx, row.ParentID)
		if ferr != nil {
			return nil, ferr
		}
		if parent != nil {
			parentSyncID = syncIDOf(parent.SyncMeta)
		}
	}
	leadSyncID, err := agentSyncIDOfLocalID(ctx, row.LeadAgentID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(departmentPayload{
		Name:          row.Name,
		Description:   row.Description,
		Icon:          row.Icon,
		AccentColor:   row.AccentColor,
		ParentSyncID:  parentSyncID,
		LeadAgentSync: leadSyncID,
		SortOrder:     row.SortOrder,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID:    row.SyncID,
		UpdatedAt: row.Updatetime,
		Payload:   payload,
	}, nil
}

// refs 刻意**不**把部门负责人列为阻塞引用。
//
// 负责人必须是这个部门的成员（department_svc.Update 强制），所以那个 Agent 自己的
// 落地正等着这个部门（agentAdapter.refs 依赖 department）。两边都阻塞就是死锁：
// resolveRefs 让两行都进入站队列，replayDeferred 又只在「有一条落地成功」时才继续，
// 于是八轮全空转、两行躺满 30 天后被当成「引用丢失」丢掉——接收端一个部门都收不到，
// 连带收不到部门里的每个 Agent。守卫见 adapter_refcycle_test.go。
//
// 负责人改由 apply 自己解析：解析不出就先把部门落下去（打破环），再报 errRefMissing
// 让这一行留在入站队列，等负责人到达后的重放补上 lead_agent_id。
func (departmentAdapter) refs(in *inbound) []ref {
	var p departmentPayload
	_ = json.Unmarshal(in.Payload, &p)
	return []ref{
		{Kind: syncwire.KindDepartment, SyncID: p.ParentSyncID},
	}
}

func (departmentAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p departmentPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	row := &department_entity.Department{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindDepartment, in.SyncID, row)
	if err != nil {
		return err
	}
	// 负责人是非阻塞引用（见 refs 的注释）：解析不出时这一轮先落 0，本行照常写下去。
	leadID := int64(0)
	if p.LeadAgentSync != "" {
		if leadID, err = syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindAgent, p.LeadAgentSync); err != nil {
			return err
		}
	}
	row.Name, row.Description, row.Icon = p.Name, p.Description, p.Icon
	row.AccentColor, row.SortOrder = p.AccentColor, p.SortOrder
	row.ParentID = resolvedID(resolved, ref{Kind: syncwire.KindDepartment, SyncID: p.ParentSyncID})
	row.LeadAgentID = leadID
	row.Status = consts.ACTIVE
	if !found {
		row.SyncID = in.SyncID
		if err := department_repo.Department().Create(ctx, row); err != nil {
			return err
		}
	} else if err := department_repo.Department().Update(ctx, row); err != nil {
		return err
	}
	if p.LeadAgentSync != "" && leadID == 0 {
		// 部门已经落地（环就此打破，负责人那一行这一轮即可落地），但 lead_agent_id
		// 还空着：报 errRefMissing 把本行留在入站队列，由随后的重放补齐。
		return errRefMissing
	}
	return nil
}

func (departmentAdapter) remove(ctx context.Context, in *inbound) error {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindDepartment, in.SyncID)
	if err != nil || id == 0 {
		return err
	}
	return department_repo.Department().Delete(ctx, id)
}

// ── Agent ───────────────────────────────────────────────────────────────────

// agentPayload 里只有 avatar_hash：头像按内容哈希单独走一条路（R16a），正文一律
// 不进同步载荷（守卫见 syncwire.GuardPayload）。也没有 skills_json —— 技能授权
// 下沉到执行目标行（R15e）。
type agentPayload struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	AvatarColor       string `json:"avatar_color"`
	AvatarIcon        string `json:"avatar_icon"`
	AvatarHash        string `json:"avatar_hash,omitempty"`
	SystemBadge       string `json:"system_badge"`
	DepartmentSyncID  string `json:"department_sync_id,omitempty"`
	ParentAgentSyncID string `json:"parent_agent_sync_id,omitempty"`
	SortOrder         int    `json:"sort_order"`
	PromptJSON        string `json:"prompt_json"`
	ToolsJSON         string `json:"tools_json"`
	Pinned            bool   `json:"pinned"`
}

// agentAdapter 的 avatar 字段是头像内容存取的窄接口（R16a）；nil 时（单机模式、
// 或测试直接构造 agentAdapter{}）优雅退化，见 avatarTransport 的文档。
type agentAdapter struct {
	avatar avatarTransport
	// uploaded 记下已经推给对端的头像哈希，同一份正文只推一次（R16a）。
	uploaded avatarUploaded
}

func (*agentAdapter) kind() string { return syncwire.KindAgent }

func (a *agentAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &agent_entity.Agent{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindAgent, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	deptSyncID := ""
	if row.DepartmentID > 0 {
		dept, ferr := department_repo.Department().Find(ctx, row.DepartmentID)
		if ferr != nil {
			return nil, ferr
		}
		if dept != nil {
			deptSyncID = syncIDOf(dept.SyncMeta)
		}
	}
	parentSyncID, err := agentSyncIDOfLocalID(ctx, row.ParentAgentID)
	if err != nil {
		return nil, err
	}
	hash := avatarHash(row.AvatarDataURL)
	if hash != "" {
		a.putAvatarBestEffort(ctx, syncID, hash, row.AvatarDataURL)
	}
	payload, err := json.Marshal(agentPayload{
		Name:              row.Name,
		Description:       row.Description,
		AvatarColor:       row.AvatarColor,
		AvatarIcon:        row.AvatarIcon,
		AvatarHash:        hash,
		SystemBadge:       row.SystemBadge,
		DepartmentSyncID:  deptSyncID,
		ParentAgentSyncID: parentSyncID,
		SortOrder:         row.SortOrder,
		PromptJSON:        row.PromptJSON,
		ToolsJSON:         row.ToolsJSON,
		Pinned:            row.Pinned,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID:    row.SyncID,
		UpdatedAt: row.Updatetime,
		Payload:   payload,
	}, nil
}

// putAvatarBestEffort 把本机持有的头像正文按内容哈希推给对端（server）。上传
// 失败不影响 Agent 本身照常同步——load 仍然返回一份带 AvatarHash 的合法载荷，
// 失败只记日志（R16a：头像取不到或传输失败时该 Agent 照常同步）。
func (a *agentAdapter) putAvatarBestEffort(ctx context.Context, syncID, hash, dataURL string) {
	if a.avatar == nil {
		return
	}
	// 同一份正文只推一次（R16a）：对端已经持有这个哈希，再推一遍只是白白重发几 MB。
	if a.uploaded.mark(hash) {
		return
	}
	if err := a.avatar.PutAvatar(ctx, hash, avatarContentType(dataURL), dataURL); err != nil {
		// 没推成功就不算推过：撤销标记，下一轮同步再试（不阻塞本次 Agent 上行）。
		a.uploaded.forget(hash)
		logger.Ctx(ctx).Debug("sync_svc.agentAdapter: avatar upload failed, agent still syncs",
			zap.String("syncId", syncID), zap.Error(err))
	}
}

func (*agentAdapter) refs(in *inbound) []ref {
	var p agentPayload
	_ = json.Unmarshal(in.Payload, &p)
	return []ref{
		{Kind: syncwire.KindDepartment, SyncID: p.DepartmentSyncID},
		{Kind: syncwire.KindAgent, SyncID: p.ParentAgentSyncID},
	}
}

func (a *agentAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p agentPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	row := &agent_entity.Agent{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindAgent, in.SyncID, row)
	if err != nil {
		return err
	}
	row.Name, row.Description = p.Name, p.Description
	row.AvatarColor, row.AvatarIcon, row.SystemBadge = p.AvatarColor, p.AvatarIcon, p.SystemBadge
	row.AvatarDataURL = a.resolveAvatarDataURL(ctx, in.SyncID, row.AvatarDataURL, p.AvatarHash)
	row.DepartmentID = resolvedID(resolved, ref{Kind: syncwire.KindDepartment, SyncID: p.DepartmentSyncID})
	row.ParentAgentID = resolvedID(resolved, ref{Kind: syncwire.KindAgent, SyncID: p.ParentAgentSyncID})
	row.SortOrder, row.PromptJSON, row.ToolsJSON, row.Pinned = p.SortOrder, p.PromptJSON, p.ToolsJSON, p.Pinned
	row.Status = consts.ACTIVE
	if !found {
		// 技能授权不在载荷里：它住在执行目标行上，作为独立的同步对象落地。
		row.SyncID = in.SyncID
		row.AgentBackendID = 0
		return agent_repo.Agent().Create(ctx, row)
	}
	// UpdateRow 只写 Agent 这一行：执行目标是独立的同步对象，不能被 Agent 行的
	// 派生字段重写掉（agent_repo.Update 会那么做）。
	return agent_repo.Agent().UpdateRow(ctx, row)
}

// resolveAvatarDataURL 落实 R16a 的下行一侧：
//   - 载荷没有哈希 → 没有自定义头像，清空。
//   - 本机现有内容已经是这份哈希 → 已经持有，不重新取一次正文。
//   - 否则调 avatar.GetAvatar 取一次；取不到（没装配 / 网络失败 / 超时）或者取回来的
//     正文哈希对不上，就**保留本机已有的那份**，不阻塞这一行落地，也不在这次 apply 里
//     反复重试——下一次这份哈希再出现时才会再试一次。本机原本就没有头像时留空，
//     退回 AgentAvatar 的 initials 占位字母头像。
//
// 取不到时保留旧图而不是抹空：抹空是净损失（本机那张图是好的），而且 apply 成功后
// SaveMeta 会把版本推到最新，这一条再也不会重投，用户从此看到占位字母头像。
func (a *agentAdapter) resolveAvatarDataURL(ctx context.Context, syncID, existing, hash string) string {
	if hash == "" {
		return ""
	}
	if existing != "" && avatarHash(existing) == hash {
		return existing
	}
	if a.avatar == nil {
		return existing
	}
	content, _, err := a.avatar.GetAvatar(ctx, hash)
	if err != nil || content == "" {
		logger.Ctx(ctx).Debug("sync_svc.agentAdapter: avatar fetch failed, keeping the avatar already held",
			zap.String("syncId", syncID), zap.Error(err))
		return existing
	}
	// 内容寻址的正文必须验哈希：不验就等于让服务端决定这个 Agent 的头像是什么。
	if avatarHash(content) != hash {
		logger.Ctx(ctx).Warn("sync_svc.agentAdapter: avatar content does not match its hash, discarded",
			zap.String("syncId", syncID))
		return existing
	}
	return content
}

func (*agentAdapter) remove(ctx context.Context, in *inbound) error {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindAgent, in.SyncID)
	if err != nil || id == 0 {
		return err
	}
	return agent_repo.Agent().Delete(ctx, id)
}

// dependents Agent 的执行目标列表跟着 Agent 的写入路径变化（agent_repo 在同一个
// 事务里落两张表），因此 Agent 一有改动就把它当前的目标行一并入队。
func (*agentAdapter) dependents(ctx context.Context, syncID string) ([]relatedRow, error) {
	return agentExecTargetRows(ctx, syncID)
}

// children 删 Agent 时它的成员关系与执行目标列表项一并落墓碑（R6）。
func (*agentAdapter) children(ctx context.Context, syncID string) ([]relatedRow, error) {
	out, err := agentExecTargetRows(ctx, syncID)
	if err != nil {
		return nil, err
	}
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindAgent, syncID)
	if err != nil || id == 0 {
		return out, err
	}
	members, err := project_repo.ProjectAgent().ListByAgent(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, row := range members {
		out = append(out, relatedRow{
			Kind: syncwire.KindProjectAgent, SyncID: row.SyncID, Version: row.SyncVersion,
		})
	}
	return out, nil
}

func agentExecTargetRows(ctx context.Context, agentSyncID string) ([]relatedRow, error) {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindAgent, agentSyncID)
	if err != nil || id == 0 {
		return nil, err
	}
	targets, err := agent_repo.AgentExecTarget().ListByAgent(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]relatedRow, 0, len(targets))
	for _, t := range targets {
		out = append(out, relatedRow{
			Kind: syncwire.KindAgentExecTarget, LocalID: t.ID,
			SyncID: t.SyncID, Version: t.SyncVersion,
		})
	}
	return out, nil
}

// agentSyncIDOfLocalID 取某个 Agent 的同步标识；id <= 0 或行已不在时返回空串。
func agentSyncIDOfLocalID(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", nil
	}
	row, err := agent_repo.Agent().Find(ctx, id)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", nil
	}
	return syncIDOf(row.SyncMeta), nil
}

// ── backend ─────────────────────────────────────────────────────────────────

// agentBackendPayload is account identity only. Provider configuration travels as
// its own llm_provider object; CLI paths are a per-device overlay and never
// appear here. The device fingerprint is likewise never a payload key — it
// travels on the outbound/push item's own agentred_fingerprint column instead
// (同步与身份: the backend's device is part of account-level sync, not a
// payload field).
type agentBackendPayload struct {
	Type                  string `json:"type"`
	Name                  string `json:"name"`
	ProviderKey           string `json:"provider_key"`
	ModelKey              string `json:"model_key"`
	ModelRoutes           string `json:"model_routes"`
	Sandbox               string `json:"sandbox"`
	Approval              string `json:"approval"`
	EnvJSON               string `json:"env_json"`
	ReasoningEffort       string `json:"reasoning_effort"`
	DefaultPermissionMode string `json:"default_permission_mode"`
	DefaultModel          string `json:"default_model"`
	OpenClawGatewayURL    string `json:"openclaw_gateway_url"`
	OpenClawAgentID       string `json:"openclaw_agent_id"`
	OpenClawDefaultModel  string `json:"openclaw_default_model"`
	OpenClawSessionMode   string `json:"openclaw_session_mode"`
}

type agentBackendAdapter struct{ baseAdapter }

func (agentBackendAdapter) kind() string { return syncwire.KindAgentBackend }

func (agentBackendAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &agent_backend_entity.AgentBackend{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindAgentBackend, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	payload, err := json.Marshal(agentBackendPayload{
		Type:                  row.Type,
		Name:                  row.Name,
		ProviderKey:           row.LLMProviderKey,
		ModelKey:              row.LLMModelKey,
		ModelRoutes:           row.ModelRoutes,
		Sandbox:               row.Sandbox,
		Approval:              row.Approval,
		EnvJSON:               row.EnvJSON,
		ReasoningEffort:       row.ReasoningEffort,
		DefaultPermissionMode: row.DefaultPermissionMode,
		DefaultModel:          row.DefaultModel,
		OpenClawGatewayURL:    row.OpenClawGatewayURL,
		OpenClawAgentID:       row.OpenClawAgentID,
		OpenClawDefaultModel:  row.OpenClawDefaultModel,
		OpenClawSessionMode:   row.OpenClawSessionMode,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID:              row.SyncID,
		UpdatedAt:           row.Updatetime,
		AgentredFingerprint: row.DeviceID,
		Payload:             payload,
	}, nil
}

// Backend identity's machine reference (DeviceID) travels via the outbound's
// fingerprint column, not as a blocking ref: there is no separate device
// object to resolve here, and a missing CLI overlay is PATH on that
// installation while the account identity remains fully usable everywhere.
func (agentBackendAdapter) refs(*inbound) []ref { return nil }

func (agentBackendAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p agentBackendPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	row := &agent_backend_entity.AgentBackend{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindAgentBackend, in.SyncID, row)
	if err != nil {
		return err
	}
	row.Type, row.Name, row.LLMProviderKey = p.Type, p.Name, p.ProviderKey
	row.LLMModelKey = p.ModelKey
	// The backend's device is account-level sync identity (同步与身份): it
	// travels on the push item's own agentred_fingerprint column and lands
	// straight on DeviceID, so a backend set up on one desktop points at the
	// same machine on every other end and on the server. cli_path stays a
	// per-device overlay applied separately by agentBackendCLIAdapter.
	row.DeviceID = in.AgentredFingerprint
	row.ModelRoutes = p.ModelRoutes
	row.Sandbox, row.Approval, row.EnvJSON = p.Sandbox, p.Approval, p.EnvJSON
	row.ReasoningEffort = p.ReasoningEffort
	row.DefaultPermissionMode, row.DefaultModel = p.DefaultPermissionMode, p.DefaultModel
	row.OpenClawGatewayURL, row.OpenClawAgentID = p.OpenClawGatewayURL, p.OpenClawAgentID
	row.OpenClawDefaultModel, row.OpenClawSessionMode = p.OpenClawDefaultModel, p.OpenClawSessionMode
	row.Status = consts.ACTIVE
	if !found {
		row.SyncID = in.SyncID
		return agent_backend_repo.AgentBackend().Create(ctx, row)
	}
	return agent_backend_repo.AgentBackend().Update(ctx, row)
}

func (agentBackendAdapter) remove(ctx context.Context, in *inbound) error {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindAgentBackend, in.SyncID)
	if err != nil || id == 0 {
		return err
	}
	return agent_backend_repo.AgentBackend().Delete(ctx, id)
}

// children 删 backend 时引用它的执行目标项落墓碑，Agent 本身不删——它可能还有
// 别的档，全没了才按 R15 提示没有可用机器（R6）。
func (agentBackendAdapter) children(ctx context.Context, syncID string) ([]relatedRow, error) {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindAgentBackend, syncID)
	if err != nil || id == 0 {
		return nil, err
	}
	targets, err := agent_repo.AgentExecTarget().ListByBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]relatedRow, 0, len(targets))
	for _, t := range targets {
		out = append(out, relatedRow{
			Kind: syncwire.KindAgentExecTarget, LocalID: t.ID,
			SyncID: t.SyncID, Version: t.SyncVersion,
		})
	}
	return out, nil
}

// ── 执行目标 ────────────────────────────────────────────────────────────────

type agentExecTargetPayload struct {
	AgentSyncID   string `json:"agent_sync_id"`
	BackendSyncID string `json:"backend_sync_id"`
	SortOrder     int    `json:"sort_order"`
	SkillsJSON    string `json:"skills_json"`
}

type agentExecTargetAdapter struct{ baseAdapter }

func (agentExecTargetAdapter) kind() string { return syncwire.KindAgentExecTarget }

func (agentExecTargetAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &agent_entity.AgentExecTarget{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindAgentExecTarget, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	agentSyncID, err := agentSyncIDOfLocalID(ctx, row.AgentID)
	if err != nil {
		return nil, err
	}
	backend, err := agent_backend_repo.AgentBackend().Find(ctx, row.AgentBackendID)
	if err != nil {
		return nil, err
	}
	if agentSyncID == "" || backend == nil {
		return nil, nil
	}
	payload, err := json.Marshal(agentExecTargetPayload{
		AgentSyncID:   agentSyncID,
		BackendSyncID: syncIDOf(backend.SyncMeta),
		SortOrder:     row.SortOrder,
		SkillsJSON:    row.SkillsJSON,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID:    row.SyncID,
		UpdatedAt: row.SyncUpdatedAt,
		Payload:   payload,
	}, nil
}

func (agentExecTargetAdapter) refs(in *inbound) []ref {
	var p agentExecTargetPayload
	_ = json.Unmarshal(in.Payload, &p)
	return []ref{
		{Kind: syncwire.KindAgent, SyncID: p.AgentSyncID},
		{Kind: syncwire.KindAgentBackend, SyncID: p.BackendSyncID},
	}
}

func (agentExecTargetAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p agentExecTargetPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	agentID := resolvedID(resolved, ref{Kind: syncwire.KindAgent, SyncID: p.AgentSyncID})
	backendID := resolvedID(resolved, ref{Kind: syncwire.KindAgentBackend, SyncID: p.BackendSyncID})
	if agentID == 0 || backendID == 0 {
		return errRefMissing
	}
	row := &agent_entity.AgentExecTarget{
		AgentID: agentID, AgentBackendID: backendID,
		SortOrder: p.SortOrder, SkillsJSON: p.SkillsJSON,
	}
	row.SyncID = in.SyncID
	return agent_repo.AgentExecTarget().UpsertFromSync(ctx, row)
}

func (agentExecTargetAdapter) remove(ctx context.Context, in *inbound) error {
	return agent_repo.AgentExecTarget().DeleteBySyncID(ctx, in.SyncID)
}
