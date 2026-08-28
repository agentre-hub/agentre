package orgtool_svc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
)

// newMCPServer 把组织架构工具接到共享的 MCP server 骨架(挂在 gateway /mcp/org/)。
// 令牌签发与校验、JSON-RPC 信封与方法分支、GET 405、bootstrap 窗口 503、工具开关实时
// 校验、写工具审批闸门全部归 internal/pkg/agenttool;本包只提供 schema、org_get 只读
// 处理器与写工具分派。
func (s *orgtoolSvc) newMCPServer() *agenttool.Server {
	return agenttool.NewServer(agenttool.ServerConfig{
		ToolKey:    agenttool.KeyOrg,
		ServerName: "agentre-org",
		Schemas:    orgToolSchemas(),
		// bootstrap 窗口期(RegisterDeps 未执行)未就绪 → 503
		Ready: func() bool { return s.agentLookup != nil },
		LookupAgent: func(ctx context.Context, agentID int64) (*agent_entity.Agent, error) {
			return s.agentLookup.Find(ctx, agentID)
		},
		Read: map[string]agenttool.ReadHandler{"org_get": s.handleOrgGet},
		Write: &agenttool.WriteGate{
			Timeout: func() time.Duration { return s.approvalTimeout },
			Begin: func(ctx context.Context, sessionID int64, requestID, tool string, input map[string]any) (<-chan bool, error) {
				return s.approval.BeginToolApproval(ctx, sessionID, &blocks.ToolApprovalBlock{
					ToolKey: agenttool.KeyOrg, RequestID: requestID, ToolName: tool, ToolInput: input, Status: "pending",
				})
			},
			Finish: func(ctx context.Context, sessionID int64, requestID, status, result string) error {
				return s.approval.FinishToolApproval(ctx, sessionID, requestID, status, result)
			},
			Exec: s.execWriteTool,
		},
	})
}

// handleOrgGet 是 org_get 的只读处理器:取全量组织架构并投影成 LLM 视图。
func (s *orgtoolSvc) handleOrgGet(ctx context.Context, _ agenttool.Ref, _ json.RawMessage) (string, error) {
	resp, err := s.orgQuery.Load(ctx, &department_svc.LoadOrgRequest{})
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(orgGetView(resp))
	return string(b), nil
}

// orgGetDeptView / orgGetAgentView 是 org_get 的 LLM 投影。LoadOrgResponse 是给 Wails
// 前端渲染的 DTO,直接序列化会把 AvatarDataURL(base64 头像,单个可达数百 KB)/avatar 配色/
// prompt/skills/tools/时间戳等对 LLM 无信息量的字段整个灌进 tool result —— 这里只保留
// 组织结构与挂载关系。
type orgGetDeptView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ParentID    int64  `json:"parentId"`
	LeadAgentID int64  `json:"leadAgentId"`
	SortOrder   int    `json:"sortOrder"`
}

type orgGetAgentView struct {
	ID              int64                          `json:"id"`
	Name            string                         `json:"name"`
	Description     string                         `json:"description"`
	SystemBadge     string                         `json:"systemBadge,omitempty"`
	DepartmentID    int64                          `json:"departmentId"`
	DepartmentName  string                         `json:"departmentName"`
	ParentAgentID   int64                          `json:"parentAgentId"`
	ParentAgentName string                         `json:"parentAgentName"`
	Backend         *department_svc.BackendSummary `json:"backend,omitempty"` // BackendSummary 本身是安全子集
	SortOrder       int                            `json:"sortOrder"`
}

// orgGetView 把 LoadOrgResponse 投影成 org_get 的返回视图。
func orgGetView(resp *department_svc.LoadOrgResponse) any {
	depts := make([]orgGetDeptView, 0, len(resp.Departments))
	for _, d := range resp.Departments {
		depts = append(depts, orgGetDeptView{
			ID: d.ID, Name: d.Name, Description: d.Description,
			ParentID: d.ParentID, LeadAgentID: d.LeadAgentID, SortOrder: d.SortOrder,
		})
	}
	agents := make([]orgGetAgentView, 0, len(resp.Agents))
	for _, a := range resp.Agents {
		agents = append(agents, orgGetAgentView{
			ID: a.ID, Name: a.Name, Description: a.Description, SystemBadge: a.SystemBadge,
			DepartmentID: a.DepartmentID, DepartmentName: a.DepartmentName,
			ParentAgentID: a.ParentAgentID, ParentAgentName: a.ParentAgentName,
			Backend: a.Backend, SortOrder: a.SortOrder,
		})
	}
	return map[string]any{"departments": depts, "agents": agents}
}

// orgToolSchemas 返回 org server 暴露的 7 个 MCP 工具 schema。6 个写工具(create/update/delete
// × department/agent)的描述都注明需要用户审批、调用会挂起。
func orgToolSchemas() []any {
	const approvalNote = "（需要用户审批,调用会挂起直至批准/拒绝/超时）"
	return []any{
		map[string]any{
			"name":        "org_get",
			"description": "获取完整组织架构:部门树、agent 及挂载关系(部门/上级 agent)、各 agent 的 backend 摘要。无参数。",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		map[string]any{
			"name":        "org_create_department",
			"description": "新建部门" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "部门名称(必填)"},
					"description": map[string]any{"type": "string", "description": "部门描述"},
					"parentId":    map[string]any{"type": "integer", "description": "上级部门 id(0/省略=顶级部门)"},
				},
			},
		},
		map[string]any{
			"name":        "org_update_department",
			"description": "更新部门;改 parentId 即把部门移动到新的上级下" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id":          map[string]any{"type": "integer", "description": "部门 id(必填)"},
					"name":        map[string]any{"type": "string", "description": "新部门名称"},
					"description": map[string]any{"type": "string", "description": "新部门描述"},
					"leadAgentId": map[string]any{"type": "integer", "description": "负责人 agent id"},
					"parentId":    map[string]any{"type": "integer", "description": "新上级部门 id(改此值即移动部门)"},
				},
			},
		},
		map[string]any{
			"name":        "org_delete_department",
			"description": "删除部门;strategy=reparent 把下级挂到上一层,strategy=cascade 连同子部门/agent 一并删除" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id":       map[string]any{"type": "integer", "description": "部门 id(必填)"},
					"strategy": map[string]any{"type": "string", "enum": []string{"reparent", "cascade"}, "description": "reparent=下级上移;cascade=级联删除"},
				},
			},
		},
		map[string]any{
			"name":        "org_create_agent",
			"description": "新建 agent" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name":          map[string]any{"type": "string", "description": "agent 名称(必填)"},
					"description":   map[string]any{"type": "string", "description": "agent 描述"},
					"departmentId":  map[string]any{"type": "integer", "description": "所属部门 id"},
					"parentAgentId": map[string]any{"type": "integer", "description": "上级 agent id"},
					"backendId":     map[string]any{"type": "integer", "description": "agent 后端 id(0=继承调用者后端)"},
					"prompt":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "系统提示词(逐段)"},
				},
			},
		},
		map[string]any{
			"name":        "org_update_agent",
			"description": "更新 agent;改 departmentId/parentAgentId 即把 agent 移动到新的挂载位置" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id":            map[string]any{"type": "integer", "description": "agent id(必填)"},
					"name":          map[string]any{"type": "string", "description": "新 agent 名称"},
					"description":   map[string]any{"type": "string", "description": "新 agent 描述"},
					"prompt":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "新系统提示词(逐段)"},
					"departmentId":  map[string]any{"type": "integer", "description": "新所属部门 id(改此值即移动)"},
					"parentAgentId": map[string]any{"type": "integer", "description": "新上级 agent id(改此值即移动)"},
				},
			},
		},
		map[string]any{
			"name":        "org_delete_agent",
			"description": "删除 agent" + approvalNote,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "description": "agent id(必填)"},
				},
			},
		},
	}
}
