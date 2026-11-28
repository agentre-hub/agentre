package agent_backend_entity

import (
	"context"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// EngineProfile 引擎配置（spec 2026-11-25 决策 2）：今天的后端去掉「在哪台设备上」与
// 「CLI 在哪」两件事之后剩下的那份「用什么、怎么干活」。一份可被任意多个成员、部门引用；
// 运行时类型创建后不可改（Update 不接 Type）。其中至多一份标为账号默认
// （engine_profiles 上的部分唯一索引）。
//
// 字段与 AgentBackend 同名同义（类型专属设置同样收在 config_json，形状是
// syncwire.AgentBackendConfig）；校验与类型分派复用 AgentBackend 的那一套，经 AsBackend
// 交过去，规则只写一份。
type EngineProfile struct {
	ID                    int64    `gorm:"column:id;primaryKey;autoIncrement"`
	Type                  string   `gorm:"column:type;type:text;not null"`
	Name                  string   `gorm:"column:name;type:text;not null"`
	LLMProviderKey        string   `gorm:"column:llm_provider_key;type:text;not null;default:''"`
	LLMModelKey           string   `gorm:"column:model_key;type:text;not null;default:''"`
	ModelRoutes           string   `gorm:"-"`
	Sandbox               string   `gorm:"-"`
	Approval              string   `gorm:"-"`
	EnvJSON               string   `gorm:"column:env_json;type:text;not null;default:'{}'"`
	ReasoningEffort       string   `gorm:"column:reasoning_effort;type:text;not null;default:''"`
	DefaultPermissionMode string   `gorm:"-"`
	DefaultModel          string   `gorm:"-"`
	OpenClawGatewayURL    string   `gorm:"-"`
	OpenClawAgentID       string   `gorm:"-"`
	OpenClawDefaultModel  string   `gorm:"-"`
	OpenClawSessionMode   string   `gorm:"-"`
	HermesURL             string   `gorm:"-"`
	HermesAuthProvider    string   `gorm:"-"`
	HermesUserID          string   `gorm:"-"`
	ACPCommand            string   `gorm:"-"`
	ACPArgs               []string `gorm:"-"`
	ConfigJSON            string   `gorm:"column:config_json;type:text;not null;default:'{}'"`
	// IsAccountDefault 账号默认：成员与部门链都没指定时用它。永远只有一份活配置带它。
	IsAccountDefault bool  `gorm:"column:is_account_default;type:boolean;not null;default:0"`
	Status           int   `gorm:"column:status;type:int;not null;default:1"`
	Createtime       int64 `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime       int64 `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta 账号级同步元数据。sync_id 也是设备本地凭据槽位的键（决策 12）。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

// TableName 绑定表名。
func (*EngineProfile) TableName() string { return "engine_profiles" }

// IsActive 是否处于启用态。软删的配置仍可被已钉住它的会话读到（按最后的设置继续），
// 但不能再被新会话、成员或部门选中。
func (p *EngineProfile) IsActive() bool { return p != nil && p.Status == consts.ACTIVE }

// AsBackend 把配置摊成运行时与校验消费的 AgentBackend 视图：设备与 CLI 路径为空（它们
// 属于派发结果与设备设置，不属于配置），同步身份是配置自己的——OpenClaw token 槽位
// 因而按配置 sync_id 命名（迁移规则 8）。ID 留零：视图不是 agent_backends 的一行。
func (p *EngineProfile) AsBackend() *AgentBackend {
	if p == nil {
		return nil
	}
	return &AgentBackend{
		Type:                  p.Type,
		Name:                  p.Name,
		LLMProviderKey:        p.LLMProviderKey,
		LLMModelKey:           p.LLMModelKey,
		ModelRoutes:           p.ModelRoutes,
		Sandbox:               p.Sandbox,
		Approval:              p.Approval,
		EnvJSON:               p.EnvJSON,
		ReasoningEffort:       p.ReasoningEffort,
		DefaultPermissionMode: p.DefaultPermissionMode,
		DefaultModel:          p.DefaultModel,
		OpenClawGatewayURL:    p.OpenClawGatewayURL,
		OpenClawAgentID:       p.OpenClawAgentID,
		OpenClawDefaultModel:  p.OpenClawDefaultModel,
		OpenClawSessionMode:   p.OpenClawSessionMode,
		HermesURL:             p.HermesURL,
		HermesAuthProvider:    p.HermesAuthProvider,
		HermesUserID:          p.HermesUserID,
		ACPCommand:            p.ACPCommand,
		ACPArgs:               p.ACPArgs,
		ConfigJSON:            p.ConfigJSON,
		Status:                p.Status,
		Createtime:            p.Createtime,
		Updatetime:            p.Updatetime,
		SyncMeta:              p.SyncMeta,
	}
}

// Kind 返回运行时类型对应的 BackendKind；未知类型返 nil。
func (p *EngineProfile) Kind() BackendKind {
	if p == nil {
		return nil
	}
	return KindFor(BackendType(p.Type))
}

// Check 与 AgentBackend.Check 同一套规则（类型、名称、模型目标自洽、环境变量、模型分级、
// 思考力度、类型专属字段）；nil 报引擎配置不存在。
func (p *EngineProfile) Check(ctx context.Context) error {
	if p == nil {
		return i18n.NewError(ctx, code.EngineProfileNotFound)
	}
	return p.AsBackend().Check(ctx)
}

// MarshalConfig 把类型专属字段收进 ConfigJSON（写库前调用），与 AgentBackend 同形。
func (p *EngineProfile) MarshalConfig() error {
	if p == nil {
		return nil
	}
	b := p.AsBackend()
	if err := b.MarshalConfig(); err != nil {
		return err
	}
	p.ConfigJSON = b.ConfigJSON
	return nil
}

// UnmarshalConfig 把 ConfigJSON 摊回类型专属字段（读库后调用）。坏 JSON 报错。
func (p *EngineProfile) UnmarshalConfig() error {
	if p == nil {
		return nil
	}
	b := &AgentBackend{ConfigJSON: p.ConfigJSON}
	if err := b.UnmarshalConfig(); err != nil {
		return err
	}
	p.ModelRoutes, p.Sandbox, p.Approval = b.ModelRoutes, b.Sandbox, b.Approval
	p.DefaultPermissionMode, p.DefaultModel = b.DefaultPermissionMode, b.DefaultModel
	p.OpenClawGatewayURL, p.OpenClawAgentID = b.OpenClawGatewayURL, b.OpenClawAgentID
	p.OpenClawDefaultModel, p.OpenClawSessionMode = b.OpenClawDefaultModel, b.OpenClawSessionMode
	p.HermesURL, p.HermesAuthProvider, p.HermesUserID = b.HermesURL, b.HermesAuthProvider, b.HermesUserID
	p.ACPCommand, p.ACPArgs = b.ACPCommand, b.ACPArgs
	return nil
}

// runtimeDisplayNames 运行时的产品名，新建引擎配置时没填名称就用它（「新建时默认名称为
// 运行时名」）。产品名不随界面语言变化。
var runtimeDisplayNames = map[BackendType]string{
	TypeBuiltin:     "Builtin",
	TypeClaudeCode:  "Claude Code",
	TypeCodex:       "Codex",
	TypePiAgent:     "Pi Agent",
	TypeOpenClaw:    "OpenClaw",
	TypeHermes:      "Hermes",
	TypeACP:         "ACP",
	TypeAntigravity: "Antigravity",
}

// DefaultEngineProfileName 返回运行时名；未知类型退回类型串本身。
func DefaultEngineProfileName(t string) string {
	if name, ok := runtimeDisplayNames[BackendType(t)]; ok {
		return name
	}
	return t
}
