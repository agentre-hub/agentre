package dispatch_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc/mock_dispatch_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

const (
	selfFP  devicefp.Carrier = "sha256:self"
	boxFP   devicefp.Carrier = "sha256:box"   // 已配对的 agentred
	otherFP devicefp.Carrier = "sha256:other" // 账号里的另一台桌面端
)

// world 是一次测试里桌面端看得到的全部事实与意图；mock 端口都从这里取数。
type world struct {
	selfErr    error
	account    []dispatch_svc.AccountDevice
	accountOK  bool
	paired     []dispatch_svc.PairedDevice
	reports    map[devicefp.Carrier]*syncwire.DeviceFactsReport
	order      []dispatch_svc.DevicePref
	localFirst bool

	agents    map[int64]*agent_entity.Agent
	effective map[int64]*agent_backend_entity.EngineProfile
	profiles  map[int64]*agent_backend_entity.EngineProfile
	legacy    map[int64]*agent_backend_entity.AgentBackend
	providers map[string]*llm_provider_entity.LLMProvider

	projects  map[int64]*project_entity.Project
	locations map[int64][]*project_location_entity.ProjectLocation
	selfCwd   func(sess *chat_entity.Session) (string, error)

	sessions map[int64]*chat_entity.Session
}

// newWorld 默认：本机装着已登录的 Claude Code，配对了一台同样装着 Claude Code 的 agentred
// （ID 7），成员 1 生效 Claude Code 引擎配置，本机优先开着。
func newWorld() *world {
	return &world{
		accountOK: false,
		paired:    []dispatch_svc.PairedDevice{{ID: 7, Fingerprint: boxFP, Name: "机房", Online: true}},
		reports: map[devicefp.Carrier]*syncwire.DeviceFactsReport{
			selfFP: runtimeFacts("claudecode", true, syncwire.NativeLoginLoggedIn),
			boxFP:  runtimeFacts("claudecode", true, syncwire.NativeLoginLoggedIn),
		},
		localFirst: true,
		agents:     map[int64]*agent_entity.Agent{1: {ID: 1, Name: "小王"}},
		effective:  map[int64]*agent_backend_entity.EngineProfile{1: profile(11, "Claude", "claudecode", "")},
		profiles:   map[int64]*agent_backend_entity.EngineProfile{},
		legacy:     map[int64]*agent_backend_entity.AgentBackend{},
		providers:  map[string]*llm_provider_entity.LLMProvider{},
		projects:   map[int64]*project_entity.Project{},
		locations:  map[int64][]*project_location_entity.ProjectLocation{},
		selfCwd: func(sess *chat_entity.Session) (string, error) {
			return "/self/default", nil
		},
		sessions: map[int64]*chat_entity.Session{},
	}
}

var errSelf = errors.New("keychain unavailable")

func profile(id int64, name, typ, providerKey string) *agent_backend_entity.EngineProfile {
	return &agent_backend_entity.EngineProfile{
		ID: id, Name: name, Type: typ, LLMProviderKey: providerKey, Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-" + name},
	}
}

func runtimeFacts(runtime string, found bool, login string) *syncwire.DeviceFactsReport {
	return &syncwire.DeviceFactsReport{Runtimes: []syncwire.RuntimeFact{{Runtime: runtime, Found: found, NativeLogin: login}}}
}

func (w *world) svc(t *testing.T) dispatch_svc.DispatchSvc {
	t.Helper()
	ctrl := gomock.NewController(t)
	devices := mock_dispatch_svc.NewMockDevices(ctrl)
	devices.EXPECT().Self().DoAndReturn(func() (devicefp.Carrier, error) {
		if w.selfErr != nil {
			return "", w.selfErr
		}
		return selfFP, nil
	}).AnyTimes()
	devices.EXPECT().Account(gomock.Any()).DoAndReturn(func(context.Context) ([]dispatch_svc.AccountDevice, bool) {
		return w.account, w.accountOK
	}).AnyTimes()
	devices.EXPECT().Paired(gomock.Any()).DoAndReturn(func(context.Context) ([]dispatch_svc.PairedDevice, error) {
		return w.paired, nil
	}).AnyTimes()

	facts := mock_dispatch_svc.NewMockFacts(ctrl)
	facts.EXPECT().Reports(gomock.Any()).DoAndReturn(func(context.Context) map[devicefp.Carrier]*syncwire.DeviceFactsReport {
		return w.reports
	}).AnyTimes()

	prefs := mock_dispatch_svc.NewMockPrefs(ctrl)
	prefs.EXPECT().Order(gomock.Any()).DoAndReturn(func(context.Context) ([]dispatch_svc.DevicePref, error) {
		return w.order, nil
	}).AnyTimes()
	prefs.EXPECT().LocalFirst(gomock.Any()).DoAndReturn(func(context.Context) (bool, error) {
		return w.localFirst, nil
	}).AnyTimes()

	engines := mock_dispatch_svc.NewMockEngines(ctrl)
	engines.EXPECT().Agent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id int64) (*agent_entity.Agent, error) {
		return w.agents[id], nil
	}).AnyTimes()
	engines.EXPECT().Effective(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id int64) (*agent_backend_entity.EngineProfile, error) {
		return w.effective[id], nil
	}).AnyTimes()
	engines.EXPECT().Profile(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id int64) (*agent_backend_entity.EngineProfile, error) {
		return w.profiles[id], nil
	}).AnyTimes()
	engines.EXPECT().LegacyBackend(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id int64) (*agent_backend_entity.AgentBackend, error) {
		return w.legacy[id], nil
	}).AnyTimes()

	providers := mock_dispatch_svc.NewMockProviders(ctrl)
	providers.EXPECT().FindByKey(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, key string) (*llm_provider_entity.LLMProvider, error) {
		return w.providers[key], nil
	}).AnyTimes()

	projects := mock_dispatch_svc.NewMockProjects(ctrl)
	projects.EXPECT().Find(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id int64) (*project_entity.Project, error) {
		return w.projects[id], nil
	}).AnyTimes()
	projects.EXPECT().Locations(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id int64) ([]*project_location_entity.ProjectLocation, error) {
		return w.locations[id], nil
	}).AnyTimes()
	projects.EXPECT().SelfCwd(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, sess *chat_entity.Session) (string, error) {
		return w.selfCwd(sess)
	}).AnyTimes()

	sessions := mock_dispatch_svc.NewMockSessions(ctrl)
	sessions.EXPECT().Find(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id int64) (*chat_entity.Session, error) {
		return w.sessions[id], nil
	}).AnyTimes()

	return dispatch_svc.New(dispatch_svc.Deps{
		Devices: devices, Facts: facts, Prefs: prefs, Engines: engines,
		Providers: providers, Projects: projects, Sessions: sessions,
	})
}

func location(projectID int64, fp devicefp.Carrier, path string) *project_location_entity.ProjectLocation {
	return &project_location_entity.ProjectLocation{ProjectID: projectID, DeviceFingerprint: fp, Path: path, Status: consts.ACTIVE}
}

func requireCode(t *testing.T, err error, want int) {
	t.Helper()
	require.Error(t, err)
	var appErr *httputils.Error
	require.True(t, errors.As(err, &appErr), "want coded error, got %v", err)
	require.Equal(t, want, appErr.Code)
}

// deviceOf 在结果里按指纹找一台设备。
func deviceOf(p *dispatch_svc.Plan, fp devicefp.Carrier) *dispatch_svc.DeviceResult {
	for _, d := range p.Devices {
		if d.Fingerprint == fp {
			return d
		}
	}
	return nil
}

func fingerprints(p *dispatch_svc.Plan) []devicefp.Carrier {
	out := make([]devicefp.Carrier, 0, len(p.Devices))
	for _, d := range p.Devices {
		out = append(out, d.Fingerprint)
	}
	return out
}
