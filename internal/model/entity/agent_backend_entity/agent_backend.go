// Package agent_backend_entity 维护 Agent 后端的充血实体。
//
// 一条 AgentBackend = 一个可被多个 Agent 共享引用的「后端实例」：
//   - Type=TypeBuiltin   走 cago github.com/cago-frame/agents/app/coding，绑定一个 LLMProvider；
//   - Type=TypeClaudeCode  通过 cliagent/claudecode 拉 claude CLI，绑定 anthropic 类型 provider；
//   - Type=TypeCodex     通过 github.com/agentre-ai/agentre/pkg/codex 拉 codex CLI，绑定 openai-response provider。
//
// 不同 type 的字段约束由 BackendKind（kinds.go）分派，entity.Check 不再直接 switch type。
package agent_backend_entity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/pkg/code"
)

// BackendType Agent 后端实现类型。
type BackendType string

const (
	// TypeBuiltin 走 cago app/coding，in-process 跑工具调用。
	TypeBuiltin BackendType = "builtin"
	// TypeClaudeCode 包装本地 claude CLI（cliagent/claudecode）。
	TypeClaudeCode BackendType = "claudecode"
	// TypeCodex 包装本地 codex CLI（github.com/agentre-ai/agentre/pkg/codex），默认走 OpenAI Responses API。
	TypeCodex BackendType = "codex"
	// TypePiAgent 包装本地 pi CLI（@earendil-works/pi-coding-agent RPC mode）。
	TypePiAgent BackendType = "piagent"
	// TypeOpenClaw 通过 OpenClaw Gateway WebSocket RPC protocol 4 运行 agent。
	TypeOpenClaw BackendType = "openclaw"
)

const (
	// OpenClawSessionPerAgentRESession 将每条 AgentRE chat_session 稳定映射到一条
	// OpenClaw session。MVP 只支持这一种模式。
	OpenClawSessionPerAgentRESession = "per-agentre-session"
)

// AgentBackend 一条 Agent 后端配置记录。
type AgentBackend struct {
	ID             int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Type           string `gorm:"column:type;type:text;not null"`
	Name           string `gorm:"column:name;type:text;not null"`
	LLMProviderKey string `gorm:"column:llm_provider_key;type:text;not null;default:''"`
	// LLMModelKey 主绑定目标（ModelTarget）的稳定 ModelKey。
	// 与 LLMProviderKey 组合决定目标模式：
	//   - 两个都空 = native（CLI 自身登录态）；
	//   - ProviderKey 非空且 ModelKey 空 = provider-default（每轮解析当前默认模型）；
	//   - 两个都非空 = fixed-model（解析指定 Model 记录）。
	// 模型存在 / 启用 / 归属校验由 service 层跨 repo 完成（entity 不碰 DB）。
	LLMModelKey string `gorm:"column:model_key;type:text;not null;default:''"`
	// DeviceID is the target machine's canonical device fingerprint. It is the
	// cross-device persisted and synced identity; dispatch resolves it to this
	// installation's paired-row ID only at the local boundary.
	DeviceID string `gorm:"column:device_id;type:text;not null;default:''"`
	CLIPath  string `gorm:"column:cli_path;type:text;not null;default:''"`
	// ModelRoutes 仅 claudecode 使用：`{"OPUS":{"providerKey":"..","modelKey":".."},...}` 子集，
	// 任意 alias 缺省时回落主 LLMProviderKey/LLMModelKey（inherit-main）。
	// 解析 / 序列化分别用 ParseModelRoutes / MarshalModelRoutes（见 kinds.go）。
	ModelRoutes string `gorm:"column:model_routes;type:text;not null;default:'{}'"`
	// Sandbox 仅 codex 使用：read-only / workspace-write / danger-full-access；空 = CLI 默认。
	Sandbox string `gorm:"column:sandbox;type:text;not null;default:''"`
	// Approval 仅 codex 使用：untrusted / on-request / never；空 = never。
	Approval string `gorm:"column:approval;type:text;not null;default:''"`
	// EnvJSON claudecode / codex 共用：`{"K":"V"}` 自定义透传环境变量；保留键拒入。
	EnvJSON string `gorm:"column:env_json;type:text;not null;default:'{}'"`
	// ReasoningEffort 思考力度档位（六档）。空串 = 走模型/CLI 默认；其余取值见 effort.go。
	ReasoningEffort string `gorm:"column:reasoning_effort;type:text;not null;default:''"`
	// DefaultPermissionMode 仅 claudecode 使用：CLI 子进程 spawn 时下发的
	// --permission-mode 值；空串走 pkg/claudecode 默认（acceptEdits）。
	// 取值：'' / default / acceptEdits / plan / bypassPermissions。
	// 其它后端类型必须保持空串，否则 entity.Check 报 InvalidParameter。
	DefaultPermissionMode string `gorm:"column:default_permission_mode;type:text;not null;default:''"`
	// DefaultModel 仅 claudecode 使用：spawn claude 子进程时下发的 --model 值。
	// 只属于 CLI-native（未绑 provider）场景，是独立自由文本（spec 决策 13）。
	// 切换到 Agentre Provider 后保留但忽略该值（执行由 ModelTarget 决定），
	// 切回 native 后恢复使用。空串 = 不下发 --model，走 CLI 默认。其它后端类型必须保持空串。
	DefaultModel string `gorm:"column:default_model;type:text;not null;default:''"`
	// OpenClawGatewayURL 是 OpenClaw Gateway WebSocket 地址。loopback 可使用 ws；
	// 非 loopback 必须使用 wss。认证信息不得出现在 URL 中。
	OpenClawGatewayURL string `gorm:"column:openclaw_gateway_url;type:text;not null;default:''"`
	// OpenClawAgentID 为空时使用 Gateway 默认 agent。
	OpenClawAgentID string `gorm:"column:openclaw_agent_id;type:text;not null;default:''"`
	// OpenClawDefaultModel 为空时使用 OpenClaw agent/session 默认模型。
	OpenClawDefaultModel string `gorm:"column:openclaw_default_model;type:text;not null;default:''"`
	// OpenClawSessionMode MVP 固定为 per-agentre-session。
	OpenClawSessionMode string `gorm:"column:openclaw_session_mode;type:text;not null;default:''"`
	Status              int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime          int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime          int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta 账号级同步元数据（R1，366 行）。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

// TableName 绑定表名。
func (*AgentBackend) TableName() string { return "agent_backends" }

// CLIOverlay is the local projection of one per-device CLI override. Its
// backend_sync_id and fingerprint form the natural key; missing or empty paths
// mean PATH resolution. It deliberately has its own SyncMeta because the
// overlay is a separate account-sync object, not a backend identity field.
type CLIOverlay struct {
	ID                       int64  `gorm:"column:id;primaryKey;autoIncrement"`
	BackendSyncID            string `gorm:"column:backend_sync_id;type:text;not null;default:''"`
	AgentredFingerprint      string `gorm:"column:agentred_fingerprint;type:text;not null;default:''"`
	CLIPath                  string `gorm:"column:cli_path;type:text;not null;default:''"`
	Status                   int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime               int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime               int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*CLIOverlay) TableName() string { return "agent_backend_cli_overlays" }
func (o *CLIOverlay) IsActive() bool  { return o != nil && o.Status == consts.ACTIVE }

// IsActive 是否处于启用态（未被软删除）。
func (b *AgentBackend) IsActive() bool { return b != nil && b.Status == consts.ACTIVE }

// IsBuiltin / IsClaudeCode / IsCodex 类型判断；nil receiver 返回 false。
func (b *AgentBackend) IsBuiltin() bool {
	return b != nil && BackendType(b.Type) == TypeBuiltin
}

func (b *AgentBackend) IsClaudeCode() bool {
	return b != nil && BackendType(b.Type) == TypeClaudeCode
}

func (b *AgentBackend) IsCodex() bool {
	return b != nil && BackendType(b.Type) == TypeCodex
}

func (b *AgentBackend) IsPiAgent() bool {
	return b != nil && BackendType(b.Type) == TypePiAgent
}

func (b *AgentBackend) IsOpenClaw() bool {
	return b != nil && BackendType(b.Type) == TypeOpenClaw
}

// IsLocal DeviceID 为空时为本地模式；nil receiver 返回 false。
func (b *AgentBackend) IsLocal() bool { return b != nil && b.DeviceID == "" }

// IsRemote DeviceID 非空即视为"绑了远端意图"，无论字段值是否可解析为整数；nil receiver 返回 false。
func (b *AgentBackend) IsRemote() bool { return b != nil && b.DeviceID != "" }

// DeviceIDInt is retained only for legacy callers while DeviceID transitions
// from a paired-row ID to a canonical fingerprint. New persisted values never
// use it; dispatch boundaries resolve fingerprints through paired devices.
func (b *AgentBackend) DeviceIDInt() (int64, bool) {
	if b == nil || b.DeviceID == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(b.DeviceID, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// Kind 返回当前 backend 对应的 BackendKind；未知类型返 nil。
func (b *AgentBackend) Kind() BackendKind {
	if b == nil {
		return nil
	}
	return KindFor(BackendType(b.Type))
}

// Check 校验通用字段 + 通过 BackendKind 分派类型独有校验。
//
// 规则：
//   - nil receiver → AgentBackendNotFound
//   - 空 name → InvalidParameter
//   - 未知 type → AgentBackendInvalidType
//   - env_json 反序列化失败 → AgentBackendInvalidEnvJSON
//   - env_json 含保留键 → AgentBackendReservedEnvKey
//   - model_routes 反序列化失败 / 含未知 alias → AgentBackendUnknownAlias
//   - cli_path 在不允许该字段的 kind 上非空 → AgentBackendCLIPathNotAllowed
//   - 其它 kind 独有字段（sandbox / approval / llm_provider_key）由 ValidateExtra 抛
func (b *AgentBackend) Check(ctx context.Context) error {
	if b == nil {
		return i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	if strings.TrimSpace(b.Name) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	kind := b.Kind()
	if kind == nil {
		return i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	if !b.IsOpenClaw() && b.hasOpenClawConfig() {
		return i18n.NewError(ctx, code.InvalidParameter)
	}

	// ModelTarget 自洽性：fixed-model（ModelKey 非空）必须同时钉住 ProviderKey。
	// provider-default / native 的 ModelKey 必须为空。
	if strings.TrimSpace(b.LLMModelKey) != "" && strings.TrimSpace(b.LLMProviderKey) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}

	// env_json：反序列化 + 保留键白名单。
	envMap, err := ParseEnvJSON(b.EnvJSON)
	if err != nil {
		return i18n.NewError(ctx, code.AgentBackendInvalidEnvJSON)
	}
	for k := range envMap {
		if IsReservedEnvKey(k) {
			return i18n.NewError(ctx, code.AgentBackendReservedEnvKey)
		}
	}

	// model_routes：反序列化 + alias 集合限定。空集对所有 kind 都合法。
	routes, err := ParseModelRoutes(b.ModelRoutes)
	if err != nil {
		return i18n.NewError(ctx, code.AgentBackendUnknownAlias)
	}
	if len(routes) > 0 {
		known := kind.KnownAliases()
		if len(known) == 0 {
			return i18n.NewError(ctx, code.AgentBackendUnknownAlias)
		}
		allowed := make(map[string]struct{}, len(known))
		for _, a := range known {
			allowed[a] = struct{}{}
		}
		for alias, route := range routes {
			if _, ok := allowed[alias]; !ok {
				return i18n.NewError(ctx, code.AgentBackendUnknownAlias)
			}
			if strings.TrimSpace(route.ProviderKey) == "" {
				return i18n.NewError(ctx, code.AgentBackendAliasProviderInvalid)
			}
		}
	}

	// cli_path：kind 决定能否非空。
	if !kind.AllowsCLIPath() && strings.TrimSpace(b.CLIPath) != "" {
		return i18n.NewError(ctx, code.AgentBackendCLIPathNotAllowed)
	}

	// reasoning_effort：三种 type 共用同一枚举；codex 的 max 由启动层 clamp，
	// 不在 entity 层拒绝（避免 type 切换时丢值）。
	if !IsValidReasoningEffort(b.ReasoningEffort) {
		return i18n.NewError(ctx, code.AgentBackendInvalidReasoningEffort)
	}

	return kind.ValidateExtra(ctx, b)
}

func (b *AgentBackend) hasOpenClawConfig() bool {
	return strings.TrimSpace(b.OpenClawGatewayURL) != "" ||
		strings.TrimSpace(b.OpenClawAgentID) != "" ||
		strings.TrimSpace(b.OpenClawDefaultModel) != "" ||
		strings.TrimSpace(b.OpenClawSessionMode) != ""
}

// validPermissionModes 与 pkg/claudecode/session.go::validPermissionModes 对齐。
// 空串单独由 caller 处理（claudecode 允许 "" = 走 acceptEdits 默认）。
var validPermissionModes = map[string]struct{}{
	"default":           {},
	"acceptEdits":       {},
	"plan":              {},
	"bypassPermissions": {},
}

// IsValidPermissionMode 校验 default_permission_mode 取值（不含空串语义）。
func IsValidPermissionMode(mode string) bool {
	_, ok := validPermissionModes[mode]
	return ok
}

// NormalizeOpenClawGatewayURL validates and normalizes a Gateway WebSocket URL.
// Plaintext WebSocket is intentionally limited to loopback. User info, query and
// fragment components are rejected so credentials cannot be smuggled into a
// persisted URL or later appear in logs/errors.
// OpenClaw Gateway URL 的各条拒绝原因都有自己的哨兵错误:调用方(service 层)据此
// 给前端结构化的 Code,前端才能用本地化文案说清"错在哪个字段"。全部塌成一个
// InvalidParameter 时,用户只会看到一句没有信息量的「参数错误」。
var (
	ErrOpenClawGatewayURLRequired        = errors.New("openclaw gateway URL is required")
	ErrOpenClawGatewayURLInvalid         = errors.New("openclaw gateway URL is invalid")
	ErrOpenClawGatewayURLScheme          = errors.New("openclaw gateway URL must use ws or wss")
	ErrOpenClawGatewayURLHost            = errors.New("openclaw gateway URL must include a host")
	ErrOpenClawGatewayURLCredentials     = errors.New("openclaw gateway URL cannot contain credentials, query, or fragment")
	ErrOpenClawGatewayURLPlaintextRemote = errors.New("plaintext openclaw gateway URL is limited to loopback")
	ErrOpenClawSessionModeInvalid        = errors.New("openclaw session mode is unsupported")
)

func NormalizeOpenClawGatewayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrOpenClawGatewayURLRequired
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrOpenClawGatewayURLInvalid, err)
	}
	u.Scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return "", ErrOpenClawGatewayURLScheme
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return "", ErrOpenClawGatewayURLHost
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", ErrOpenClawGatewayURLCredentials
	}

	hostname := strings.ToLower(strings.TrimSpace(u.Hostname()))
	loopback := hostname == "localhost"
	if ip := net.ParseIP(hostname); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme == "ws" && !loopback {
		return "", ErrOpenClawGatewayURLPlaintextRemote
	}

	port := u.Port()
	switch {
	case port != "":
		u.Host = net.JoinHostPort(hostname, port)
	case strings.Contains(hostname, ":"):
		u.Host = "[" + hostname + "]"
	default:
		u.Host = hostname
	}
	return u.String(), nil
}
