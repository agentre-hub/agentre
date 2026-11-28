package agent_entity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// ExecConstraintMode 成员「在哪跑」约束的三种取值（spec 2026-11-25 决策 4）。约束只做
// 过滤，不做排序——顺序属于用户和项目。
const (
	ExecConstraintAny     = "any"     // 任意设备（默认）
	ExecConstraintLabels  = "labels"  // 设备须带全部所选标签
	ExecConstraintDevices = "devices" // 只在这几台设备上
)

// ExecConstraint 成员「在哪跑」约束。Labels 只在 labels 模式下有值，Devices 只在 devices
// 模式下有值（规范指纹）。
type ExecConstraint struct {
	Mode    string             `json:"mode"`
	Labels  []string           `json:"labels,omitempty"`
	Devices []devicefp.Carrier `json:"devices,omitempty"`
}

// GetExecConstraint 读约束。空列 / {} / 坏 JSON / 未知模式都读成「任意设备」——列默认值
// 就是这个意思，读侧不报错。
func (a *Agent) GetExecConstraint() ExecConstraint {
	if a == nil || strings.TrimSpace(a.ExecConstraintJSON) == "" {
		return ExecConstraint{Mode: ExecConstraintAny}
	}
	var c ExecConstraint
	if err := json.Unmarshal([]byte(a.ExecConstraintJSON), &c); err != nil {
		return ExecConstraint{Mode: ExecConstraintAny}
	}
	normalized, ok := normalizeExecConstraint(c)
	if !ok {
		return ExecConstraint{Mode: ExecConstraintAny}
	}
	return normalized
}

// SetExecConstraint 校验并写约束：标签 / 设备去空白、去重；labels 模式至少一个标签，
// devices 模式至少一台设备；任意设备丢掉残留的标签与设备。非法时原值不动。
func (a *Agent) SetExecConstraint(ctx context.Context, c ExecConstraint) error {
	normalized, ok := normalizeExecConstraint(c)
	if !ok {
		return i18n.NewError(ctx, code.EngineExecConstraintInvalid)
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	a.ExecConstraintJSON = string(b)
	return nil
}

func normalizeExecConstraint(c ExecConstraint) (ExecConstraint, bool) {
	switch c.Mode {
	case "", ExecConstraintAny:
		return ExecConstraint{Mode: ExecConstraintAny}, true
	case ExecConstraintLabels:
		labels := dedupeTrimmed(c.Labels)
		return ExecConstraint{Mode: ExecConstraintLabels, Labels: labels}, len(labels) > 0
	case ExecConstraintDevices:
		raw := make([]string, 0, len(c.Devices))
		for _, d := range c.Devices {
			raw = append(raw, string(d))
		}
		var devices []devicefp.Carrier
		for _, d := range dedupeTrimmed(raw) {
			devices = append(devices, devicefp.Carrier(d))
		}
		return ExecConstraint{Mode: ExecConstraintDevices, Devices: devices}, len(devices) > 0
	default:
		return ExecConstraint{}, false
	}
}

func dedupeTrimmed(in []string) []string {
	var out []string
	seen := make(map[string]bool, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// GetSkillGrants 读技能授权（继承 / 强制开 / 强制关三态：不在列表里 = 继承）。坏 JSON
// 读成空。
func (a *Agent) GetSkillGrants() []AgentSkillItem {
	out := []AgentSkillItem{}
	if a == nil || a.SkillGrantsJSON == "" {
		return out
	}
	_ = json.Unmarshal([]byte(a.SkillGrantsJSON), &out)
	if out == nil {
		out = []AgentSkillItem{}
	}
	return out
}

// SetSkillGrants 写技能授权，并记下它们是对哪种运行时做的授权。
func (a *Agent) SetSkillGrants(runtimeType string, items []AgentSkillItem) {
	if items == nil {
		items = []AgentSkillItem{}
	}
	b, _ := json.Marshal(items)
	a.SkillGrantsJSON = string(b)
	a.SkillGrantsRuntime = runtimeType
}

// SkillGrantsFor 按生效引擎配置的运行时类型校验授权：授权时的运行时与它一致才有效，
// 否则整份授权都是失效的（技能包 ID 按运行时区分，Claude 插件与 Codex 对不上），由界面
// 显示为失效并一键移除。
func (a *Agent) SkillGrantsFor(runtimeType string) (valid, invalid []AgentSkillItem) {
	grants := a.GetSkillGrants()
	if len(grants) == 0 {
		return nil, nil
	}
	if a.SkillGrantsRuntime == runtimeType {
		return grants, nil
	}
	return nil, grants
}
