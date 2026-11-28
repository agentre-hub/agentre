package dispatch_svc

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/device_facts_svc"
	"github.com/agentre-hub/agentre/internal/service/device_pref_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 生产来源：每个端口一份薄适配，只做取数与形状转换，规则都在 plan.go / scope.go。

// SelfCwdFunc 本机会话工作目录规则（project_svc.ResolveSessionCwd），由 bootstrap 注入。
type SelfCwdFunc func(ctx context.Context, sess *chat_entity.Session) (string, error)

// DefaultDeps 生产来源。selfCwd 由 bootstrap 给出（本包不 import project_svc 以免跨域）。
func DefaultDeps(selfCwd SelfCwdFunc) Deps {
	return Deps{
		Devices: devicesSource{}, Facts: factsSource{}, Prefs: prefsSource{}, Engines: enginesSource{},
		Providers: providersSource{}, Projects: projectsSource{selfCwd: selfCwd}, Sessions: sessionsSource{},
	}
}

type devicesSource struct{}

var errNoRemoteDeviceSvc = errors.New("remote device service not initialized")

func (devicesSource) Self() (devicefp.Carrier, error) {
	rds := remote_device_svc.Default()
	if rds == nil {
		return "", errNoRemoteDeviceSvc
	}
	return rds.DeviceFingerprint()
}

func (devicesSource) Account(ctx context.Context) ([]AccountDevice, bool) {
	server := server_svc.Server()
	if server == nil {
		return nil, false
	}
	devices, err := server.ListDevices(ctx)
	if err != nil {
		return nil, false
	}
	out := make([]AccountDevice, 0, len(devices))
	for _, d := range devices {
		out = append(out, AccountDevice{
			Fingerprint: d.Fingerprint, Name: d.Name, Kind: d.Kind, Online: d.Online, LastSeenAt: d.LastSeenAt,
		})
	}
	return out, true
}

func (devicesSource) Paired(ctx context.Context) ([]PairedDevice, error) {
	rds := remote_device_svc.Default()
	if rds == nil {
		return nil, nil
	}
	rows, err := rds.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PairedDevice, 0, len(rows))
	for _, r := range rows {
		if r == nil {
			continue
		}
		out = append(out, PairedDevice{
			ID: r.ID, Fingerprint: r.DaemonFingerprint, Name: r.Name, Online: r.Online, LastSeenAt: r.LastSeenAt,
		})
	}
	return out, nil
}

// NewCachedFacts 让派发读一份设备情况缓存（device_facts_svc.List 只读缓存、从不发 RPC），
// 上报时间随 Facts.CollectedAt 一并交给判定。
func NewCachedFacts(svc device_facts_svc.DeviceFactsSvc) Facts { return cachedFacts{svc: svc} }

// factsSource 读进程默认的设备情况服务（生产装配）；未装配时没有任何情况。
type factsSource struct{}

func (factsSource) Reports(ctx context.Context) map[devicefp.Carrier]*syncwire.DeviceFactsReport {
	svc := device_facts_svc.Default()
	if svc == nil {
		return map[devicefp.Carrier]*syncwire.DeviceFactsReport{}
	}
	return cachedFacts{svc: svc}.Reports(ctx)
}

type cachedFacts struct {
	svc device_facts_svc.DeviceFactsSvc
}

func (c cachedFacts) Reports(ctx context.Context) map[devicefp.Carrier]*syncwire.DeviceFactsReport {
	out := map[devicefp.Carrier]*syncwire.DeviceFactsReport{}
	views, err := c.svc.List(ctx)
	if err != nil {
		return out
	}
	for _, v := range views {
		if v != nil && v.Facts != nil {
			out[v.Fingerprint] = v.Facts
		}
	}
	return out
}

type prefsSource struct{}

func (prefsSource) Order(ctx context.Context) ([]DevicePref, error) {
	rows, err := device_pref_repo.DevicePref().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DevicePref, 0, len(rows))
	for _, r := range rows {
		out = append(out, DevicePref{Fingerprint: r.DeviceFingerprint, Labels: r.Labels()})
	}
	return out, nil
}

func (prefsSource) LocalFirst(ctx context.Context) (bool, error) {
	return device_pref_svc.Default().GetLocalFirst(ctx)
}

type enginesSource struct{}

func (enginesSource) Agent(ctx context.Context, id int64) (*agent_entity.Agent, error) {
	return agent_repo.Agent().Find(ctx, id)
}

func (enginesSource) Effective(ctx context.Context, agentID int64) (*agent_backend_entity.EngineProfile, error) {
	eff, err := agent_svc.Agent().EffectiveEngine(ctx, agentID)
	if err != nil || eff == nil {
		return nil, err
	}
	return eff.Profile, nil
}

func (enginesSource) Profile(ctx context.Context, id int64) (*agent_backend_entity.EngineProfile, error) {
	return agent_backend_repo.EngineProfile().FindAnyStatus(ctx, id)
}

func (enginesSource) LegacyBackend(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error) {
	return agent_backend_repo.AgentBackend().Find(ctx, id)
}

type providersSource struct{}

func (providersSource) FindByKey(ctx context.Context, key string) (*llm_provider_entity.LLMProvider, error) {
	return llm_provider_repo.LLMProvider().FindByKey(ctx, key)
}

type projectsSource struct{ selfCwd SelfCwdFunc }

func (projectsSource) Find(ctx context.Context, id int64) (*project_entity.Project, error) {
	return project_repo.Project().Find(ctx, id)
}

func (projectsSource) Locations(ctx context.Context, projectID int64) ([]*project_location_entity.ProjectLocation, error) {
	return project_location_repo.ProjectLocation().ListByProject(ctx, projectID)
}

func (p projectsSource) SelfCwd(ctx context.Context, sess *chat_entity.Session) (string, error) {
	return p.selfCwd(ctx, sess)
}

type sessionsSource struct{}

func (sessionsSource) Find(ctx context.Context, id int64) (*chat_entity.Session, error) {
	return chat_repo.Session().Find(ctx, id)
}
