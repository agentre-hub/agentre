package department_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
)

// 生效引擎配置的来源（spec 2026-11-25 决策 3：成员 › 所在部门 › 上级部门 › 账号默认）。
const (
	EngineSourceMember         = "member"
	EngineSourceDepartment     = "department"
	EngineSourceAccountDefault = "account_default"
)

// EffectiveEngine 一次继承链解析的结果：生效的配置，以及它是从哪一级来的。
type EffectiveEngine struct {
	Profile *agent_backend_entity.EngineProfile
	Source  string
	// Department 是 Source == department 时提供这份配置的部门，其余为 nil。
	Department *department_entity.Department
}

// View 把解析结果投影成界面用的 DTO；nil 结果投影成 nil（无处可用）。
func (e *EffectiveEngine) View() *EffectiveEngineView {
	if e == nil || e.Profile == nil {
		return nil
	}
	v := &EffectiveEngineView{
		EngineProfileID:   e.Profile.ID,
		EngineProfileName: e.Profile.Name,
		RuntimeType:       e.Profile.Type,
		Source:            e.Source,
	}
	if e.Department != nil {
		v.SourceDepartmentID = e.Department.ID
		v.SourceDepartmentName = e.Department.Name
	}
	return v
}

func (s *departmentSvc) ResolveDepartmentEngine(ctx context.Context, departmentID int64) (*EffectiveEngine, error) {
	visited := map[int64]bool{}
	for id := departmentID; id > 0 && !visited[id]; {
		visited[id] = true
		d, err := department_repo.Department().Find(ctx, id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			break
		}
		own, err := s.ownEngine(ctx, d)
		if err != nil || own != nil {
			return own, err
		}
		id = d.ParentID
	}
	return s.accountDefaultEngine(ctx)
}

func (s *departmentSvc) GetDepartmentEngine(ctx context.Context, req *GetDepartmentEngineRequest) (*DepartmentEngineView, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	d, err := s.requireDepartment(ctx, req.DepartmentID)
	if err != nil {
		return nil, err
	}
	return s.engineView(ctx, d)
}

func (s *departmentSvc) SetDepartmentEngine(ctx context.Context, req *SetDepartmentEngineRequest) (*DepartmentEngineView, error) {
	if req == nil || req.EngineProfileID < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	d, err := s.requireDepartment(ctx, req.DepartmentID)
	if err != nil {
		return nil, err
	}
	if req.EngineProfileID > 0 {
		p, err := s.engineProfiles.Find(ctx, req.EngineProfileID)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, i18n.NewError(ctx, code.EngineProfileNotFound)
		}
	}
	if err := department_repo.Department().UpdateDefaultEngineProfile(ctx, d.ID, req.EngineProfileID); err != nil {
		return nil, err
	}
	d.DefaultEngineProfileID = req.EngineProfileID
	return s.engineView(ctx, d)
}

func (s *departmentSvc) requireDepartment(ctx context.Context, id int64) (*department_entity.Department, error) {
	d, err := department_repo.Department().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, i18n.NewError(ctx, code.DepartmentNotFound)
	}
	return d, nil
}

// engineView：Inherited 是「跟随上级」那一项会落到的配置（顶层部门即账号默认），
// Effective 是部门自己的设置生效时的那份，否则就是 Inherited。
func (s *departmentSvc) engineView(ctx context.Context, d *department_entity.Department) (*DepartmentEngineView, error) {
	inherited, err := s.ResolveDepartmentEngine(ctx, d.ParentID)
	if err != nil {
		return nil, err
	}
	effective, err := s.ownEngine(ctx, d)
	if err != nil {
		return nil, err
	}
	if effective == nil {
		effective = inherited
	}
	return &DepartmentEngineView{
		DepartmentID:    d.ID,
		EngineProfileID: d.DefaultEngineProfileID,
		Inherited:       inherited.View(),
		Effective:       effective.View(),
	}, nil
}

// ownEngine 部门自己设的成员默认引擎；没设、或指向的配置已不在（被删后引用会被清掉，
// 这里只是不让一条迟到的引用挡住回落）时为 nil，调用方回落到下一级。
func (s *departmentSvc) ownEngine(ctx context.Context, d *department_entity.Department) (*EffectiveEngine, error) {
	if d.DefaultEngineProfileID <= 0 {
		return nil, nil
	}
	p, err := s.engineProfiles.Find(ctx, d.DefaultEngineProfileID)
	if err != nil || p == nil {
		return nil, err
	}
	return &EffectiveEngine{Profile: p, Source: EngineSourceDepartment, Department: d}, nil
}

func (s *departmentSvc) accountDefaultEngine(ctx context.Context) (*EffectiveEngine, error) {
	p, err := s.engineProfiles.FindAccountDefault(ctx)
	if err != nil || p == nil {
		return nil, err
	}
	return &EffectiveEngine{Profile: p, Source: EngineSourceAccountDefault}, nil
}
