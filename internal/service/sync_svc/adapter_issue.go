package sync_svc

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
)

// 看板的三个适配器（规格 2026-08-27-issues-board-project-scope「数据与迁移 ›
// 同步（跨仓）」）。任务随账号走：标签目录、任务本身与它们之间的关联各是一个
// 账号级同步对象，引用方向是 label ← issue_label → issue。

// ── 标签目录 ────────────────────────────────────────────────────────────────

// labelPayload 是标签的同步载荷。
//
// status 在载荷里而不像别的对象那样只靠墓碑表达：server 没有本地行，它的 /issues
// 读路径是把 sync_objects 的载荷直接拼成响应，没有这一列就判不出一个标签还在不在。
// 本机落地时**不**读它——存活 / 墓碑由下行项的 DeletedAt 表达（决策 20），一条活着
// 的下行项按定义就是活的，与每个兄弟适配器同一口径。
type labelPayload struct {
	Name   string `json:"name"`
	Tone   string `json:"tone"`
	Status int    `json:"status"`
}

type labelAdapter struct{ baseAdapter }

func (labelAdapter) kind() string { return syncwire.KindLabel }

func (labelAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &issue_entity.Label{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindLabel, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	payload, err := json.Marshal(labelPayload{
		Name:   row.Name,
		Tone:   row.Tone,
		Status: row.Status,
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

// refs 标签不引用任何别的对象：它是目录的根，任务与关联行都指向它。
func (labelAdapter) refs(*inbound) []ref { return nil }

func (labelAdapter) apply(ctx context.Context, in *inbound, _ map[string]int64) error {
	var p labelPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	row := &issue_entity.Label{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindLabel, in.SyncID, row)
	if err != nil {
		return err
	}
	if !found {
		// 两端各自建了同名标签，带着两个不同的同步标识落在同一个自然键上：本机那
		// 一行还占着 uniq_labels_name_active，硬插会撞唯一索引抛一个原始的 SQLite
		// 错误，把这一行推进暂缓队列后每一轮重放都再撞一次。名字就是身份——接管
		// 本机那一行，让它跟随账号里胜出的同步标识，不为同一件事再插一行（兄弟
		// 适配器 projectAgentAdapter / projectLocationAdapter 早就这么处理同类冲突）。
		//
		// 内置的五个种子标签不走这条路：它们的标识按名字确定性派生
		// （issue_entity.SeedLabelSyncID），两端天然收敛成同一个对象。
		held, ferr := issue_repo.Label().FindByName(ctx, p.Name)
		if ferr != nil {
			return ferr
		}
		if held != nil {
			row = held
			found = true
		}
	}
	row.Name, row.Tone = p.Name, p.Tone
	// 新建的行落成存活。已经在本机软删的行，下面那条 Update 的 WHERE 里带着
	// status = ACTIVE，命中 0 行——这不是漏洞：本机的软删自己会以墓碑上行，server
	// 从此把这个对象当墓碑，绝不会再发一条活着的下行项过来（删除不复活，R6）。
	row.Status = consts.ACTIVE
	row.SyncID = in.SyncID
	if !found {
		return issue_repo.Label().Create(ctx, row)
	}
	return issue_repo.Label().Update(ctx, row)
}

// remove 墓碑到达（R6）：标签软删，并从全部任务上摘掉——留着关联行就等于在本机
// 留下一串指向已消失标签的悬空引用，本地删除路径（issue_svc）也是这两步。
func (labelAdapter) remove(ctx context.Context, in *inbound) error {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindLabel, in.SyncID)
	if err != nil || id == 0 {
		return err
	}
	if err := issue_repo.Label().Delete(ctx, id); err != nil {
		return err
	}
	return issue_repo.IssueLabel().DeleteByLabel(ctx, id)
}

// ── 任务 ────────────────────────────────────────────────────────────────────

// issuePayload 是任务的同步载荷。
//
// 载荷里**没有**运行态：agent_status、session_id 与 source 是这台机器上这一轮跑成
// 什么样，跨机没有意义，也不该让另一台机器的界面显示一个它并不持有的会话。也没有
// state —— 状态轴本轮消失，它完全由 stage 推导（stage=done 即已完成）。
//
// 执行归属的三个字段里，Agent 与机器是账号级对象，用同步标识表达；provider / model
// 本来就是稳定的字符串键（决策 6 里 llm_providers 整表不出本机，跨机只传 key）。
type issuePayload struct {
	Title              string  `json:"title"`
	Description        string  `json:"description"`
	Stage              string  `json:"stage"`
	Position           float64 `json:"position"`
	ProjectSyncID      string  `json:"project_sync_id,omitempty"`
	AgentSyncID        string  `json:"agent_sync_id,omitempty"`
	AgentBackendSyncID string  `json:"agent_backend_sync_id,omitempty"`
	LLMProviderKey     string  `json:"llm_provider_key"`
	LLMModelKey        string  `json:"llm_model_key"`
	ClosedAt           int64   `json:"closed_at"`
}

type issueAdapter struct{ baseAdapter }

func (issueAdapter) kind() string { return syncwire.KindIssue }

func (issueAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &issue_entity.Issue{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindIssue, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	projectSyncID := ""
	if row.ProjectID > 0 {
		project, ferr := project_repo.Project().Find(ctx, row.ProjectID)
		if ferr != nil {
			return nil, ferr
		}
		if project != nil {
			projectSyncID = syncIDOf(project.SyncMeta)
		}
	}
	agentSyncID, err := agentSyncIDOfLocalID(ctx, row.AssigneeAgentID)
	if err != nil {
		return nil, err
	}
	backendSyncID := ""
	if row.AgentBackendID > 0 {
		backend, ferr := agent_backend_repo.AgentBackend().Find(ctx, row.AgentBackendID)
		if ferr != nil {
			return nil, ferr
		}
		if backend != nil {
			backendSyncID = syncIDOf(backend.SyncMeta)
		}
	}
	payload, err := json.Marshal(issuePayload{
		Title:              row.Title,
		Description:        row.Body,
		Stage:              row.Stage,
		Position:           row.Position,
		ProjectSyncID:      projectSyncID,
		AgentSyncID:        agentSyncID,
		AgentBackendSyncID: backendSyncID,
		LLMProviderKey:     row.LLMProviderKey,
		LLMModelKey:        row.LLMModelKey,
		ClosedAt:           row.ClosedAt,
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

// refs 三个引用都是阻塞的（R2a）：任务落地前项目 / Agent / 机器必须都已经在本机
// 有行，否则写下去就是三个悬空引用。空引用（未归属项目、没指定执行归属）不参与
// 解析，resolveRefs 直接跳过。
func (issueAdapter) refs(in *inbound) []ref {
	var p issuePayload
	_ = json.Unmarshal(in.Payload, &p)
	return []ref{
		{Kind: syncwire.KindProject, SyncID: p.ProjectSyncID},
		{Kind: syncwire.KindAgent, SyncID: p.AgentSyncID},
		{Kind: syncwire.KindAgentBackend, SyncID: p.AgentBackendSyncID},
	}
}

func (issueAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p issuePayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	row := &issue_entity.Issue{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindIssue, in.SyncID, row)
	if err != nil {
		return err
	}
	row.Title, row.Body = p.Title, p.Description
	row.Stage, row.Position = p.Stage, p.Position
	row.ProjectID = resolvedID(resolved, ref{Kind: syncwire.KindProject, SyncID: p.ProjectSyncID})
	row.AssigneeAgentID = resolvedID(resolved, ref{Kind: syncwire.KindAgent, SyncID: p.AgentSyncID})
	row.AgentBackendID = resolvedID(resolved,
		ref{Kind: syncwire.KindAgentBackend, SyncID: p.AgentBackendSyncID})
	row.LLMProviderKey, row.LLMModelKey = p.LLMProviderKey, p.LLMModelKey
	// 状态轴消失之后 state 只是 stage 的投影，两端各自推导即可，不进载荷。
	row.State = issue_entity.StateOpen
	if p.Stage == issue_entity.StageDone {
		row.State = issue_entity.StateClosed
	}
	row.ClosedAt = p.ClosedAt
	// 与标签同理：软删的行由它自己的墓碑上行表达，server 不会再发活着的下行项。
	row.Status = consts.ACTIVE
	if !found {
		// 同步进来的任务在本机没有会话，运行态从零起（这台机器还没跑过它）。
		row.SyncID = in.SyncID
		row.SessionID = 0
		row.AgentStatus = issue_entity.AgentStatusIdle
		row.Source = issue_entity.SourceManual
		return issue_repo.Issue().Create(ctx, row)
	}
	// 已有的行：session_id 与 agent_status 是本机独有状态，一律不动。
	return issue_repo.Issue().Update(ctx, row)
}

func (issueAdapter) remove(ctx context.Context, in *inbound) error {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindIssue, in.SyncID)
	if err != nil || id == 0 {
		return err
	}
	return issue_repo.Issue().Delete(ctx, id)
}

// ── 任务 ↔ 标签 ─────────────────────────────────────────────────────────────

// issueLabelPayload 两端都用同步标识表达：关联表的主键是 (issue_id, label_id) 两个
// 本地自增值，在另一台机器上指向完全不同的两行。
type issueLabelPayload struct {
	IssueSyncID string `json:"issue_sync_id"`
	LabelSyncID string `json:"label_sync_id"`
}

type issueLabelAdapter struct{ baseAdapter }

func (issueLabelAdapter) kind() string { return syncwire.KindIssueLabel }

func (issueLabelAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &issue_entity.IssueLabel{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindIssueLabel, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	issue, err := issue_repo.Issue().Find(ctx, row.IssueID)
	if err != nil {
		return nil, err
	}
	label, err := issue_repo.Label().Find(ctx, row.LabelID)
	if err != nil {
		return nil, err
	}
	if issue == nil || label == nil {
		// 两端之一在本机已经不存在：这条关联没有可表达的跨机引用，交给它自己的
		// 删除路径去落墓碑，这里不发一条带空引用的上行（与 projectAgentAdapter
		// 同一口径）。
		return nil, nil
	}
	payload, err := json.Marshal(issueLabelPayload{
		IssueSyncID: syncIDOf(issue.SyncMeta),
		LabelSyncID: syncIDOf(label.SyncMeta),
	})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID: row.SyncID,
		// 关联行没有自己的业务时间列：它要么在、要么不在，改动只有增删两种。
		UpdatedAt: row.SyncUpdatedAt,
		Payload:   payload,
	}, nil
}

func (issueLabelAdapter) refs(in *inbound) []ref {
	var p issueLabelPayload
	_ = json.Unmarshal(in.Payload, &p)
	return []ref{
		{Kind: syncwire.KindIssue, SyncID: p.IssueSyncID},
		{Kind: syncwire.KindLabel, SyncID: p.LabelSyncID},
	}
}

func (issueLabelAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p issueLabelPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	issueID := resolvedID(resolved, ref{Kind: syncwire.KindIssue, SyncID: p.IssueSyncID})
	labelID := resolvedID(resolved, ref{Kind: syncwire.KindLabel, SyncID: p.LabelSyncID})
	if issueID == 0 || labelID == 0 {
		return errRefMissing
	}
	row := &issue_entity.IssueLabel{IssueID: issueID, LabelID: labelID}
	row.SyncID = in.SyncID
	return issue_repo.IssueLabel().UpsertFromSync(ctx, row)
}

// remove 关联表是硬删（没有 status 软删列），联合主键也指认不了它——按同步标识删。
func (issueLabelAdapter) remove(ctx context.Context, in *inbound) error {
	return issue_repo.IssueLabel().DeleteBySyncID(ctx, in.SyncID)
}
