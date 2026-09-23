package ctl_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 生产写网关：把执行者合并好的文档翻译成现有服务的请求，一个字段只经它在编辑器里走的
// 那个服务方法写入（改父级走 Move、置顶走 SetPinned、默认模型走 SetModelDefault…）。
// update 只调用值真的变了的那几个方法；服务层的错误原样上抛。

func productionWriters(p servicePorts) map[agentrewire.CtlKind]KindWriter {
	return map[agentrewire.CtlKind]KindWriter{
		agentrewire.CtlKind_CTL_KIND_AGENT:      agentWriter{p},
		agentrewire.CtlKind_CTL_KIND_DEPARTMENT: departmentWriter{p},
		agentrewire.CtlKind_CTL_KIND_PROJECT:    projectWriter{p},
		agentrewire.CtlKind_CTL_KIND_PROVIDER:   providerWriter{p},
		agentrewire.CtlKind_CTL_KIND_MODEL:      modelWriter{p},
		agentrewire.CtlKind_CTL_KIND_BACKEND:    backendWriter{p},
	}
}

// ---- agents ----

type agentWriter struct{ p servicePorts }

func (w agentWriter) Create(ctx context.Context, wr Write) (int64, error) {
	a := wr.Next.GetAgent()
	var first int64
	if len(a.GetBackendIds()) > 0 {
		first = a.GetBackendIds()[0]
	}
	resp, err := w.p.agents().Create(ctx, &agent_svc.CreateAgentRequest{
		Name: a.GetName(), Description: a.GetDescription(), AvatarColor: a.GetAvatarColor(), AvatarIcon: a.GetAvatarIcon(),
		DepartmentID: a.GetDepartmentId(), AgentBackendID: first,
	})
	if err != nil {
		return 0, err
	}
	// 创建只收一个执行目标；其余目标与置顶按 update 的路径补上。
	return resp.Item.ID, w.apply(ctx, resp.Item, a)
}

func (w agentWriter) Update(ctx context.Context, wr Write) error {
	id := wr.Cur.GetAgent().GetId()
	org, err := w.p.departments().Load(ctx, &department_svc.LoadOrgRequest{})
	if err != nil {
		return err
	}
	for _, item := range org.Agents {
		if item.ID == id {
			return w.apply(ctx, item, wr.Next.GetAgent())
		}
	}
	return errNotFound{kind: agentrewire.CtlKind_CTL_KIND_AGENT, id: id}
}

// apply 把 item（当前数据）改成 next。Update 是整份快照写入，提示词、工具授权和各执行
// 目标已有的技能授权原样带回。
func (w agentWriter) apply(ctx context.Context, item *department_svc.AgentItem, next *agentrewire.CtlAgent) error {
	cur := agentDocFrom(item)
	if cur.GetName() != next.GetName() || cur.GetDescription() != next.GetDescription() ||
		cur.GetAvatarColor() != next.GetAvatarColor() || cur.GetAvatarIcon() != next.GetAvatarIcon() ||
		!slices.Equal(cur.GetBackendIds(), next.GetBackendIds()) {
		skills := map[int64][]department_svc.AgentSkillDTO{}
		for _, t := range item.ExecTargets {
			skills[t.AgentBackendID] = t.Skills
		}
		targets := make([]agent_svc.ExecTargetInputDTO, 0, len(next.GetBackendIds()))
		for _, id := range next.GetBackendIds() {
			targets = append(targets, agent_svc.ExecTargetInputDTO{AgentBackendID: id, Skills: skills[id]})
		}
		if _, err := w.p.agents().Update(ctx, &agent_svc.UpdateAgentRequest{
			ID: item.ID, Name: next.GetName(), Description: next.GetDescription(),
			AvatarColor: next.GetAvatarColor(), AvatarIcon: next.GetAvatarIcon(),
			Prompt: item.Prompt, ExecTargets: targets, Tools: item.Tools,
		}); err != nil {
			return err
		}
	}
	if cur.GetDepartmentId() != next.GetDepartmentId() {
		if _, err := w.p.agents().Move(ctx, &agent_svc.MoveAgentRequest{ID: item.ID, NewDepartmentID: next.GetDepartmentId()}); err != nil {
			return err
		}
	}
	if cur.GetPinned() != next.GetPinned() {
		if _, err := w.p.agents().SetPinned(ctx, &agent_svc.SetPinnedRequest{ID: item.ID, Pinned: next.GetPinned()}); err != nil {
			return err
		}
	}
	return nil
}

func (w agentWriter) Delete(ctx context.Context, wr Write) error {
	_, err := w.p.agents().Delete(ctx, &agent_svc.DeleteAgentRequest{ID: wr.Cur.GetAgent().GetId()})
	return err
}

// ---- departments ----

type departmentWriter struct{ p servicePorts }

func (w departmentWriter) Create(ctx context.Context, wr Write) (int64, error) {
	d := wr.Next.GetDepartment()
	resp, err := w.p.departments().Create(ctx, &department_svc.CreateDepartmentRequest{
		Name: d.GetName(), Description: d.GetDescription(), Icon: d.GetIcon(), AccentColor: d.GetAccentColor(), ParentID: d.GetParentId(),
	})
	if err != nil {
		return 0, err
	}
	if d.GetLeadAgentId() != 0 { // 创建不收负责人，按 update 补上
		if err := w.update(ctx, resp.Item.ID, d); err != nil {
			return resp.Item.ID, err
		}
	}
	return resp.Item.ID, nil
}

func (w departmentWriter) update(ctx context.Context, id int64, d *agentrewire.CtlDepartment) error {
	_, err := w.p.departments().Update(ctx, &department_svc.UpdateDepartmentRequest{
		ID: id, Name: d.GetName(), Description: d.GetDescription(), Icon: d.GetIcon(), AccentColor: d.GetAccentColor(),
		LeadAgentID: d.GetLeadAgentId(),
	})
	return err
}

func (w departmentWriter) Update(ctx context.Context, wr Write) error {
	cur, next := wr.Cur.GetDepartment(), wr.Next.GetDepartment()
	if cur.GetName() != next.GetName() || cur.GetDescription() != next.GetDescription() || cur.GetIcon() != next.GetIcon() ||
		cur.GetAccentColor() != next.GetAccentColor() || cur.GetLeadAgentId() != next.GetLeadAgentId() {
		if err := w.update(ctx, cur.GetId(), next); err != nil {
			return err
		}
	}
	if cur.GetParentId() != next.GetParentId() {
		if _, err := w.p.departments().Move(ctx, &department_svc.MoveDepartmentRequest{ID: cur.GetId(), NewParentID: next.GetParentId()}); err != nil {
			return err
		}
	}
	return nil
}

func (w departmentWriter) Delete(ctx context.Context, wr Write) error {
	strategy := department_svc.StrategyReparent
	if wr.Cascade {
		strategy = department_svc.StrategyCascade
	}
	_, err := w.p.departments().Delete(ctx, &department_svc.DeleteDepartmentRequest{ID: wr.Cur.GetDepartment().GetId(), Strategy: strategy})
	return err
}

// CascadeImpact 按服务层级联删除的同一口径计数：部门子树（不含它自己），以及挂在子树
// 部门上的顶层 Agent 连同它们的下级 Agent。
func (w departmentWriter) CascadeImpact(ctx context.Context, departmentID int64) (int, int, error) {
	org, err := w.p.departments().Load(ctx, &department_svc.LoadOrgRequest{})
	if err != nil {
		return 0, 0, err
	}
	childDepts := map[int64][]int64{}
	for _, d := range org.Departments {
		childDepts[d.ParentID] = append(childDepts[d.ParentID], d.ID)
	}
	inTree := map[int64]bool{}
	var walkDept func(id int64)
	walkDept = func(id int64) {
		inTree[id] = true
		for _, c := range childDepts[id] {
			walkDept(c)
		}
	}
	walkDept(departmentID)

	childAgents := map[int64][]int64{}
	for _, a := range org.Agents {
		childAgents[a.ParentAgentID] = append(childAgents[a.ParentAgentID], a.ID)
	}
	seen := map[int64]bool{}
	var walkAgent func(id int64)
	walkAgent = func(id int64) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, c := range childAgents[id] {
			walkAgent(c)
		}
	}
	for _, a := range org.Agents {
		if a.ParentAgentID == 0 && inTree[a.DepartmentID] {
			walkAgent(a.ID)
		}
	}
	return len(inTree) - 1, len(seen), nil
}

// ---- projects ----

type projectWriter struct{ p servicePorts }

func (w projectWriter) Create(ctx context.Context, wr Write) (int64, error) {
	p := wr.Next.GetProject()
	created, err := w.p.projects().Create(ctx, &project_svc.CreateProjectRequest{
		ParentID: p.GetParentId(), Name: p.GetName(), Icon: p.GetIcon(), Color: p.GetColor(),
		Description: p.GetDescription(), Path: p.GetPath(), InitialAgentIDs: p.GetMemberAgentIds(),
	})
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (w projectWriter) Update(ctx context.Context, wr Write) error {
	cur, next := wr.Cur.GetProject(), wr.Next.GetProject()
	id := cur.GetId()
	svc := w.p.projects()
	if cur.GetName() != next.GetName() || cur.GetIcon() != next.GetIcon() || cur.GetColor() != next.GetColor() ||
		cur.GetDescription() != next.GetDescription() {
		if _, err := svc.Update(ctx, &project_svc.UpdateProjectRequest{
			ID: id, Name: next.GetName(), Icon: next.GetIcon(), Color: next.GetColor(), Description: next.GetDescription(),
		}); err != nil {
			return err
		}
	}
	if cur.GetParentId() != next.GetParentId() {
		if _, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: id, NewParentID: next.GetParentId()}); err != nil {
			return err
		}
	}
	if cur.GetPath() != next.GetPath() {
		var err error
		if strings.TrimSpace(next.GetPath()) == "" {
			_, err = svc.ClearLocalPath(ctx, id)
		} else {
			_, err = svc.SetLocalPath(ctx, id, next.GetPath())
		}
		if err != nil {
			return err
		}
	}
	for _, agentID := range next.GetMemberAgentIds() {
		if !slices.Contains(cur.GetMemberAgentIds(), agentID) {
			if err := svc.AddMember(ctx, id, agentID); err != nil {
				return err
			}
		}
	}
	for _, agentID := range cur.GetMemberAgentIds() {
		if !slices.Contains(next.GetMemberAgentIds(), agentID) {
			if err := svc.RemoveMember(ctx, id, agentID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w projectWriter) Delete(ctx context.Context, wr Write) error {
	return w.p.projects().Delete(ctx, wr.Cur.GetProject().GetId())
}

// ---- providers ----

type providerWriter struct{ p servicePorts }

func (w providerWriter) Create(ctx context.Context, wr Write) (int64, error) {
	p := wr.Next.GetProvider()
	resp, err := w.p.providers().Create(ctx, &llm_provider_svc.CreateProviderRequest{
		Type: p.GetType(), Name: p.GetName(), APIKey: p.GetApiKey(), BaseURL: p.GetBaseUrl(),
	})
	if err != nil {
		return 0, err
	}
	// 服务层以停用态创建（还没有默认模型）；显式 --enable 交给它去判能不能启用。
	if wr.Fields["enabled"] && p.GetEnabled() {
		if _, err := w.p.providers().SetProviderEnabled(ctx, &llm_provider_svc.SetProviderEnabledRequest{ID: resp.Item.ID, Enabled: true}); err != nil {
			return resp.Item.ID, err
		}
	}
	return resp.Item.ID, nil
}

func (w providerWriter) Update(ctx context.Context, wr Write) error {
	cur, next := wr.Cur.GetProvider(), wr.Next.GetProvider()
	id := cur.GetId()
	svc := w.p.providers()
	// api key 空 = 沿用原值（服务层规则）；非空才是本次写入的明文。
	if cur.GetName() != next.GetName() || cur.GetBaseUrl() != next.GetBaseUrl() || next.GetApiKey() != "" {
		if _, err := svc.Update(ctx, &llm_provider_svc.UpdateProviderRequest{
			ID: id, Name: next.GetName(), APIKey: next.GetApiKey(), BaseURL: next.GetBaseUrl(),
		}); err != nil {
			return err
		}
	}
	if next.GetDefaultModelKey() != "" && cur.GetDefaultModelKey() != next.GetDefaultModelKey() {
		if _, err := svc.SetModelDefault(ctx, &llm_provider_svc.SetModelDefaultRequest{ProviderID: id, ModelKey: next.GetDefaultModelKey()}); err != nil {
			return err
		}
	}
	if cur.GetEnabled() != next.GetEnabled() {
		if _, err := svc.SetProviderEnabled(ctx, &llm_provider_svc.SetProviderEnabledRequest{ID: id, Enabled: next.GetEnabled()}); err != nil {
			return err
		}
	}
	return nil
}

func (w providerWriter) Delete(ctx context.Context, wr Write) error {
	_, err := w.p.providers().Delete(ctx, &llm_provider_svc.DeleteProviderRequest{ID: wr.Cur.GetProvider().GetId(), ConfirmReference: wr.Force})
	return err
}

// ---- models ----

type modelWriter struct{ p servicePorts }

func (w modelWriter) Create(ctx context.Context, wr Write) (int64, error) {
	m := wr.Next.GetModel()
	svc := w.p.providers()
	existing, err := svc.ListModels(ctx, &llm_provider_svc.ListModelsRequest{ID: m.GetProviderId()})
	if err != nil {
		return 0, err
	}
	// ImportModels 对已有的 ModelID 是「补齐」而不是新建；create 不能悄悄改掉已有模型。
	for _, it := range existing.Items {
		if it.ModelID == strings.TrimSpace(m.GetModelId()) {
			return 0, fmt.Errorf("model %q already exists under this provider (id %d)", it.ModelID, it.ID)
		}
	}
	resp, err := svc.ImportModels(ctx, &llm_provider_svc.ImportModelsRequest{
		ProviderID: m.GetProviderId(),
		Models: []*llm_provider_svc.ModelInput{{
			ModelID: m.GetModelId(), Name: m.GetName(), ContextWindow: int(m.GetContextWindow()), MaxOutput: int(m.GetMaxOutput()),
		}},
	})
	if err != nil {
		return 0, err
	}
	var created *llm_provider_svc.ModelItem
	for _, it := range resp.Items {
		if it.ModelID == strings.TrimSpace(m.GetModelId()) {
			created = it
		}
	}
	if created == nil {
		return 0, fmt.Errorf("model %q was not created", m.GetModelId())
	}
	cur := modelDocFrom(created, 0)
	next := modelDocFrom(created, 0)
	if wr.Fields["enabled"] {
		next.Enabled = m.GetEnabled()
	}
	if wr.Fields["isDefault"] {
		next.IsDefault = m.GetIsDefault()
	}
	return created.ID, w.applyFlags(ctx, cur, next)
}

func (w modelWriter) Update(ctx context.Context, wr Write) error {
	cur, next := wr.Cur.GetModel(), wr.Next.GetModel()
	if cur.GetModelId() != next.GetModelId() || cur.GetName() != next.GetName() ||
		cur.GetContextWindow() != next.GetContextWindow() || cur.GetMaxOutput() != next.GetMaxOutput() {
		if _, err := w.p.providers().UpdateModel(ctx, &llm_provider_svc.UpdateModelRequest{
			ID: cur.GetId(), ModelID: next.GetModelId(), Name: next.GetName(),
			ContextWindow: int(next.GetContextWindow()), MaxOutput: int(next.GetMaxOutput()),
		}); err != nil {
			return err
		}
	}
	return w.applyFlags(ctx, cur, next)
}

// applyFlags 写启用与默认：先启用、再设默认、最后停用（服务层不许停用默认模型）。
func (w modelWriter) applyFlags(ctx context.Context, cur, next *agentrewire.CtlModel) error {
	svc := w.p.providers()
	setEnabled := func(v bool) error {
		_, err := svc.SetModelEnabled(ctx, &llm_provider_svc.SetModelEnabledRequest{ID: cur.GetId(), Enabled: v})
		return err
	}
	if !cur.GetEnabled() && next.GetEnabled() {
		if err := setEnabled(true); err != nil {
			return err
		}
	}
	if cur.GetIsDefault() != next.GetIsDefault() {
		if !next.GetIsDefault() {
			return fmt.Errorf("a provider's default model cannot be unset; make another model the default instead")
		}
		if _, err := svc.SetModelDefault(ctx, &llm_provider_svc.SetModelDefaultRequest{ProviderID: cur.GetProviderId(), ModelKey: cur.GetKey()}); err != nil {
			return err
		}
	}
	if cur.GetEnabled() && !next.GetEnabled() {
		return setEnabled(false)
	}
	return nil
}

func (w modelWriter) Delete(ctx context.Context, wr Write) error {
	_, err := w.p.providers().DeleteModel(ctx, &llm_provider_svc.DeleteModelRequest{ID: wr.Cur.GetModel().GetId(), ConfirmReference: wr.Force})
	return err
}

// ---- backends ----

type backendWriter struct{ p servicePorts }

// backendRequest 是 Create/UpdateBackendRequest 共有的字段。
type backendRequest struct {
	providerKey, modelKey, deviceID string
	envJSON                         string
	cfg                             syncwire.AgentBackendConfig
	routes                          map[string]agent_backend_svc.RouteTarget
}

func (w backendWriter) Create(ctx context.Context, wr Write) (int64, error) {
	b := wr.Next.GetBackend()
	r, err := w.request(ctx, b, nil, wr.Fields)
	if err != nil {
		return 0, err
	}
	req := &agent_backend_svc.CreateBackendRequest{
		Type: b.GetType(), Name: b.GetName(), LLMProviderKey: r.providerKey, LLMModelKey: r.modelKey, ModelRoutes: r.routes,
		Sandbox: r.cfg.Sandbox, Approval: r.cfg.Approval, EnvJSON: r.envJSON, ReasoningEffort: b.GetReasoningEffort(),
		DefaultPermissionMode: r.cfg.DefaultPermissionMode, DefaultModel: r.cfg.DefaultModel,
		OpenClawGatewayURL: r.cfg.OpenClawGatewayURL, OpenClawAgentID: r.cfg.OpenClawAgentID,
		OpenClawDefaultModel: r.cfg.OpenClawDefaultModel, OpenClawSessionMode: r.cfg.OpenClawSessionMode,
		HermesURL: r.cfg.HermesURL, HermesAuthProvider: r.cfg.HermesAuthProvider, HermesUserID: r.cfg.HermesUserID,
		ACPCommand: r.cfg.ACPCommand, ACPArgs: r.cfg.ACPArgs, DeviceID: r.deviceID,
	}
	var resp *agent_backend_svc.CreateBackendResponse
	if b.GetToken() != "" {
		resp, err = w.p.backends().CreateOpenClaw(ctx, req, b.GetToken())
	} else {
		resp, err = w.p.backends().Create(ctx, req)
	}
	if err != nil {
		return 0, err
	}
	return resp.Item.ID, nil
}

func (w backendWriter) Update(ctx context.Context, wr Write) error {
	id := wr.Cur.GetBackend().GetId()
	list, err := w.p.backends().List(ctx, &agent_backend_svc.ListBackendsRequest{})
	if err != nil {
		return err
	}
	var item *agent_backend_svc.BackendItem
	for _, it := range list.Items {
		if it.ID == id {
			item = it
		}
	}
	if item == nil {
		return errNotFound{kind: agentrewire.CtlKind_CTL_KIND_BACKEND, id: id}
	}
	b := wr.Next.GetBackend()
	r, err := w.request(ctx, b, item, wr.Fields)
	if err != nil {
		return err
	}
	req := &agent_backend_svc.UpdateBackendRequest{
		ID: id, Name: b.GetName(), LLMProviderKey: r.providerKey, LLMModelKey: r.modelKey, ModelRoutes: r.routes,
		Sandbox: r.cfg.Sandbox, Approval: r.cfg.Approval, EnvJSON: r.envJSON, ReasoningEffort: b.GetReasoningEffort(),
		DefaultPermissionMode: r.cfg.DefaultPermissionMode, DefaultModel: r.cfg.DefaultModel,
		OpenClawGatewayURL: r.cfg.OpenClawGatewayURL, OpenClawAgentID: r.cfg.OpenClawAgentID,
		OpenClawDefaultModel: r.cfg.OpenClawDefaultModel, OpenClawSessionMode: r.cfg.OpenClawSessionMode,
		HermesURL: r.cfg.HermesURL, HermesAuthProvider: r.cfg.HermesAuthProvider, HermesUserID: r.cfg.HermesUserID,
		ACPCommand: r.cfg.ACPCommand, ACPArgs: r.cfg.ACPArgs, DeviceID: r.deviceID,
	}
	if wr.Fields["token"] {
		// 空 token = 清除；token 由服务层写进后端绑定设备的钥匙串。
		_, err = w.p.backends().UpdateOpenClaw(ctx, req, b.GetToken(), b.GetToken() == "")
	} else {
		_, err = w.p.backends().Update(ctx, req)
	}
	return err
}

func (w backendWriter) Delete(ctx context.Context, wr Write) error {
	_, err := w.p.backends().Delete(ctx, &agent_backend_svc.DeleteBackendRequest{ID: wr.Cur.GetBackend().GetId()})
	return err
}

// request 算出服务请求里的引用与设置。update 时没写的引用沿用服务层的原始 key 与设备
// 指纹（item），不经过 id / 名字往返——悬空的引用或重名的设备也不会被改写。
func (w backendWriter) request(ctx context.Context, b *agentrewire.CtlBackend, item *agent_backend_svc.BackendItem, fields map[string]bool) (backendRequest, error) {
	var r backendRequest
	if item != nil {
		r.providerKey, r.modelKey, r.deviceID = item.LLMProviderKey, item.LLMModelKey, string(item.DeviceID)
	}
	var err error
	if item == nil || fields["providerId"] {
		if r.providerKey, err = w.providerKey(ctx, b.GetProviderId()); err != nil {
			return r, err
		}
	}
	if item == nil || fields["modelId"] {
		if r.modelKey, err = w.modelKey(ctx, b.GetModelId()); err != nil {
			return r, err
		}
	}
	if item == nil || fields["device"] {
		if r.deviceID, err = w.deviceID(ctx, b.GetDevice()); err != nil {
			return r, err
		}
	}
	if len(b.GetEnv()) > 0 {
		raw, err := json.Marshal(b.GetEnv())
		if err != nil {
			return r, err
		}
		r.envJSON = string(raw)
	}
	if b.GetConfigJson() != "" {
		if err := json.Unmarshal([]byte(b.GetConfigJson()), &r.cfg); err != nil {
			return r, errBadRequest("configJson: " + err.Error())
		}
	}
	if len(r.cfg.ModelRoutes) > 0 && string(r.cfg.ModelRoutes) != "null" {
		if err := json.Unmarshal(r.cfg.ModelRoutes, &r.routes); err != nil {
			return r, errBadRequest("configJson.modelRoutes: " + err.Error())
		}
	}
	return r, nil
}

func (w backendWriter) providerKey(ctx context.Context, id int64) (string, error) {
	if id == 0 {
		return "", nil
	}
	resp, err := w.p.providers().List(ctx, &llm_provider_svc.ListProvidersRequest{})
	if err != nil {
		return "", err
	}
	for _, p := range resp.Items {
		if p.ID == id {
			return p.ProviderKey, nil
		}
	}
	return "", errNotFound{kind: agentrewire.CtlKind_CTL_KIND_PROVIDER, id: id}
}

func (w backendWriter) modelKey(ctx context.Context, id int64) (string, error) {
	if id == 0 {
		return "", nil
	}
	svc := w.p.providers()
	providers, err := svc.List(ctx, &llm_provider_svc.ListProvidersRequest{})
	if err != nil {
		return "", err
	}
	for _, p := range providers.Items {
		models, err := svc.ListModels(ctx, &llm_provider_svc.ListModelsRequest{ID: p.ID})
		if err != nil {
			return "", err
		}
		for _, m := range models.Items {
			if m.ID == id {
				return m.ModelKey, nil
			}
		}
	}
	return "", errNotFound{kind: agentrewire.CtlKind_CTL_KIND_MODEL, id: id}
}

// deviceID 把 --device（已配对设备的名字或指纹，空 = 本机）解析成设备指纹。
func (w backendWriter) deviceID(ctx context.Context, device string) (string, error) {
	device = strings.TrimSpace(device)
	if device == "" || strings.HasPrefix(device, "sha256:") {
		return device, nil
	}
	var views []string
	if dir := w.p.devices(); dir != nil {
		list, err := dir.List(ctx)
		if err != nil {
			return "", err
		}
		for _, v := range list {
			if v != nil && v.Name == device && !slices.Contains(views, string(v.DaemonFingerprint)) {
				views = append(views, string(v.DaemonFingerprint))
			}
		}
	}
	switch len(views) {
	case 0:
		return "", errBadRequest(fmt.Sprintf("device %q not found (use a paired device name or its sha256: fingerprint)", device))
	case 1:
		return views[0], nil
	default:
		return "", errBadRequest(fmt.Sprintf("device %q is ambiguous; use one of the fingerprints: %s", device, strings.Join(views, ", ")))
	}
}
