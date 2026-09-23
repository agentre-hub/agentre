package blocks

import "encoding/json"

// CtlApprovalInput 是 toolKey = "ctl" 的 ToolApprovalBlock.ToolInput：agrctl 一次写命令的
// 「变更清单卡」（spec 决策 6）。桌面端与控制台共用的卡片组件按这个 JSON 形状渲染。
//
// 前后值由执行者对照当前数据算出，不取自客户端；密钥字段只有 secret 标记，从不带值
// （Hard invariant 1）。
type CtlApprovalInput struct {
	// Command 是完整命令行，密钥 flag 的值已替换为 …。
	Command string              `json:"command"`
	Changes []CtlApprovalChange `json:"changes"`
}

// CtlApprovalChange 是一条资源的变更。
type CtlApprovalChange struct {
	// Op 是 create | update | delete。
	Op string `json:"op"`
	// Kind 是 agent | department | project | provider | model | backend。
	Kind string `json:"kind"`
	// ID 是资源 id；create 时为 0（省略）。
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name"`
	// Fields 是修改的字段；delete 没有。
	Fields []CtlApprovalField `json:"fields,omitempty"`
	// Cascade 只在带 --cascade 的部门删除时出现：连带删除的子部门与 Agent 数。
	Cascade *CtlApprovalCascade `json:"cascade,omitempty"`
}

// CtlApprovalField 是一个字段的前后值。before / after 缺席表示「没有」（create 无 before，
// 清空的字段无 after）；Secret 为 true 时两者都缺席，只表示该密钥被写入。
type CtlApprovalField struct {
	// Field 是资源文档里字段的 JSON 名（如 departmentId）；后端类型独占设置写成 config.<键>。
	Field  string  `json:"field"`
	Before *string `json:"before,omitempty"`
	After  *string `json:"after,omitempty"`
	Secret bool    `json:"secret,omitempty"`
}

// CtlApprovalCascade 是级联删除波及的子资源数量。
type CtlApprovalCascade struct {
	Departments int `json:"departments"`
	Agents      int `json:"agents"`
}

// ToolInput 转成 ToolApprovalBlock.ToolInput 的通用 map（块按 JSON 持久化与推流）。
func (in CtlApprovalInput) ToolInput() map[string]any {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil // 只含字符串 / 整数 / 布尔，不会失败
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// ParseCtlApprovalInput 从 ToolApprovalBlock.ToolInput 还原变更清单。
func ParseCtlApprovalInput(toolInput map[string]any) (CtlApprovalInput, error) {
	var in CtlApprovalInput
	raw, err := json.Marshal(toolInput)
	if err != nil {
		return in, err
	}
	err = json.Unmarshal(raw, &in)
	return in, err
}
