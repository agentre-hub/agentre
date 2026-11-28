package dispatch_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

//go:generate mockgen -source deps.go -destination mock_dispatch_svc/mock_deps.go -package mock_dispatch_svc

// 下列端口是派发服务读桌面端各处来源的窄口（ISP / DIP）：生产实现在 sources.go，
// 单测注入 mockgen 替身。

// AccountDevice 账号设备清单里的一台设备。
type AccountDevice struct {
	Fingerprint devicefp.Carrier
	Name        string
	// Kind 是 server 报的设备类型：desktop / agentred（老称 daemon）参与派发，其余忽略。
	Kind string
	// Online 是它在账号中继上的在线登记；对桌面端即「那台电脑上的 Agentre 在运行」。
	Online     bool
	LastSeenAt int64
}

// PairedDevice 本机配对表里的一台 agentred。
type PairedDevice struct {
	ID          int64
	Fingerprint devicefp.Carrier
	Name        string
	Online      bool
	LastSeenAt  int64
}

// Devices 有哪些设备、它们此刻是否在线。
type Devices interface {
	// Self 本机指纹；读不出时报错（派发据此当作没有本机可排）。
	Self() (devicefp.Carrier, error)
	// Account 账号设备清单；未登录或拉不到时 ok=false（未登录模式只派本机与已配对的 agentred）。
	Account(ctx context.Context) (devices []AccountDevice, ok bool)
	// Paired 本机配对表里的 agentred。
	Paired(ctx context.Context) ([]PairedDevice, error)
}

// Facts 设备上报的情况，按指纹；读不到的设备不在表里。
type Facts interface {
	Reports(ctx context.Context) map[devicefp.Carrier]*syncwire.DeviceFactsReport
}

// DevicePref 账号设备顺序里的一项及其标签。
type DevicePref struct {
	Fingerprint devicefp.Carrier
	Labels      []string
}

// Prefs 设备偏好：账号设备顺序（按位置排好）与本机优先本地开关。
type Prefs interface {
	Order(ctx context.Context) ([]DevicePref, error)
	LocalFirst(ctx context.Context) (bool, error)
}

// Engines 成员与引擎配置。
type Engines interface {
	// Agent 取成员；不存在回 nil。
	Agent(ctx context.Context, id int64) (*agent_entity.Agent, error)
	// Effective 成员生效的引擎配置（成员 › 部门链 › 账号默认）；都没有回 nil。
	Effective(ctx context.Context, agentID int64) (*agent_backend_entity.EngineProfile, error)
	// Profile 按 id 取一份引擎配置（不看状态）；不存在回 nil。
	Profile(ctx context.Context, id int64) (*agent_backend_entity.EngineProfile, error)
	// LegacyBackend 旧会话钉住的执行目标档（chat_svc 改派发前的写法，随 exec_target_svc
	// 一起删）；不存在回 nil。
	LegacyBackend(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error)
}

// Providers LLM 供应商（取「谁能连到」与名称）。
type Providers interface {
	FindByKey(ctx context.Context, key string) (*llm_provider_entity.LLMProvider, error)
}

// Projects 项目、项目在其它设备上的路径，以及本机会话的工作目录规则。
type Projects interface {
	// Find 取项目；不存在回 nil。
	Find(ctx context.Context, id int64) (*project_entity.Project, error)
	// Locations 项目在其它设备上的路径记录。
	Locations(ctx context.Context, projectID int64) ([]*project_location_entity.ProjectLocation, error)
	// SelfCwd 本机会话的工作目录（项目路径 / Agent 默认目录）。
	SelfCwd(ctx context.Context, sess *chat_entity.Session) (string, error)
}

// Sessions 会话。
type Sessions interface {
	// Find 取会话；不存在回 nil。
	Find(ctx context.Context, id int64) (*chat_entity.Session, error)
}
