package orgtool_svc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
)

// execWriteTool 把已批准的写工具分发到 department_svc / agent_svc。每分支只解参数 + 调
// deps 接口,不写业务逻辑;update 类先 Load 现值再 merge(沿用未给字段)。错误原样上抛。
func (s *orgtoolSvc) execWriteTool(ctx context.Context, ref agenttool.Ref, tool string, rawArgs json.RawMessage) (string, error) {
	switch tool {
	case "org_create_department":
		return s.createDepartment(ctx, rawArgs)
	case "org_update_department":
		return s.updateDepartment(ctx, rawArgs)
	case "org_delete_department":
		return s.deleteDepartment(ctx, rawArgs)
	case "org_create_agent":
		return s.createAgent(ctx, ref, rawArgs)
	case "org_update_agent":
		return s.updateAgent(ctx, rawArgs)
	case "org_delete_agent":
		return s.deleteAgent(ctx, rawArgs)
	default:
		return "", fmt.Errorf("未知写工具: %s", tool)
	}
}

func (s *orgtoolSvc) createDepartment(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args createDepartmentArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", err
	}
	resp, err := s.deptCommand.Create(ctx, &department_svc.CreateDepartmentRequest{
		Name:        args.Name,
		Description: args.Description,
		ParentID:    args.ParentID,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已创建部门「%s」(id=%d)", resp.Item.Name, resp.Item.ID), nil
}

func (s *orgtoolSvc) updateDepartment(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args updateDepartmentArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", err
	}
	cur, err := s.loadDepartment(ctx, args.ID)
	if err != nil {
		return "", err
	}
	name := cur.Name
	if args.Name != "" {
		name = args.Name
	}
	description := cur.Description
	if args.Description != nil {
		description = *args.Description
	}
	leadAgentID := cur.LeadAgentID
	if args.LeadAgentID != nil {
		leadAgentID = *args.LeadAgentID
	}
	if _, err := s.deptCommand.Update(ctx, &department_svc.UpdateDepartmentRequest{
		ID:          args.ID,
		Name:        name,
		Description: description,
		Icon:        cur.Icon,
		AccentColor: cur.AccentColor,
		LeadAgentID: leadAgentID,
	}); err != nil {
		return "", err
	}
	if args.ParentID != nil && *args.ParentID != cur.ParentID {
		if _, err := s.deptCommand.Move(ctx, &department_svc.MoveDepartmentRequest{
			ID:          args.ID,
			NewParentID: *args.ParentID,
		}); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("已更新部门「%s」(id=%d)", name, args.ID), nil
}

func (s *orgtoolSvc) deleteDepartment(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args deleteDepartmentArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", err
	}
	if _, err := s.deptCommand.Delete(ctx, &department_svc.DeleteDepartmentRequest{
		ID:       args.ID,
		Strategy: args.Strategy,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("已删除部门(id=%d)", args.ID), nil
}

func (s *orgtoolSvc) createAgent(ctx context.Context, ref agenttool.Ref, rawArgs json.RawMessage) (string, error) {
	var args createAgentArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", err
	}
	backendID := args.BackendID
	if backendID == 0 {
		caller, err := s.agentLookup.Find(ctx, ref.AgentID)
		if err != nil {
			return "", err
		}
		if caller == nil {
			return "", fmt.Errorf("找不到调用者 agent(id=%d)", ref.AgentID)
		}
		backendID = caller.AgentBackendID
	}
	resp, err := s.agentCommand.Create(ctx, &agent_svc.CreateAgentRequest{
		Name:           args.Name,
		Description:    args.Description,
		DepartmentID:   args.DepartmentID,
		ParentAgentID:  args.ParentAgentID,
		AgentBackendID: backendID,
		Prompt:         args.Prompt,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已创建 agent「%s」(id=%d)", resp.Item.Name, resp.Item.ID), nil
}

func (s *orgtoolSvc) updateAgent(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args updateAgentArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", err
	}
	cur, err := s.loadAgent(ctx, args.ID)
	if err != nil {
		return "", err
	}
	name := cur.Name
	if args.Name != "" {
		name = args.Name
	}
	description := cur.Description
	if args.Description != nil {
		description = *args.Description
	}
	prompt := cur.Prompt
	if args.Prompt != nil {
		prompt = args.Prompt
	}
	if _, err := s.agentCommand.Update(ctx, &agent_svc.UpdateAgentRequest{
		ID:          args.ID,
		Name:        name,
		Description: description,
		AvatarColor: cur.AvatarColor,
		AvatarIcon:  cur.AvatarIcon,
		Prompt:      prompt,
		// 这个工具不改执行目标列表/技能授权(R15/R15e)，原样带回去——不重复校验，
		// 也不把 Agent 现有的多档配置折叠成一档。
		ExecTargets: execTargetInputsFromItem(cur.ExecTargets),
		Tools:       cur.Tools,
	}); err != nil {
		return "", err
	}
	// 挂载位置: department / parentAgent 互斥(agent_svc.Move 语义),只给其一时另一个传 0。
	moveDept := args.DepartmentID != nil && *args.DepartmentID != cur.DepartmentID
	moveParent := args.ParentAgentID != nil && *args.ParentAgentID != cur.ParentAgentID
	if moveDept || moveParent {
		move := &agent_svc.MoveAgentRequest{ID: args.ID}
		if args.DepartmentID != nil {
			move.NewDepartmentID = *args.DepartmentID
		}
		if args.ParentAgentID != nil {
			move.NewParentAgentID = *args.ParentAgentID
		}
		if _, err := s.agentCommand.Move(ctx, move); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("已更新 agent「%s」(id=%d)", name, args.ID), nil
}

func (s *orgtoolSvc) deleteAgent(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args deleteAgentArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", err
	}
	if _, err := s.agentCommand.Delete(ctx, &agent_svc.DeleteAgentRequest{ID: args.ID}); err != nil {
		return "", err
	}
	return fmt.Sprintf("已删除 agent(id=%d)", args.ID), nil
}

// loadDepartment 从 org 全量里按 id 找部门现值(merge 沿用未给字段需要)。
func (s *orgtoolSvc) loadDepartment(ctx context.Context, id int64) (*department_svc.DepartmentItem, error) {
	resp, err := s.orgQuery.Load(ctx, &department_svc.LoadOrgRequest{})
	if err != nil {
		return nil, err
	}
	for _, d := range resp.Departments {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, fmt.Errorf("找不到部门(id=%d)", id)
}

// loadAgent 从 org 全量里按 id 找 agent 现值(merge 沿用未给字段需要)。
func (s *orgtoolSvc) loadAgent(ctx context.Context, id int64) (*department_svc.AgentItem, error) {
	resp, err := s.orgQuery.Load(ctx, &department_svc.LoadOrgRequest{})
	if err != nil {
		return nil, err
	}
	for _, a := range resp.Agents {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("找不到 agent(id=%d)", id)
}

// execTargetInputsFromItem 把 AgentItem.ExecTargets(读侧 DTO)转成
// agent_svc.UpdateAgentRequest.ExecTargets(写侧 DTO)，原样透传——这个工具从不改
// 执行目标列表，只是把现值带回写请求里（agent_svc.Update 要求整份替换）。
func execTargetInputsFromItem(items []department_svc.AgentExecTargetItem) []agent_svc.ExecTargetInputDTO {
	out := make([]agent_svc.ExecTargetInputDTO, 0, len(items))
	for _, it := range items {
		out = append(out, agent_svc.ExecTargetInputDTO{AgentBackendID: it.AgentBackendID, Skills: it.Skills})
	}
	return out
}
