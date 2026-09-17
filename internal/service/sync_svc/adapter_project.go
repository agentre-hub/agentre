package sync_svc

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// ── 项目 ────────────────────────────────────────────────────────────────────

type projectAdapter struct{ baseAdapter }

func (projectAdapter) kind() string { return syncwire.KindProject }

func (projectAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &project_entity.Project{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindProject, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	parentSyncID := ""
	if row.ParentID > 0 {
		parent, ferr := project_repo.Project().Find(ctx, row.ParentID)
		if ferr != nil {
			return nil, ferr
		}
		if parent != nil {
			parentSyncID = syncIDOf(parent.SyncMeta)
		}
	}
	payload, err := json.Marshal(syncwire.ProjectPayload{
		Name:         row.Name,
		Icon:         row.Icon,
		Color:        row.Color,
		Description:  row.Description,
		ParentSyncID: parentSyncID,
		SortOrder:    row.SortOrder,
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

func (projectAdapter) refs(in *inbound) []ref {
	var p syncwire.ProjectPayload
	_ = json.Unmarshal(in.Payload, &p)
	return []ref{{Kind: syncwire.KindProject, SyncID: p.ParentSyncID}}
}

func (projectAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p syncwire.ProjectPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	parentID := resolvedID(resolved, ref{Kind: syncwire.KindProject, SyncID: p.ParentSyncID})

	row := &project_entity.Project{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindProject, in.SyncID, row)
	if err != nil {
		return err
	}
	row.Name, row.Icon, row.Color = p.Name, p.Icon, p.Color
	row.Description, row.ParentID, row.SortOrder = p.Description, parentID, p.SortOrder
	row.Status = consts.ACTIVE
	if !found {
		// 同步进来的项目不带源端本机路径，落成「本机未配置路径」（R10、决策 21）。
		row.SyncID, row.Path, row.LocalPathMissing = in.SyncID, "", true
		return project_repo.Project().Create(ctx, row)
	}
	// 已有的行：path / local_path_missing 是本机独有状态，一律不动。
	return project_repo.Project().Update(ctx, row)
}

func (projectAdapter) remove(ctx context.Context, in *inbound) error {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindProject, in.SyncID)
	if err != nil || id == 0 {
		return err
	}
	return project_repo.Project().Delete(ctx, id)
}

// children 删项目时它名下的路径记录与成员关系一并落墓碑（R6）。
func (projectAdapter) children(ctx context.Context, syncID string) ([]relatedRow, error) {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindProject, syncID)
	if err != nil || id == 0 {
		return nil, err
	}
	return projectRefHolders(ctx, id)
}

// dependentsOnClaim 项目刚拿到身份（R12a 认领）时，把**引用它的那些行**一并重发一次：
// 成员关系、它在各台 agentred 上的路径记录，以及**它的子项目**。
//
// 它们在这之前上行时，项目还没有同步标识，引用只能写成空串：成员关系在 server 上
// 是一条不属于任何项目的孤儿行（web 控制台按引用归集成员，孤儿行一个都不显示——
// 「这个项目还没有成员」），路径记录的自然键则退化成空作用域（同指纹的第二条会被
// 自然键合并掉），而子项目的 `parent_sync_id` 一空就在 server 上被落成了**顶层项目**
// ——控制台的项目树里那一层父子关系整个消失，挂在父项目上的成员也就继承不到子项目。
//
// 而它们在本机已经同步过（版本非 0），不会再自己上行，其中 project_agent 还早就
// 属于这个账号、不是认领的对象：认领是它们唯一能被重发的机会。
func (projectAdapter) dependentsOnClaim(ctx context.Context, syncID string) ([]relatedRow, error) {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindProject, syncID)
	if err != nil || id == 0 {
		return nil, err
	}
	out, err := projectRefHolders(ctx, id)
	if err != nil {
		return nil, err
	}
	children, err := project_repo.Project().ListByParent(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, row := range children {
		out = append(out, relatedRow{
			Kind: syncwire.KindProject, LocalID: row.ID,
			SyncID: row.SyncID, Version: row.SyncVersion,
		})
	}
	return out, nil
}

// projectRefHolders 报出挂在某个项目下的两样东西：它的路径记录与成员关系。
// 删项目时它们跟着落墓碑（R6），项目第一次拿到身份时它们跟着重发（R2）。
//
// 与 dependents 不是一回事：那一条是「只跟着本行的写入路径变化」的行（Agent 的
// 执行目标）。成员关系与路径记录各有自己的写入路径，平时不随项目一起上行。
func projectRefHolders(ctx context.Context, projectID int64) ([]relatedRow, error) {
	out := make([]relatedRow, 0, 4)
	locations, err := project_location_repo.ProjectLocation().ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, row := range locations {
		out = append(out, relatedRow{
			Kind: syncwire.KindProjectLocation, LocalID: row.ID,
			SyncID: row.SyncID, Version: row.SyncVersion,
		})
	}
	members, err := project_repo.ProjectAgent().ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, row := range members {
		out = append(out, relatedRow{
			Kind: syncwire.KindProjectAgent, LocalID: row.ID,
			SyncID: row.SyncID, Version: row.SyncVersion,
		})
	}
	return out, nil
}

// ── 成员关系 ────────────────────────────────────────────────────────────────

type projectAgentAdapter struct{ baseAdapter }

func (projectAgentAdapter) kind() string { return syncwire.KindProjectAgent }

func (projectAgentAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &project_entity.ProjectAgent{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindProjectAgent, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	project, err := project_repo.Project().Find(ctx, row.ProjectID)
	if err != nil {
		return nil, err
	}
	agent, err := agentSyncIDOfLocalID(ctx, row.AgentID)
	if err != nil {
		return nil, err
	}
	// 两端之一在本机**表达不出跨机引用**就算数：行不在了，或行还在、却还没有同步
	// 标识（R12a 的认领失效时，本机的项目行就盖着空标识）。照发是一条引用为空串的
	// 成员关系——它在 server 上不属于任何项目、也不会再被认领（它已经是那个账号的
	// 行），只会在那边占着一个标识。得等这一行拿到标识（dependentsOnClaim 那时会
	// 把它重发一次），这里一条都不发。
	if project == nil || syncIDOf(project.SyncMeta) == "" || agent == "" {
		return nil, nil
	}
	payload, err := json.Marshal(syncwire.ProjectAgentPayload{
		ProjectSyncID: syncIDOf(project.SyncMeta),
		AgentSyncID:   agent,
		JoinedAt:      row.JoinedAt,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID:    row.SyncID,
		UpdatedAt: row.JoinedAt,
		Payload:   payload,
	}, nil
}

func (projectAgentAdapter) refs(in *inbound) []ref {
	var p syncwire.ProjectAgentPayload
	_ = json.Unmarshal(in.Payload, &p)
	return []ref{
		{Kind: syncwire.KindProject, SyncID: p.ProjectSyncID},
		{Kind: syncwire.KindAgent, SyncID: p.AgentSyncID},
	}
}

func (projectAgentAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p syncwire.ProjectAgentPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	projectID := resolvedID(resolved, ref{Kind: syncwire.KindProject, SyncID: p.ProjectSyncID})
	agentID := resolvedID(resolved, ref{Kind: syncwire.KindAgent, SyncID: p.AgentSyncID})
	if projectID == 0 || agentID == 0 {
		return errRefMissing
	}
	existing := &project_entity.ProjectAgent{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindProjectAgent, in.SyncID, existing)
	if err != nil {
		return err
	}
	if found {
		// 成员关系没有可改的内容：它要么在、要么不在。
		return nil
	}
	// 两端各自把同一个 Agent 加进同一个项目时，会带着两个不同的同步标识落在同一个
	// (project_id, agent_id) 联合主键上。保留本机那一行、不为同一件事再插一行——
	// 硬插会撞主键，把整轮下行也一起带崩。
	pairs, err := project_repo.ProjectAgent().ListByProject(ctx, projectID)
	if err != nil {
		return err
	}
	for _, pair := range pairs {
		if pair.AgentID == agentID {
			return nil
		}
	}
	row := &project_entity.ProjectAgent{ProjectID: projectID, AgentID: agentID, JoinedAt: p.JoinedAt}
	row.SyncID = in.SyncID
	return project_repo.ProjectAgent().CreateFromSync(ctx, row)
}

func (projectAgentAdapter) remove(ctx context.Context, in *inbound) error {
	row := &project_entity.ProjectAgent{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindProjectAgent, in.SyncID, row)
	if err != nil || !found {
		return err
	}
	return project_repo.ProjectAgent().Remove(ctx, row.ProjectID, row.AgentID)
}

// ── agentred 上的项目路径 ───────────────────────────────────────────────────

type projectLocationAdapter struct{ baseAdapter }

func (projectLocationAdapter) kind() string { return syncwire.KindProjectLocation }

func (projectLocationAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &project_location_entity.ProjectLocation{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindProjectLocation, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	project, err := project_repo.Project().Find(ctx, row.ProjectID)
	if err != nil {
		return nil, err
	}
	// 项目行还在、却还没有同步标识时，这一行的**账号内自然键**（项目标识 × 指纹）
	// 只能退化成空作用域：server 那边空作用域的行会彼此碰撞（同指纹的第二条被自然键
	// 合并掉，落败的那一份还会进「没能同步的改动」），而它指的也不可能是任何一个
	// 项目。项目拿到标识之后由 dependentsOnClaim 重发。
	if project == nil || syncIDOf(project.SyncMeta) == "" || row.DeviceFingerprint == "" {
		return nil, nil
	}
	payload, err := json.Marshal(syncwire.ProjectLocationPayload{Path: row.Path})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID:              row.SyncID,
		UpdatedAt:           row.Updatetime,
		ScopeSyncID:         syncIDOf(project.SyncMeta),
		AgentredFingerprint: row.DeviceFingerprint,
		Payload:             payload,
	}, nil
}

// refs 路径记录只依赖项目。它指向的 agentred **不**作为引用要求本机已配对：
// R2b 明说本机没配对那台机器时这一行照常留着，只是不参与解析、不呈现——
// device_id 缓存留空就是那个状态（决策 26）。
func (projectLocationAdapter) refs(in *inbound) []ref {
	return []ref{{Kind: syncwire.KindProject, SyncID: in.ScopeSyncID}}
}

func (projectLocationAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p syncwire.ProjectLocationPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	projectID := resolvedID(resolved, ref{Kind: syncwire.KindProject, SyncID: in.ScopeSyncID})
	if projectID == 0 {
		return errRefMissing
	}
	row := &project_location_entity.ProjectLocation{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindProjectLocation, in.SyncID, row)
	if err != nil {
		return err
	}
	// device_id 是「由指纹解析出的本地缓存」（决策 26），**落地当场就解析**：本机
	// 配对了那台 agentred 就填上，没配对留空（R2b，行与 path 照常留着，只是不参与
	// 解析、不呈现）。留给「用户点开位置页签时再回填」是不够的——决策 34 的逐档
	// 可用性判定与两个 cwd 解析点都按 device_id 查行，缓存空着它们一律判成「这台
	// 机器上没配这个项目的路径」。
	localID, err := localIDOfFingerprint(ctx, in.AgentredFingerprint)
	if err != nil {
		return err
	}
	deviceID := ""
	if localID > 0 {
		deviceID = strconv.FormatInt(localID, 10)
	}
	if !found {
		// 两端各自为同一个（项目, 指纹）建了一行，带着不同的同步标识落在同一个自然
		// 键上（R4b）。本机那一行还占着 uniq_project_locations_proj_fingerprint，
		// 硬插会撞唯一索引抛一个原始的 SQLite 错误——偏偏这正是合并落败方那一类行。
		//
		// 自然键就是身份：接管本机那一行，让它跟随账号里胜出的那个同步标识，不为
		// 同一件事再插一行。兄弟适配器 projectAgentAdapter 早就这么处理同类冲突。
		held, ferr := findLocationAtNaturalKey(ctx, projectID, in.AgentredFingerprint)
		if ferr != nil {
			return ferr
		}
		if held != nil {
			row = held
			found = true
		}
	}
	row.ProjectID, row.Path = projectID, p.Path
	row.DeviceFingerprint = in.AgentredFingerprint
	row.DeviceID = deviceID
	row.Status = consts.ACTIVE
	row.SyncID = in.SyncID
	if !found {
		return project_location_repo.ProjectLocation().Create(ctx, row)
	}
	return project_location_repo.ProjectLocation().Update(ctx, row)
}

// findLocationAtNaturalKey 取（项目, 指纹）这个自然键上的活行；没有返回 (nil, nil)。
// 仓储对「没有」既可能回 (nil, nil) 也可能回 gorm.ErrRecordNotFound，两种都归一。
func findLocationAtNaturalKey(
	ctx context.Context, projectID int64, fingerprint devicefp.Carrier,
) (*project_location_entity.ProjectLocation, error) {
	if projectID == 0 || fingerprint == "" {
		return nil, nil
	}
	row, err := project_location_repo.ProjectLocation().
		FindByProjectAndFingerprint(ctx, projectID, fingerprint)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return row, nil
}

// syncIDAtNaturalKey 报告（项目同步标识, agentred 指纹）这个自然键上现在站着的是
// 哪一行（决策 26）；没有活行时返回空串。R4b 的合并落败判定用它，见
// downlink.recordMergeLosses。
func (projectLocationAdapter) syncIDAtNaturalKey(ctx context.Context, in *inbound) (string, error) {
	if in.ScopeSyncID == "" || in.AgentredFingerprint == "" {
		return "", nil
	}
	projectID, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindProject, in.ScopeSyncID)
	if err != nil || projectID == 0 {
		return "", err
	}
	row, err := findLocationAtNaturalKey(ctx, projectID, in.AgentredFingerprint)
	if err != nil || row == nil {
		return "", err
	}
	return row.SyncID, nil
}

func (projectLocationAdapter) remove(ctx context.Context, in *inbound) error {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindProjectLocation, in.SyncID)
	if err != nil || id == 0 {
		return err
	}
	return project_location_repo.ProjectLocation().Delete(ctx, id)
}
