package dispatch_svc

import (
	"context"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/syncwire/dispatch"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func (s *dispatchSvc) Plan(ctx context.Context, req *PlanRequest) (*Plan, error) {
	if req == nil || req.AgentID <= 0 || req.ProjectID < 0 || req.EngineProfileID < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	return s.plan(ctx, *req)
}

func (s *dispatchSvc) Availability(ctx context.Context, agentID int64) (*Plan, error) {
	return s.Plan(ctx, &PlanRequest{AgentID: agentID})
}

func (s *dispatchSvc) ValidateManual(ctx context.Context, req *PlanRequest) error {
	if req == nil || strings.TrimSpace(string(req.ManualDevice)) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	p, err := s.Plan(ctx, req)
	if err != nil {
		return err
	}
	for _, d := range p.Devices {
		if d.Fingerprint == req.ManualDevice && d.Eligible {
			return nil
		}
	}
	return i18n.NewError(ctx, code.DispatchManualDeviceUnavailable)
}

// candidate 是拼 Input 时一台参与设备的全部已知：判定用的事实与界面用的身份。
type candidate struct {
	device   dispatch.Device
	name     string
	pairedID int64
	facts    *syncwire.DeviceFactsReport
}

func (s *dispatchSvc) plan(ctx context.Context, req PlanRequest) (*Plan, error) {
	agent, err := s.Engines.Agent(ctx, req.AgentID)
	if err != nil {
		return nil, operationFailed(ctx, err, zap.Int64("agentId", req.AgentID))
	}
	if agent == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	profile, err := s.engineFor(ctx, req)
	if err != nil {
		return nil, err
	}
	var project *project_entity.Project
	if req.ProjectID > 0 {
		if project, err = s.Projects.Find(ctx, req.ProjectID); err != nil {
			return nil, operationFailed(ctx, err, zap.Int64("projectId", req.ProjectID))
		}
		if project == nil {
			return nil, i18n.NewError(ctx, code.ProjectNotFound)
		}
	}

	self := s.self()
	cands, err := s.candidates(ctx, self)
	if err != nil {
		return nil, err
	}
	in, engine, err := s.input(ctx, req, agent, profile, project, self, cands)
	if err != nil {
		return nil, err
	}
	paths, err := s.projectPaths(ctx, project, self)
	if err != nil {
		return nil, err
	}
	for i := range in.Devices {
		if _, ok := paths[devicefp.Carrier(in.Devices[i].Fingerprint)]; ok {
			in.Devices[i].ProjectPaths = map[string]bool{in.ProjectID: true}
		}
	}

	res := dispatch.Evaluate(in)
	return s.view(res, req, engine, profile, self, cands, paths), nil
}

// engineFor 本会话换用的引擎配置（须是活的），否则成员生效的那份。
func (s *dispatchSvc) engineFor(ctx context.Context, req PlanRequest) (*agent_backend_entity.EngineProfile, error) {
	if req.EngineProfileID > 0 {
		p, err := s.Engines.Profile(ctx, req.EngineProfileID)
		if err != nil {
			return nil, operationFailed(ctx, err, zap.Int64("engineProfileId", req.EngineProfileID))
		}
		if !p.IsActive() {
			return nil, i18n.NewError(ctx, code.EngineProfileNotFound)
		}
		return p, nil
	}
	p, err := s.Engines.Effective(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.EngineProfileRequired)
	}
	return p, nil
}

// self 本机指纹；读不出（钥匙串缺失）时为空 —— 没有可识别的「自己」，本机不参与排序。
func (s *dispatchSvc) self() devicefp.Carrier {
	fp, err := s.Devices.Self()
	if err != nil {
		return ""
	}
	return fp
}

// candidates 参与派发的设备：本机、账号里的桌面端与 agentred、本机配对表里的 agentred。
// 从桌面端派发到 agentred 要求本机配对了它；已配对的以本机配对连接的在线态为准（派发走
// 的就是这条连接）。未登录账号时只有本机与已配对的 agentred。
func (s *dispatchSvc) candidates(ctx context.Context, self devicefp.Carrier) ([]*candidate, error) {
	var out []*candidate
	byFP := map[devicefp.Carrier]*candidate{}
	add := func(c *candidate) {
		byFP[devicefp.Carrier(c.device.Fingerprint)] = c
		out = append(out, c)
	}
	if self != "" {
		add(&candidate{device: dispatch.Device{
			Fingerprint: string(self), Kind: dispatch.KindDesktop, Online: true, AppRunning: true,
		}})
	}
	if account, ok := s.Devices.Account(ctx); ok {
		for _, d := range account {
			if d.Fingerprint == "" {
				continue
			}
			if c := byFP[d.Fingerprint]; c != nil {
				if c.name == "" {
					c.name = d.Name
				}
				continue
			}
			switch d.Kind {
			case string(dispatch.KindDesktop):
				// 账号中继只登记 Agentre 进程：不在线就是那台电脑上的 Agentre 没开。
				add(&candidate{name: d.Name, device: dispatch.Device{
					Fingerprint: string(d.Fingerprint), Kind: dispatch.KindDesktop,
					Online: true, AppRunning: d.Online, LastSeen: millis(d.LastSeenAt),
				}})
			case string(dispatch.KindAgentred), "daemon":
				add(&candidate{name: d.Name, device: dispatch.Device{
					Fingerprint: string(d.Fingerprint), Kind: dispatch.KindAgentred,
					Online: d.Online, LastSeen: millis(d.LastSeenAt), RequiresPairing: true,
				}})
			}
		}
	}
	paired, err := s.Devices.Paired(ctx)
	if err != nil {
		return nil, operationFailed(ctx, err)
	}
	for _, p := range paired {
		if p.Fingerprint == "" || p.Fingerprint == self {
			continue
		}
		device := dispatch.Device{
			Fingerprint: string(p.Fingerprint), Kind: dispatch.KindAgentred,
			Online: p.Online, LastSeen: millis(p.LastSeenAt), RequiresPairing: true, Paired: true,
		}
		c := byFP[p.Fingerprint]
		if c == nil {
			c = &candidate{name: p.Name, device: device}
			add(c)
		}
		if c.name == "" {
			c.name = p.Name
		}
		c.pairedID, c.device = p.ID, device
	}
	return out, nil
}

// input 拼出 dispatch.Input（项目路径另行填）。
func (s *dispatchSvc) input(
	ctx context.Context, req PlanRequest, agent *agent_entity.Agent, profile *agent_backend_entity.EngineProfile,
	project *project_entity.Project, self devicefp.Carrier, cands []*candidate,
) (dispatch.Input, EngineView, error) {
	prefs, err := s.Prefs.Order(ctx)
	if err != nil {
		return dispatch.Input{}, EngineView{}, operationFailed(ctx, err)
	}
	localFirst, err := s.Prefs.LocalFirst(ctx)
	if err != nil {
		return dispatch.Input{}, EngineView{}, operationFailed(ctx, err)
	}
	labels := make(map[devicefp.Carrier][]string, len(prefs))
	order := make([]string, 0, len(prefs))
	for _, p := range prefs {
		labels[p.Fingerprint] = p.Labels
		order = append(order, string(p.Fingerprint))
	}
	reports := s.Facts.Reports(ctx)

	in := dispatch.Input{
		AccountOrder:    order,
		LocalFirst:      localFirst,
		SelfFingerprint: string(self),
		ManualDevice:    string(req.ManualDevice),
		Constraint:      constraintOf(agent.GetExecConstraint()),
	}
	if project != nil {
		in.ProjectID = strconv.FormatInt(project.ID, 10)
		in.ProjectName = project.Name
		in.ProjectDefaultDevice = string(project.DefaultDeviceFingerprint)
	}
	for _, c := range cands {
		fp := devicefp.Carrier(c.device.Fingerprint)
		c.device.Labels = labels[fp]
		c.facts = reports[fp]
		applyFacts(&c.device, c.facts)
		in.Devices = append(in.Devices, c.device)
	}

	engine := EngineView{
		ID: profile.ID, Name: profile.Name, Runtime: profile.Type,
		ModelSource: string(dispatch.ModelNative), ProviderKey: profile.LLMProviderKey, ModelKey: profile.LLMModelKey,
	}
	in.Engine = dispatch.EngineProfile{
		SyncID: profile.SyncID, Name: profile.Name, Runtime: profile.Type, ModelSource: dispatch.ModelNative,
	}
	if key := strings.TrimSpace(profile.LLMProviderKey); key != "" {
		engine.ModelSource = string(dispatch.ModelProvider)
		in.Engine.ModelSource, in.Engine.ProviderKey = dispatch.ModelProvider, key
		prov, err := s.Providers.FindByKey(ctx, key)
		if err != nil {
			return dispatch.Input{}, EngineView{}, operationFailed(ctx, err, zap.String("providerKey", key))
		}
		if prov != nil {
			engine.ProviderName, in.Engine.ProviderName = prov.Name, prov.Name
			if mode, devices := prov.Reach(); mode == llm_provider_entity.ReachDevices {
				reach := dispatch.ProviderReach{}
				for _, d := range devices {
					reach.Devices = append(reach.Devices, string(d))
				}
				in.Providers = map[string]dispatch.ProviderReach{key: reach}
			}
		}
	}
	return in, engine, nil
}

// applyFacts 把设备上报的情况摊进判定用的 Device；没有上报时运行时一项也没有（判没装）。
func applyFacts(d *dispatch.Device, r *syncwire.DeviceFactsReport) {
	if r == nil {
		return
	}
	d.Runtimes = make(map[string]dispatch.RuntimeFact, len(r.Runtimes))
	for _, rt := range r.Runtimes {
		d.Runtimes[rt.Runtime] = dispatch.RuntimeFact{
			Found: rt.Found, Version: rt.Version, BelowMinimum: rt.BelowMinimum,
			NativeLogin: dispatch.NativeLogin(rt.NativeLogin),
		}
	}
	d.ACPResolvable = make(map[string]bool, len(r.ACPEngines))
	for _, e := range r.ACPEngines {
		d.ACPResolvable[e.EngineSyncID] = e.Resolvable
	}
	d.Credentials = make(map[string]bool, len(r.ServiceEngines))
	for _, e := range r.ServiceEngines {
		d.Credentials[e.EngineSyncID] = e.CredentialPresent
	}
}

// projectPaths 项目在每台设备上的路径：本机看项目自己的路径（判据是 LocalPathMissing 状态
// 位），其它设备看项目路径记录里活着的那一行。
func (s *dispatchSvc) projectPaths(ctx context.Context, project *project_entity.Project, self devicefp.Carrier) (map[devicefp.Carrier]string, error) {
	paths := map[devicefp.Carrier]string{}
	if project == nil {
		return paths, nil
	}
	if self != "" && !project.LocalPathMissing && strings.TrimSpace(project.Path) != "" {
		paths[self] = project.Path
	}
	locs, err := s.Projects.Locations(ctx, project.ID)
	if err != nil {
		return nil, operationFailed(ctx, err, zap.Int64("projectId", project.ID))
	}
	for _, l := range locs {
		if l == nil || !l.IsActive() || strings.TrimSpace(l.Path) == "" || l.DeviceFingerprint == self {
			continue
		}
		if _, dup := paths[l.DeviceFingerprint]; !dup {
			paths[l.DeviceFingerprint] = l.Path
		}
	}
	return paths, nil
}

func (s *dispatchSvc) view(
	res dispatch.Result, req PlanRequest, engine EngineView, profile *agent_backend_entity.EngineProfile,
	self devicefp.Carrier, cands []*candidate, paths map[devicefp.Carrier]string,
) *Plan {
	byFP := make(map[string]*candidate, len(cands))
	for _, c := range cands {
		byFP[c.device.Fingerprint] = c
	}
	p := &Plan{
		Found: res.Found, Chosen: devicefp.Carrier(res.Chosen), Engine: engine,
		Devices: make([]*DeviceResult, 0, len(res.Devices)),
	}
	if res.Found {
		p.ChosenBy = string(res.ChosenBy)
	}
	p.ManualUnavailable = req.ManualDevice != "" && (!res.Found || res.ChosenBy != dispatch.ByManual)
	for _, d := range res.Devices {
		fp := devicefp.Carrier(d.Fingerprint)
		c := byFP[d.Fingerprint]
		out := &DeviceResult{
			Fingerprint: fp, Kind: string(d.Kind), IsSelf: fp == self,
			Eligible: d.Eligible, Chosen: res.Found && d.Fingerprint == res.Chosen,
			ProjectPath: paths[fp],
		}
		if c != nil {
			out.Name, out.pairedID = c.name, c.pairedID
			out.skillPacks, out.skillPacksKnown = skillPacksFor(c.facts, profile.Type)
		}
		if !d.Eligible {
			out.Reason = reasonView(d.Reason)
		}
		p.Devices = append(p.Devices, out)
	}
	return p
}

func skillPacksFor(r *syncwire.DeviceFactsReport, runtime string) ([]string, bool) {
	if r == nil {
		return nil, false
	}
	for _, sp := range r.SkillPacks {
		if sp.Runtime == runtime {
			return sp.PackIDs, true
		}
	}
	return []string{}, true
}

func reasonView(r dispatch.Reason) *ReasonView {
	v := &ReasonView{
		Code: string(r.Code), Runtime: r.Runtime, Labels: r.Labels,
		Project: r.Project, Provider: r.Provider, Engine: r.Engine,
	}
	if !r.LastSeen.IsZero() {
		v.LastSeen = r.LastSeen.UnixMilli()
	}
	return v
}

func constraintOf(c agent_entity.ExecConstraint) dispatch.Constraint {
	switch c.Mode {
	case agent_entity.ExecConstraintLabels:
		return dispatch.Constraint{Kind: dispatch.ConstraintLabels, Labels: c.Labels}
	case agent_entity.ExecConstraintDevices:
		out := dispatch.Constraint{Kind: dispatch.ConstraintDevices}
		for _, d := range c.Devices {
			out.Devices = append(out.Devices, string(d))
		}
		return out
	default:
		return dispatch.Constraint{}
	}
}
