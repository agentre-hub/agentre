// Package agentskill 维护 agent 技能包(= Claude Code plugin)目录:静态推荐 +
// 按 backend 的发现器注册表。leaf 层,不反向 import service。
package agentskill

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// Source 技能包来源。
type Source string

const (
	SourceRecommended Source = "recommended" // agentre 精选、当前未安装
	SourceInstalled   Source = "installed"   // 该 backend 安装命中
	SourceAvailable   Source = "available"   // marketplace 可装、未装
)

// SkillPack 一个技能包(= 一个 Claude Code plugin)。ID = "name@marketplace"。
type SkillPack struct {
	ID              string
	Name            string
	Description     string
	Skills          []string // 包内 skill 名(用于 UI 展开/文案)
	Source          Source
	Recommended     bool
	Installed       bool
	GloballyEnabled bool // CLI 全局是否启用(claude plugin list --json 的 enabled)
}

// Recommended 返回 agentre 精选包(静态,纯函数)。id 用官方 marketplace 全名。
func Recommended() []SkillPack {
	// Recommended 条目不带 Skills 列表(由 Discoverer 在发现时填充)。
	return []SkillPack{
		{ID: "superpowers@claude-plugins-official", Name: "superpowers", Description: "TDD / 调试 / 计划 / 协作", Recommended: true, Source: SourceRecommended},
		{ID: "code-review@claude-plugins-official", Name: "code-review", Description: "提交前自查 diff 与回归", Recommended: true, Source: SourceRecommended},
	}
}

// RecommendedFor 返回适用于某 backend 的静态精选包。不同 CLI 的 marketplace ID
// 不兼容,不能把 Claude Code 官方包推荐给 Codex。
func RecommendedFor(t agent_backend_entity.BackendType) []SkillPack {
	switch t {
	case agent_backend_entity.TypeClaudeCode:
		return Recommended()
	default:
		return nil
	}
}

// DiscoverQuery 发现入参。
type DiscoverQuery struct {
	BackendType agent_backend_entity.BackendType
	CLIPath     string // 定位该 claude 安装(空 = 默认 binary)
}

// Discoverer 按 backend 枚举已安装技能包(消费者侧窄接口)。
type Discoverer interface {
	Discover(ctx context.Context, q DiscoverQuery) ([]SkillPack, error)
}

// SkillCommand is one backend-native command-addressable skill name. Name does
// not include the input prefix (`$` for Codex, `/` for Claude Code).
type SkillCommand struct {
	Name        string
	Description string
}

// CommandDiscoverQuery carries the launch context needed for a CLI to resolve
// its effective user/project/plugin skill set without mutating shared config.
type CommandDiscoverQuery struct {
	BackendType    agent_backend_entity.BackendType
	CLIPath        string
	Cwd            string
	EnabledPlugins map[string]bool
}

// CommandDiscoverer enumerates the skill names the current backend can invoke.
// It is separate from Discoverer because standalone/system skills are not
// configurable plugin packs and must not appear in the organization picker.
type CommandDiscoverer interface {
	DiscoverCommands(ctx context.Context, q CommandDiscoverQuery) ([]SkillCommand, error)
}

var discoverers = map[agent_backend_entity.BackendType]Discoverer{}
var commandDiscoverers = map[agent_backend_entity.BackendType]CommandDiscoverer{}

// RegisterDiscoverer init 注册(仿 runtime/prober);非线程安全,只在 init/bootstrap 调。
func RegisterDiscoverer(t agent_backend_entity.BackendType, d Discoverer) {
	discoverers[t] = d
	if commands, ok := d.(CommandDiscoverer); ok {
		commandDiscoverers[t] = commands
	} else {
		delete(commandDiscoverers, t)
	}
}

// SwapDiscovererForTest 单元测试临时替换发现器,返回 restore 闭包(仿 SwapRuntimeForTest)。
func SwapDiscovererForTest(t agent_backend_entity.BackendType, d Discoverer) func() {
	old, existed := discoverers[t]
	discoverers[t] = d
	return func() {
		if existed {
			discoverers[t] = old
		} else {
			delete(discoverers, t)
		}
	}
}

// DiscovererFor 取某 backend 的发现器。
func DiscovererFor(t agent_backend_entity.BackendType) (Discoverer, bool) {
	d, ok := discoverers[t]
	return d, ok
}

// CommandDiscovererFor returns the backend-native skill command discoverer.
func CommandDiscovererFor(t agent_backend_entity.BackendType) (CommandDiscoverer, bool) {
	d, ok := commandDiscoverers[t]
	return d, ok
}

// SwapCommandDiscovererForTest temporarily replaces command discovery.
func SwapCommandDiscovererForTest(t agent_backend_entity.BackendType, d CommandDiscoverer) func() {
	old, existed := commandDiscoverers[t]
	commandDiscoverers[t] = d
	return func() {
		if existed {
			commandDiscoverers[t] = old
		} else {
			delete(commandDiscoverers, t)
		}
	}
}
