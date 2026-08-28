package agent_svc

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

const (
	// avatarMaxBytes 头像上传字节上限（解码后）。
	avatarMaxBytes = 2 * 1024 * 1024
)

// avatarDataURLPrefixes 允许的 data URL 前缀。
var avatarDataURLPrefixes = []string{
	"data:image/png;base64,",
	"data:image/jpeg;base64,",
	"data:image/webp;base64,",
}

// AgentSvc Agent 应用服务。
type AgentSvc interface {
	Create(ctx context.Context, req *CreateAgentRequest) (*CreateAgentResponse, error)
	Update(ctx context.Context, req *UpdateAgentRequest) (*UpdateAgentResponse, error)
	Move(ctx context.Context, req *MoveAgentRequest) (*MoveAgentResponse, error)
	Delete(ctx context.Context, req *DeleteAgentRequest) (*DeleteAgentResponse, error)
	UploadAvatar(ctx context.Context, req *UploadAvatarRequest) (*UploadAvatarResponse, error)
	DeleteAvatar(ctx context.Context, req *DeleteAvatarRequest) (*DeleteAvatarResponse, error)
	SetPinned(ctx context.Context, req *SetPinnedRequest) (*SetPinnedResponse, error)
	Reorder(ctx context.Context, req *ReorderAgentsRequest) error
}

type agentSvc struct {
	now func() int64
}

var defaultAgent AgentSvc = &agentSvc{now: func() int64 { return time.Now().UnixMilli() }}

func Agent() AgentSvc { return defaultAgent }

func (s *agentSvc) Create(ctx context.Context, req *CreateAgentRequest) (*CreateAgentResponse, error) {
	now := s.now()
	a := &agent_entity.Agent{
		Name:           strings.TrimSpace(req.Name),
		Description:    strings.TrimSpace(req.Description),
		AvatarColor:    strings.TrimSpace(req.AvatarColor),
		AvatarIcon:     strings.TrimSpace(req.AvatarIcon),
		DepartmentID:   req.DepartmentID,
		ParentAgentID:  req.ParentAgentID,
		AgentBackendID: req.AgentBackendID,
		Status:         consts.ACTIVE,
		Createtime:     now,
		Updatetime:     now,
	}
	a.SetPrompt(req.Prompt)
	a.SkillsJSON = encodeSkills(skillsFromDTO(req.Skills))
	a.SetTools(toolsFromDTO(req.Tools))
	if err := a.Check(ctx); err != nil {
		return nil, err
	}
	if err := s.requireActivePlacement(ctx, a.DepartmentID, a.ParentAgentID, 0); err != nil {
		return nil, err
	}
	if err := s.requireActiveBackend(ctx, a.AgentBackendID); err != nil {
		return nil, err
	}
	dup, err := agent_repo.Agent().FindByName(ctx, a.Name)
	if err != nil {
		return nil, err
	}
	if dup != nil {
		return nil, i18n.NewError(ctx, code.AgentNameDuplicated)
	}
	next, err := s.nextSortOrder(ctx, a.DepartmentID, a.ParentAgentID)
	if err != nil {
		return nil, err
	}
	a.SortOrder = next
	if err := agent_repo.Agent().Create(ctx, a); err != nil {
		return nil, err
	}
	// 执行目标行随 Agent 的写入路径一起变化，级联在同步层展开（R15/R15e）。
	sync_svc.NotifyCreate(ctx, syncwire.KindAgent, a.ID, a.SyncMeta)
	targets, _ := execTargetSnapshot(ctx, a.ID)
	return &CreateAgentResponse{Item: toItem(a, targets)}, nil
}

func (s *agentSvc) Update(ctx context.Context, req *UpdateAgentRequest) (*UpdateAgentResponse, error) {
	existing, err := agent_repo.Agent().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	// R14 / R16：orderOverride 非 nil = 只写本端顺序覆盖，不碰账号默认执行目标列表、
	// 不同步（不触发 NotifyUpdate / 墓碑级联）。顺序覆盖只表达排列，不增删档，因此
	// 其余字段在此路径下被忽略。
	if req.OrderOverride != nil {
		if err := s.applyExecTargetOrderOverride(ctx, existing.ID, req.OrderOverride); err != nil {
			return nil, err
		}
		targets, _ := execTargetSnapshot(ctx, existing.ID)
		return &UpdateAgentResponse{Item: toItem(existing, targets)}, nil
	}
	newName := strings.TrimSpace(req.Name)
	if newName != existing.Name {
		dup, err := agent_repo.Agent().FindByName(ctx, newName)
		if err != nil {
			return nil, err
		}
		if dup != nil && dup.ID != existing.ID {
			return nil, i18n.NewError(ctx, code.AgentNameDuplicated)
		}
	}
	targets, err := s.buildExecTargets(ctx, req.ExecTargets)
	if err != nil {
		return nil, err
	}
	existing.Name = newName
	existing.Description = strings.TrimSpace(req.Description)
	existing.AvatarColor = strings.TrimSpace(req.AvatarColor)
	existing.AvatarIcon = strings.TrimSpace(req.AvatarIcon)
	// AgentBackendID/SkillsJSON 是 Agent 行上的保留列（= targets[0] 的镜像，删列前的
	// 回滚窗口用；组织架构的读口已经改从执行目标行取技能，R15e）；写口只信
	// ExecTargets，这里只是把镜像保持同步，不重复做校验。
	existing.AgentBackendID = targets[0].AgentBackendID
	existing.SkillsJSON = targets[0].SkillsJSON
	existing.SetPrompt(req.Prompt)
	existing.SetTools(toolsFromDTO(req.Tools))
	existing.Updatetime = s.now()
	if err := existing.Check(ctx); err != nil {
		return nil, err
	}
	// 执行目标列表被这次写入重排/裁剪：被挤掉的档要各自落墓碑（R6），留下来的档
	// 随 Agent 一起上行（同步层的 dependents）。快照必须在写之前取。
	targetsBefore, beforeOK := execTargetSnapshot(ctx, existing.ID)
	if err := agent_repo.Agent().UpdateWithTargets(ctx, existing, targets); err != nil {
		return nil, err
	}
	targetsAfter, afterOK := execTargetSnapshot(ctx, existing.ID)
	notifyDroppedExecTargets(ctx, targetsBefore, targetsAfter, beforeOK && afterOK)
	sync_svc.NotifyUpdate(ctx, syncwire.KindAgent, existing.ID, existing.SyncMeta)
	return &UpdateAgentResponse{Item: toItem(existing, targetsAfter)}, nil
}

// applyExecTargetOrderOverride 把本端执行目标顺序覆盖落到纯本地表（R14）：非空数组
// 按此顺序 upsert，空数组 = 清除（「恢复为账号默认顺序」）。仓储未装配（同步未就绪
// 的极端构建）时是空操作——覆盖本就是可选的本地偏好，丢了不丢数据。
func (s *agentSvc) applyExecTargetOrderOverride(ctx context.Context, agentID int64, order []int64) error {
	repo := agent_repo.AgentExecTargetOverride()
	if repo == nil {
		return nil
	}
	if len(order) == 0 {
		return repo.Delete(ctx, agentID)
	}
	o := &agent_entity.AgentExecTargetOverride{AgentID: agentID, Updatetime: s.now()}
	o.SetOrder(order)
	return repo.Save(ctx, o)
}

// buildExecTargets 把 UpdateAgentRequest.ExecTargets 转成仓储层的执行目标列表：
// 校验至少一项（R15：「列表为空的 Agent 不能起会话——界面在保存时就要求至少一项」，
// 这里是那条校验的服务端防线）、每一档的 backend 存在且启用、同一个 Agent 不重复
// 挂同一个 backend（前端「+ 添加」面板已经靠这个不变量把「已在列表中」的选项置灰，
// 这里是同一条不变量的服务端防线）。
func (s *agentSvc) buildExecTargets(ctx context.Context, items []ExecTargetInputDTO) ([]*agent_entity.AgentExecTarget, error) {
	if len(items) == 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	// 先做不碰 DB 的结构校验（每档 backendId 有效 + 同一个 Agent 不重复挂同一个
	// backend），全部通过之后才逐个去查 backend 是否存在且启用——避免对一个注定
	// 因结构问题被拒的请求发出多余的 Find 调用。
	seen := make(map[int64]struct{}, len(items))
	for _, it := range items {
		if it.AgentBackendID <= 0 {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		if _, dup := seen[it.AgentBackendID]; dup {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		seen[it.AgentBackendID] = struct{}{}
	}
	out := make([]*agent_entity.AgentExecTarget, 0, len(items))
	for _, it := range items {
		if err := s.requireActiveBackend(ctx, it.AgentBackendID); err != nil {
			return nil, err
		}
		out = append(out, &agent_entity.AgentExecTarget{
			AgentBackendID: it.AgentBackendID,
			SkillsJSON:     encodeSkills(skillsFromDTO(it.Skills)),
		})
	}
	return out, nil
}

func (s *agentSvc) Move(ctx context.Context, req *MoveAgentRequest) (*MoveAgentResponse, error) {
	existing, err := agent_repo.Agent().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	if existing.IsSystem() {
		return nil, i18n.NewError(ctx, code.AgentSystemImmutable)
	}
	if err := s.requireActivePlacement(ctx, req.NewDepartmentID, req.NewParentAgentID, existing.ID); err != nil {
		return nil, err
	}
	sortOrder := req.NewSortOrder
	if sortOrder <= 0 {
		next, err := s.nextSortOrder(ctx, req.NewDepartmentID, req.NewParentAgentID)
		if err != nil {
			return nil, err
		}
		sortOrder = next
	}
	if err := agent_repo.Agent().UpdatePlacement(ctx, existing.ID, req.NewDepartmentID, req.NewParentAgentID, sortOrder); err != nil {
		return nil, err
	}
	existing.DepartmentID = req.NewDepartmentID
	existing.ParentAgentID = req.NewParentAgentID
	existing.SortOrder = sortOrder
	sync_svc.NotifyUpdate(ctx, syncwire.KindAgent, existing.ID, existing.SyncMeta)
	targets, _ := execTargetSnapshot(ctx, existing.ID)
	return &MoveAgentResponse{Item: toItem(existing, targets)}, nil
}

func (s *agentSvc) Reorder(ctx context.Context, req *ReorderAgentsRequest) error {
	if req == nil || len(req.OrderedIDs) == 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	// 与实体 Check 一致:恰好挂在部门或上级之一。
	if (req.DepartmentID > 0) == (req.ParentAgentID > 0) {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	var siblings []*agent_entity.Agent
	var err error
	if req.ParentAgentID > 0 {
		siblings, err = agent_repo.Agent().ListByParent(ctx, req.ParentAgentID)
	} else {
		siblings, err = agent_repo.Agent().ListByDepartment(ctx, req.DepartmentID)
	}
	if err != nil {
		return err
	}
	if len(siblings) != len(req.OrderedIDs) {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	allowed := make(map[int64]struct{}, len(siblings))
	for _, a := range siblings {
		allowed[a.ID] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(req.OrderedIDs))
	for _, id := range req.OrderedIDs {
		if id <= 0 {
			return i18n.NewError(ctx, code.InvalidParameter)
		}
		if _, ok := allowed[id]; !ok {
			return i18n.NewError(ctx, code.InvalidParameter)
		}
		if _, ok := seen[id]; ok {
			return i18n.NewError(ctx, code.InvalidParameter)
		}
		seen[id] = struct{}{}
	}
	if err := agent_repo.Agent().ReorderSiblings(ctx, req.DepartmentID, req.ParentAgentID, req.OrderedIDs); err != nil {
		return err
	}
	for _, sibling := range siblings {
		sync_svc.NotifyUpdate(ctx, syncwire.KindAgent, sibling.ID, sibling.SyncMeta)
	}
	return nil
}

func (s *agentSvc) Delete(ctx context.Context, req *DeleteAgentRequest) (*DeleteAgentResponse, error) {
	existing, err := agent_repo.Agent().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	if existing.IsSystem() {
		return nil, i18n.NewError(ctx, code.AgentSystemImmutable)
	}
	err = db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		if err := agent_repo.Agent().ClearLeadOfDepartment(txCtx, existing.ID); err != nil {
			return err
		}
		if err := agent_repo.Agent().ReparentChildren(txCtx, existing.ID, existing.DepartmentID, existing.ParentAgentID); err != nil {
			return err
		}
		return agent_repo.Agent().Delete(txCtx, existing.ID)
	})
	if err != nil {
		return nil, err
	}
	// 成员关系与执行目标列表项随它一并落墓碑，级联在同步层展开（R6）。
	sync_svc.NotifyDelete(ctx, syncwire.KindAgent, existing.ID, existing.SyncMeta)
	return &DeleteAgentResponse{}, nil
}

func (s *agentSvc) UploadAvatar(ctx context.Context, req *UploadAvatarRequest) (*UploadAvatarResponse, error) {
	existing, err := agent_repo.Agent().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	if err := validateAvatarDataURL(ctx, req.DataURL); err != nil {
		return nil, err
	}
	existing.AvatarDataURL = req.DataURL
	existing.Updatetime = s.now()
	if err := agent_repo.Agent().UpdateAvatar(ctx, existing.ID, req.DataURL, existing.Updatetime); err != nil {
		return nil, err
	}
	// R16a：头像正文按内容哈希单独传，但「换了头像」本身是 Agent 行的一次普通修改，
	// 必须照常触发上行 —— 不发这条通知，新头像要等用户碰巧改了别的字段才到对端。
	sync_svc.NotifyUpdate(ctx, syncwire.KindAgent, existing.ID, existing.SyncMeta)
	targets, _ := execTargetSnapshot(ctx, existing.ID)
	return &UploadAvatarResponse{Item: toItem(existing, targets)}, nil
}

func (s *agentSvc) DeleteAvatar(ctx context.Context, req *DeleteAvatarRequest) (*DeleteAvatarResponse, error) {
	existing, err := agent_repo.Agent().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	existing.AvatarDataURL = ""
	existing.Updatetime = s.now()
	if err := agent_repo.Agent().UpdateAvatar(ctx, existing.ID, "", existing.Updatetime); err != nil {
		return nil, err
	}
	// 同 UploadAvatar：清掉自定义头像也是一次内容变化（R16a）。
	sync_svc.NotifyUpdate(ctx, syncwire.KindAgent, existing.ID, existing.SyncMeta)
	targets, _ := execTargetSnapshot(ctx, existing.ID)
	return &DeleteAvatarResponse{Item: toItem(existing, targets)}, nil
}

// SetPinned 切换 Agent 用户置顶。系统 agent 与普通 Agent 同一条路径，不特判：
// 置顶与否完全由 DB 的 pinned 列承载，侧栏读口不再用 IsSystem() 强制浮顶
// （R: ceo-unpin）。
func (s *agentSvc) SetPinned(ctx context.Context, req *SetPinnedRequest) (*SetPinnedResponse, error) {
	existing, err := agent_repo.Agent().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	if err := agent_repo.Agent().SetPinned(ctx, existing.ID, req.Pinned); err != nil {
		return nil, err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindAgent, existing.ID, existing.SyncMeta)
	return &SetPinnedResponse{ID: existing.ID, Pinned: req.Pinned}, nil
}

func validateAvatarDataURL(ctx context.Context, dataURL string) error {
	if dataURL == "" {
		return i18n.NewError(ctx, code.AgentAvatarInvalid)
	}
	var payload string
	matched := false
	for _, prefix := range avatarDataURLPrefixes {
		if strings.HasPrefix(dataURL, prefix) {
			payload = dataURL[len(prefix):]
			matched = true
			break
		}
	}
	if !matched {
		return i18n.NewError(ctx, code.AgentAvatarInvalid)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return i18n.NewError(ctx, code.AgentAvatarInvalid)
	}
	if len(decoded) > avatarMaxBytes {
		return i18n.NewError(ctx, code.AgentAvatarTooLarge)
	}
	return nil
}

func (s *agentSvc) requireActiveDepartment(ctx context.Context, id int64) error {
	if id <= 0 {
		return i18n.NewError(ctx, code.AgentDepartmentRequired)
	}
	d, err := department_repo.Department().Find(ctx, id)
	if err != nil {
		return err
	}
	if d == nil {
		return i18n.NewError(ctx, code.AgentDepartmentNotFound)
	}
	if !d.IsActive() {
		return i18n.NewError(ctx, code.AgentDepartmentInactive)
	}
	return nil
}

func (s *agentSvc) requireActivePlacement(ctx context.Context, departmentID, parentAgentID, movingAgentID int64) error {
	hasDepartment := departmentID > 0
	hasParentAgent := parentAgentID > 0
	if !hasDepartment && !hasParentAgent {
		return i18n.NewError(ctx, code.AgentDepartmentRequired)
	}
	if hasDepartment && hasParentAgent {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if hasDepartment {
		return s.requireActiveDepartment(ctx, departmentID)
	}

	parent, err := agent_repo.Agent().Find(ctx, parentAgentID)
	if err != nil {
		return err
	}
	if parent == nil || !parent.IsActive() {
		return i18n.NewError(ctx, code.AgentParentNotFound)
	}
	if movingAgentID > 0 {
		if parentAgentID == movingAgentID {
			return i18n.NewError(ctx, code.AgentCircularReference)
		}
		all, err := agent_repo.Agent().List(ctx)
		if err != nil {
			return err
		}
		if hasAgentCycle(all, parentAgentID, movingAgentID) {
			return i18n.NewError(ctx, code.AgentCircularReference)
		}
	}
	return nil
}

func (s *agentSvc) nextSortOrder(ctx context.Context, departmentID, parentAgentID int64) (int, error) {
	if parentAgentID > 0 {
		return agent_repo.Agent().NextSortOrderByParent(ctx, parentAgentID)
	}
	return agent_repo.Agent().NextSortOrder(ctx, departmentID)
}

func hasAgentCycle(all []*agent_entity.Agent, startParentID, selfID int64) bool {
	index := make(map[int64]*agent_entity.Agent, len(all))
	for _, a := range all {
		index[a.ID] = a
	}
	cur := startParentID
	for cur > 0 {
		if cur == selfID {
			return true
		}
		next, ok := index[cur]
		if !ok {
			return false
		}
		cur = next.ParentAgentID
	}
	return false
}

func (s *agentSvc) requireActiveBackend(ctx context.Context, id int64) error {
	if id <= 0 {
		return nil // 后端可选，0 表示未配置
	}
	b, err := agent_backend_repo.AgentBackend().Find(ctx, id)
	if err != nil {
		return err
	}
	if b == nil || !b.IsActive() {
		return i18n.NewError(ctx, code.AgentBackendInvalidRef)
	}
	return nil
}

func skillsFromDTO(items []department_svc.AgentSkillDTO) []agent_entity.AgentSkillItem {
	out := make([]agent_entity.AgentSkillItem, 0, len(items))
	for _, s := range items {
		out = append(out, agent_entity.AgentSkillItem{ID: s.ID, Enabled: s.Enabled})
	}
	return out
}

// encodeSkills 技能授权的存放位置已下沉到 AgentExecTarget（R15e），GetSkills /
// SetSkills 也随字段一起搬了过去；agent_svc 只在**写**的方向上还经手 Agent 结构体
// 上的 SkillsJSON 原始载荷（交给仓储层，由 agent_repo 转落到那唯一一档的执行目标
// 行，见 agent_repo.primaryTargetList），这里借一个临时的执行目标值对象复用同一份
// 编码逻辑，不重复实现。**读**的方向一律走执行目标行（primaryTargetSkills）。
func encodeSkills(items []agent_entity.AgentSkillItem) string {
	t := agent_entity.AgentExecTarget{}
	t.SetSkills(items)
	return t.SkillsJSON
}

func toolsFromDTO(items []department_svc.AgentToolDTO) []agent_entity.AgentToolItem {
	out := make([]agent_entity.AgentToolItem, 0, len(items))
	for _, t := range items {
		out = append(out, agent_entity.AgentToolItem{Key: t.Key, Enabled: t.Enabled})
	}
	return out
}

// toItem 把 Agent 行 + 它当前的执行目标列表打平成前端 DTO。targets 由调用方给出
// （通常是刚写完之后的 execTargetSnapshot 结果），避免这里重复查询。
func toItem(a *agent_entity.Agent, targets []*agent_entity.AgentExecTarget) *AgentItem {
	rawTools := a.GetTools()
	tools := make([]department_svc.AgentToolDTO, 0, len(rawTools))
	for _, t := range rawTools {
		tools = append(tools, department_svc.AgentToolDTO{Key: t.Key, Enabled: t.Enabled})
	}
	return &AgentItem{
		ID:            a.ID,
		Name:          a.Name,
		Description:   a.Description,
		AvatarColor:   a.AvatarColor,
		AvatarIcon:    a.AvatarIcon,
		AvatarDataURL: a.AvatarDataURL,
		SystemBadge:   a.SystemBadge,
		DepartmentID:  a.DepartmentID,
		ParentAgentID: a.ParentAgentID,
		SortOrder:     a.SortOrder,
		Prompt:        a.GetPrompt(),
		ExecTargets:   toAgentExecTargetItems(targets),
		Tools:         tools,
		Createtime:    a.Createtime,
		Updatetime:    a.Updatetime,
	}
}

// toAgentExecTargetItems 把执行目标行投影成前端 DTO（与 department_svc 的同名私有
// 辅助函数职责相同，两处入口各自独立维护——见 agent_entity.AgentExecTarget 到
// department_svc.AgentExecTargetItem 的转换，department_svc.Load 走批量查询、
// 这里走单个 Agent 的写后快照，来源不同不合并）。
func toAgentExecTargetItems(rows []*agent_entity.AgentExecTarget) []department_svc.AgentExecTargetItem {
	out := make([]department_svc.AgentExecTargetItem, 0, len(rows))
	for _, row := range rows {
		rawSkills := row.GetSkills()
		skills := make([]department_svc.AgentSkillDTO, 0, len(rawSkills))
		for _, s := range rawSkills {
			skills = append(skills, department_svc.AgentSkillDTO{ID: s.ID, Enabled: s.Enabled})
		}
		out = append(out, department_svc.AgentExecTargetItem{
			ID:             row.ID,
			AgentBackendID: row.AgentBackendID,
			Skills:         skills,
		})
	}
	return out
}

// execTargetSnapshot 取某个 Agent 当前的执行目标行。第二个返回值报告这次读**是否
// 成功**：读失败返回的空列表与「一档都没有」在类型上不可区分，而这两者对墓碑级联
// 的含义正好相反（见 notifyDroppedExecTargets）。
func execTargetSnapshot(ctx context.Context, agentID int64) ([]*agent_entity.AgentExecTarget, bool) {
	// 刻意**不**按 sync_svc.Active() 短路：这份快照除了喂同步的墓碑级联，还是
	// AgentItem 里 Skills 的真相来源（R15e）——未登录时短路掉会让写完之后回给前端
	// 的那份 DTO 一个技能都没有。
	rows, err := agent_repo.AgentExecTarget().ListByAgent(ctx, agentID)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_svc.execTargetSnapshot: read exec targets failed",
			zap.Int64("agentId", agentID), zap.Error(err))
		return nil, false
	}
	return rows, true
}

// notifyDroppedExecTargets 把「写入前有、写入后没了」的那些档报成删除：它们在别的
// 端还活着，不落墓碑就会在下一次下行时被原样送回来（R6）。
//
// 两次快照里任何一次读失败，这个差集就不可信：写入本身已经提交，读失败时的空列表
// 会让**每一档**都算成「被挤掉了」，墓碑一上行就把别的端上还活着的档全删了，而本机
// 一档没少。宁可漏报（下一次下行会把该删的原样送回来，届时再收敛），不可错报。
func notifyDroppedExecTargets(ctx context.Context, before, after []*agent_entity.AgentExecTarget, trustworthy bool) {
	if !trustworthy || len(before) == 0 {
		return
	}
	kept := make(map[string]struct{}, len(after))
	for _, row := range after {
		kept[row.SyncID] = struct{}{}
	}
	for _, row := range before {
		if row.SyncID == "" {
			continue
		}
		if _, ok := kept[row.SyncID]; !ok {
			sync_svc.NotifyDelete(ctx, syncwire.KindAgentExecTarget, row.ID, row.SyncMeta)
		}
	}
}
