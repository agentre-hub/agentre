package hooktool_svc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/hook_svc"
)

// newMCPServer 把脚本 Hook 工具接到共享的 MCP server 骨架(挂在 gateway /mcp/hook/)。
// 令牌签发与校验、JSON-RPC 信封与方法分支、GET 405、bootstrap 窗口 503、工具开关实时
// 校验、写工具审批闸门全部归 internal/pkg/agenttool;本包只提供 schema、两个只读处理器
// 与写工具分派。
func (s *hooktoolSvc) newMCPServer() *agenttool.Server {
	return agenttool.NewServer(agenttool.ServerConfig{
		ToolKey:    agenttool.KeyHook,
		ServerName: "agentre-hook",
		Schemas:    hookToolSchemas(),
		// bootstrap 窗口期(RegisterDeps 未执行)未就绪 → 503
		Ready: func() bool { return s.agentLookup != nil && s.hooks != nil },
		LookupAgent: func(ctx context.Context, agentID int64) (*agent_entity.Agent, error) {
			return s.agentLookup.Find(ctx, agentID)
		},
		Read: map[string]agenttool.ReadHandler{
			"hook_list": s.handleList,
			"hook_get":  s.handleGet,
		},
		Write: &agenttool.WriteGate{
			Timeout: func() time.Duration { return s.approvalTimeout },
			Begin: func(ctx context.Context, sessionID int64, requestID, tool string, input map[string]any) (<-chan bool, error) {
				return s.approval.BeginToolApproval(ctx, sessionID, &blocks.ToolApprovalBlock{
					ToolKey: agenttool.KeyHook, RequestID: requestID, ToolName: tool, ToolInput: input, Status: "pending",
				})
			},
			Finish: func(ctx context.Context, sessionID int64, requestID, status, result string) error {
				return s.approval.FinishToolApproval(ctx, sessionID, requestID, status, result)
			},
			Exec: s.execWriteTool,
		},
	})
}

// ---- 读工具 ----

// handleList 列全部 hook 的精简视图(无 command 正文 / 无 env 值,省 token)。
func (s *hooktoolSvc) handleList(ctx context.Context, _ agenttool.Ref, _ json.RawMessage) (string, error) {
	resp, err := s.hooks.Load(ctx, &hook_svc.LoadHooksRequest{})
	if err != nil {
		return "", err
	}
	rows := make([]hookListRow, 0, len(resp.Hooks))
	for _, h := range resp.Hooks {
		rows = append(rows, hookListRow{
			ID: h.ID, Name: h.Name, Interpreter: h.Interpreter, InterpreterPath: h.InterpreterPath,
			ScheduleExpr: h.ScheduleExpr, Enabled: h.Enabled,
			LastStatus: h.LastStatus, LastRunAt: h.LastRunAt,
			NextRunAt: h.NextRunAt, TotalCount: h.TotalCount,
		})
	}
	b, _ := json.Marshal(map[string]any{"hooks": rows})
	return string(b), nil
}

// handleGet 取单 hook 全文(command + 脱敏 env)+ 最近事件。
func (s *hooktoolSvc) handleGet(ctx context.Context, _ agenttool.Ref, rawArgs json.RawMessage) (string, error) {
	var args getHookArgs
	_ = json.Unmarshal(rawArgs, &args)
	if args.ID <= 0 {
		return "", agenttool.InvalidParams("缺少 id")
	}
	resp, err := s.hooks.Load(ctx, &hook_svc.LoadHooksRequest{HookID: args.ID, Limit: 20})
	if err != nil {
		return "", err
	}
	var found *hook_svc.HookItem
	for _, h := range resp.Hooks {
		if h.ID == args.ID {
			found = h
			break
		}
	}
	if found == nil {
		return "", errors.New("hook 不存在")
	}
	b, _ := json.Marshal(map[string]any{"hook": found, "events": resp.Events})
	return string(b), nil
}

// hookListRow 是 hook_list 的精简行(剔除 command 正文与 env,省 token)。
type hookListRow struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Interpreter     string `json:"interpreter"`
	InterpreterPath string `json:"interpreterPath"`
	ScheduleExpr    string `json:"scheduleExpr"`
	Enabled         bool   `json:"enabled"`
	LastStatus      string `json:"lastStatus"`
	LastRunAt       int64  `json:"lastRunAt"`
	NextRunAt       int64  `json:"nextRunAt"`
	TotalCount      int64  `json:"totalCount"`
}

// hookToolSchemas 返回 hook server 暴露的 6 个 MCP 工具 schema。4 个写工具(create/update/delete/run)
// 的描述都注明需要用户审批、调用会挂起。
func hookToolSchemas() []any {
	const approvalNote = "（需要用户审批,调用会挂起直至批准/拒绝/超时）"
	const interpDesc = "解释器,取值之一:bash|sh|node|python|pwsh|powershell|cmd(须在运行机器的 PATH 中)"
	const envItems = "环境变量/密钥条目;secret=true 的值读取时脱敏为 ********"
	envSchema := func(desc string) map[string]any {
		return map[string]any{
			"type":        "array",
			"description": desc,
			"items": map[string]any{
				"type":     "object",
				"required": []string{"key", "value"},
				"properties": map[string]any{
					"key":    map[string]any{"type": "string"},
					"value":  map[string]any{"type": "string"},
					"secret": map[string]any{"type": "boolean"},
				},
			},
		}
	}
	return []any{
		map[string]any{
			"name":        "hook_list",
			"description": "列出全部脚本 Hook(名称/解释器/cron 调度/启用状态/上次结果/累计事件数)。无参数。",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "hook_get",
			"description": "取单个 Hook 全文:脚本正文 command、env(密钥脱敏为 ********)、调度、最近产出事件。",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "description": "hook id(必填)"},
				},
			},
		},
		map[string]any{
			"name": "hook_create",
			"description": "新建脚本 Hook。脚本契约:host 注入环境变量 HOOK_STATE(上次返回的 state JSON)/HOOK_NAME/HOOK_ID 及各 env 条目;" +
				"脚本须向 stdout 打印单个 JSON 对象 {\"events\":[{\"title\":\"必填\",\"dedupeKey\":\"可选去重键\",\"payload\":{...}}],\"state\":{...可选,整体替换游标}}。" +
				"退出码非 0 或 stdout 非合法 JSON 即判失败。" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"name", "interpreter", "command", "scheduleExpr"},
				"properties": map[string]any{
					"name":            map[string]any{"type": "string", "description": "Hook 名称(唯一)"},
					"interpreter":     map[string]any{"type": "string", "description": interpDesc},
					"interpreterPath": map[string]any{"type": "string", "description": "可选:解释器二进制的自定义路径;留空则按 interpreter 自动解析(LookPath)。"},
					"command":         map[string]any{"type": "string", "description": "脚本正文(按 interpreter 解释)"},
					"scheduleExpr":    map[string]any{"type": "string", "description": "cron 表达式,如 */5 * * * *"},
					"timezone":        map[string]any{"type": "string", "description": "cron 时区,默认 Asia/Shanghai"},
					"enabled":         map[string]any{"type": "boolean", "description": "是否启用,省略=启用"},
					"env":             envSchema(envItems),
				},
			},
		},
		map[string]any{
			"name":        "hook_update",
			"description": "更新 Hook;仅传要改的字段,未传字段沿用现值。env 传入即整体替换,其中值为 ******** 的条目保留原密钥。" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id":              map[string]any{"type": "integer", "description": "hook id(必填)"},
					"name":            map[string]any{"type": "string"},
					"interpreter":     map[string]any{"type": "string", "description": interpDesc},
					"interpreterPath": map[string]any{"type": "string", "description": "可选:解释器二进制的自定义路径;留空则按 interpreter 自动解析(LookPath)。"},
					"command":         map[string]any{"type": "string"},
					"scheduleExpr":    map[string]any{"type": "string"},
					"timezone":        map[string]any{"type": "string"},
					"enabled":         map[string]any{"type": "boolean"},
					"env":             envSchema("传入即整体替换 env;不传则不动"),
				},
			},
		},
		map[string]any{
			"name":        "hook_delete",
			"description": "删除 Hook" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "description": "hook id(必填)"},
				},
			},
		},
		map[string]any{
			"name": "hook_run",
			"description": "立即执行一次 Hook 脚本。dryRun=true(默认)只回 stdout/解析事件/去重预览,不落库不改 state;" +
				"dryRun=false 真执行并落库。注意:即便 dryRun 也会在本机真实运行该脚本。" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id":     map[string]any{"type": "integer", "description": "hook id(必填)"},
					"dryRun": map[string]any{"type": "boolean", "description": "省略=true(试运行)"},
				},
			},
		},
	}
}
