package canonical

import (
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/diff"
)

// WriteContentByteCap 是 FileWrite.Content 字节上限。超过后截断并标 Truncated=true,
// 避免 GB 级文件撑爆 Wails event 序列化。各 runtime translator + 重放路径共用。
const WriteContentByteCap = 64 * 1024

// FromToolUse 把 tool_use block 的 (toolName, input) 翻译成 canonical 表示。
// 用于 chat_svc 重放路径(LoadSession 时从持久化的 tool_use 实体重建 canonical);
// runtime translator 的 live 路径直接构造,不走这条。
//
// 命中:返回 (CanonicalTool, true)。未命中(普通工具如 Bash / Read):(nil, false)。
func FromToolUse(toolName string, input map[string]any) (CanonicalTool, bool) {
	switch toolName {
	case "Write":
		if fw, ok := fileWriteFromWriteInput(input, "file_path"); ok {
			return fw, true
		}
	case "write":
		// pi agent 的 write 工具:{path, content},与 claudecode Write 同语义,
		// 只是路径键名不同。
		if fw, ok := fileWriteFromWriteInput(input, "path"); ok {
			return fw, true
		}
	case "Edit":
		payload := diff.FromEdit(input)
		if len(payload.Files) == 0 {
			return nil, false
		}
		replaceAll, _ := input["replace_all"].(bool)
		patches := PatchesFromDiff(payload)
		if replaceAll {
			for i := range patches {
				patches[i].ReplaceAll = true
			}
		}
		return FileEdit{Files: patches}, true
	case "MultiEdit":
		return fileEditFromEditList(diff.FromMultiEdit(input))
	case "edit":
		// pi agent 的 edit 工具:{path, edits:[{oldText,newText}]} —— 一次调用可带
		// 多段替换,形状对应 claudecode MultiEdit 而非 Edit,故走同一条串联路径。
		return fileEditFromEditList(diff.FromPiEdit(input))
	case "file_change":
		payload, ok := diff.FromFileChange(input)
		if !ok || len(payload.Files) == 0 {
			return nil, false
		}
		return FileEdit{Files: PatchesFromDiff(payload)}, true
	case "update_plan":
		if pu, ok := planUpdateFromUpdatePlanInput(input); ok {
			return pu, true
		}
	case "TodoWrite":
		// claudecode 独有工具:todos:[{id,content,status}]。canonical 此前没有
		// 这个分支,识别只活在 claudecode/translator.go 里,live emit 与 replay
		// 两条路径各认一套。迁入这里让两条路径共用同一份识别。
		if pu, ok := todoWriteFromInput(input); ok {
			return pu, true
		}
	}
	if IsAgentSpawnToolName(toolName) {
		if as, ok := AgentSpawnFromInput(input); ok {
			return as, true
		}
	}
	return nil, false
}

// IsAgentSpawnToolName 不同 claudecode CLI 版本对 subagent 派遣工具有两种命名:
// 旧版叫 "Task",新版(pkg/claudecode/testdata/stream_subagent.jsonl)叫 "Agent"。
// 大小写不敏感双名匹配镜像 main 分支前端 SUBAGENT_TOOL_NAMES = {"agent","task"} 行为。
func IsAgentSpawnToolName(name string) bool {
	switch strings.ToLower(name) {
	case "task", "agent":
		return true
	}
	return false
}

// AgentSpawnFromInput 从 Task/Agent 工具 raw input 提取 AgentSpawn 静态字段
// (description/subagent_type/prompt/model);运行时累计态由 SubagentStarted/Progress/Done
// 经 SubagentStateBlock 维护,不在这里填。三字段(description/subagent_type/prompt)全空返 (zero, false);
// model 字段缺失时为空,不参与 zero 判断。
func AgentSpawnFromInput(input map[string]any) (AgentSpawn, bool) {
	description, _ := input["description"].(string)
	subagentType, _ := input["subagent_type"].(string)
	prompt, _ := input["prompt"].(string)
	model, _ := input["model"].(string)
	if description == "" && subagentType == "" && prompt == "" {
		return AgentSpawn{}, false
	}
	return AgentSpawn{
		TaskDescription: description,
		SubagentType:    subagentType,
		Prompt:          prompt,
		Model:           model,
	}, true
}

func planUpdateFromUpdatePlanInput(input map[string]any) (PlanUpdate, bool) {
	rawPlan, ok := input["plan"].([]any)
	if !ok || len(rawPlan) == 0 {
		return PlanUpdate{}, false
	}
	steps := make([]PlanStep, 0, len(rawPlan))
	for _, raw := range rawPlan {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		step, _ := m["step"].(string)
		if strings.TrimSpace(step) == "" {
			continue
		}
		status, _ := m["status"].(string)
		steps = append(steps, PlanStep{
			Step:   step,
			Status: normalizePlanStepStatus(status),
		})
	}
	if len(steps) == 0 {
		return PlanUpdate{}, false
	}
	return PlanUpdate{Steps: steps}, true
}

// todoWriteFromInput 把 claudecode 独有的 TodoWrite 工具 input
// (todos:[{id,content,status}])降级到 PlanUpdate。Status 原样透传
// (claudecode 用 "in_progress" 等 snake_case,canonical enum 文案对齐留给上层),
// 与 update_plan 路径的 normalizePlanStepStatus 不同 —— 两个工具的 wire 形状本
// 就不同,共用一份归一化反而会掩盖差异。
func todoWriteFromInput(input map[string]any) (PlanUpdate, bool) {
	todosRaw, _ := input["todos"].([]any)
	if len(todosRaw) == 0 {
		return PlanUpdate{}, false
	}
	steps := make([]PlanStep, 0, len(todosRaw))
	for _, t := range todosRaw {
		todo, ok := t.(map[string]any)
		if !ok {
			continue
		}
		id, _ := todo["id"].(string)
		content, _ := todo["content"].(string)
		status, _ := todo["status"].(string)
		steps = append(steps, PlanStep{
			ID:     id,
			Step:   content,
			Status: PlanStepStatus(status),
		})
	}
	if len(steps) == 0 {
		return PlanUpdate{}, false
	}
	return PlanUpdate{Steps: steps}, true
}

func normalizePlanStepStatus(status string) PlanStepStatus {
	const legacyCancelledStatus = "cancelled" //nolint:misspell // Accept legacy/British spelling from older tool payloads.

	switch status {
	case string(StepInProgress), "in_progress":
		return StepInProgress
	case string(StepCompleted), "complete":
		return StepCompleted
	case string(StepCancelled), legacyCancelledStatus:
		return StepCancelled
	default:
		return StepPending
	}
}

// fileEditFromEditList 把「一次调用多段替换」串成的 diff.Payload 降级到 FileEdit。
// diff.FromMultiEdit / FromPiEdit 即使 edits 为空也会返单 File(0 hunks),这种空
// patch 走 raw 路径更合适 —— 前端不需要为空 diff 起 DiffCard。
func fileEditFromEditList(payload diff.Payload) (CanonicalTool, bool) {
	if len(payload.Files) == 0 {
		return nil, false
	}
	totalHunks := 0
	for _, f := range payload.Files {
		totalHunks += len(f.Hunks)
	}
	if totalHunks == 0 {
		return nil, false
	}
	return FileEdit{Files: PatchesFromDiff(payload)}, true
}

// fileWriteFromWriteInput 把「整文件写入」工具的 input 降级到 FileWrite。
// pathKey 是各后端 wire 上的路径键名(claudecode Write: file_path;pi write: path)。
func fileWriteFromWriteInput(input map[string]any, pathKey string) (FileWrite, bool) {
	path, _ := input[pathKey].(string)
	content, ok := input["content"].(string)
	if !ok {
		return FileWrite{}, false
	}
	bytes := len(content)
	truncated := false
	if bytes > WriteContentByteCap {
		content = content[:WriteContentByteCap]
		truncated = true
	}
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n")
		if !strings.HasSuffix(content, "\n") {
			lines++
		}
	}
	return FileWrite{
		Path:      path,
		Content:   content,
		Lines:     lines,
		Bytes:     bytes,
		Truncated: truncated,
	}, true
}

// PatchesFromDiff 把 diff.Payload 降级到 canonical.FileEditPatch 列表。
// 字段一一对应(diff.Op 与 canonical.DiffOp 同字符串值;diff.Kind 与
// canonical.FileChangeKind 同字符串值)。runtime translator + 重放路径共用。
func PatchesFromDiff(p diff.Payload) []FileEditPatch {
	out := make([]FileEditPatch, 0, len(p.Files))
	for _, f := range p.Files {
		patch := FileEditPatch{
			Path:       f.Path,
			Kind:       FileChangeKind(string(f.Kind)),
			Plus:       f.Plus,
			Minus:      f.Minus,
			Truncated:  f.Truncated,
			ReplaceAll: f.ReplaceAll,
		}
		patch.Hunks = make([]DiffHunk, 0, len(f.Hunks))
		for _, h := range f.Hunks {
			ch := DiffHunk{
				OldStart: h.OldStart,
				OldLines: h.OldLines,
				NewStart: h.NewStart,
				NewLines: h.NewLines,
				Header:   h.Header,
			}
			ch.Lines = make([]DiffLine, 0, len(h.Lines))
			for _, ln := range h.Lines {
				ch.Lines = append(ch.Lines, DiffLine{
					Op:   DiffOp(string(ln.Op)),
					Old:  ln.Old,
					New:  ln.New,
					Text: ln.Text,
				})
			}
			patch.Hunks = append(patch.Hunks, ch)
		}
		out = append(out, patch)
	}
	return out
}
