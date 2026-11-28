package skill_svc

import (
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type SkillPackDTO struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Skills          []string `json:"skills"`
	Source          string   `json:"source"`
	Recommended     bool     `json:"recommended"`
	Installed       bool     `json:"installed"`
	Enabled         bool     `json:"enabled"`
	GloballyEnabled bool     `json:"globallyEnabled"` // CLI 全局启用态(继承判定用)
	// EffectiveEnabled 是最终传给 CLI 的生效态:显式覆盖优先,否则继承全局态;
	// 未安装的推荐包永远为 false。输入框命令提示以此为唯一真相。
	EffectiveEnabled bool `json:"effectiveEnabled"`
}
type SkillCatalogDTO struct {
	Packs []SkillPackDTO `json:"packs"`
}

// SkillCommandDTO 是输入框可直接补全的 backend-native Skill 名称。
// Name 不包含 Codex `$` / Claude Code `/` 前缀，前端按 backend 统一添加。
type SkillCommandDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type SkillCommandCatalogDTO struct {
	Commands []SkillCommandDTO `json:"commands"`
}

// Device 一台参与派发的设备对这名成员（不带项目）的判定，及它上报的技能包。
type Device struct {
	Fingerprint devicefp.Carrier
	Name        string
	IsSelf      bool
	// PairedID 本机配对表的行 ID（已配对的 agentred）；0 = 没有可借的连接。
	PairedID int64
	// CanRun 派发规则判它能跑这名成员。
	CanRun bool
	// InstalledPacks 设备上报的该运行时已装技能包；PacksKnown=false 即没有上报。
	InstalledPacks []string
	PacksKnown     bool
}

// MemberSkillCatalogDTO 成员详情「技能」一块：目录按成员生效的引擎配置的运行时取。
type MemberSkillCatalogDTO struct {
	Runtime string `json:"runtime"`
	// Supported 该运行时支持技能包；false 时界面只显示一行原因。
	Supported bool `json:"supported"`
	// SourceDevice 目录读自哪台设备（本机优先，否则第一台能跑它的已配对 agentred）；
	// 空 = 没有能跑它的在线设备，目录只有推荐与授权。
	SourceDevice     devicefp.Carrier `json:"sourceDevice"`
	SourceDeviceName string           `json:"sourceDeviceName"`
	Packs            []SkillPackDTO   `json:"packs"`
	// InvalidGrants 对另一种运行时做的授权：换了运行时后整份失效，界面显示为失效并可一键移除。
	InvalidGrants []agent_entity.AgentSkillItem `json:"invalidGrants"`
	// MissingPacks 授权开着的包在哪些能跑这名成员的设备上没装（照常派过去，只是不带那个包）。
	MissingPacks []MissingPackDTO `json:"missingPacks"`
}

// MissingPackDTO 一个授权开着的包及没装它的设备。
type MissingPackDTO struct {
	PackID  string         `json:"packId"`
	Devices []DeviceRefDTO `json:"devices"`
}

// DeviceRefDTO 一台设备的身份；本机 Name 为空时界面写「本机」。
type DeviceRefDTO struct {
	Fingerprint devicefp.Carrier `json:"fingerprint"`
	Name        string           `json:"name"`
	IsSelf      bool             `json:"isSelf"`
}
