package app

import (
	"context"
	"errors"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
)

func TestProjectMemberItemsIncludeAgentDisplayFields(t *testing.T) {
	t.Parallel()

	got := toProjectMembers([]*project_svc.ProjectAgentMember{
		{
			AgentID:       5,
			JoinedAt:      100,
			FromProjectID: 1,
			FromName:      "Parent",
			AgentName:     "Builder",
			AvatarColor:   "agent-2",
			AvatarIcon:    "hammer",
			AvatarDataURL: "data:image/png;base64,Yg==",
		},
	}, true)

	if len(got) != 1 {
		t.Fatalf("expected one project member item, got %d", len(got))
	}
	if got[0].AgentName != "Builder" {
		t.Fatalf("expected agentName to be mapped, got %q", got[0].AgentName)
	}
	if got[0].AvatarColor != "agent-2" || got[0].AvatarIcon != "hammer" || got[0].AvatarDataURL == "" {
		t.Fatalf("expected avatar fields to be mapped, got %#v", got[0])
	}
	if !got[0].Inherited {
		t.Fatal("expected inherited flag to be preserved")
	}
}

// R10：项目树的「未配置」角标与基本页签的「指定…」入口都要靠这个字段驱动，
// toProjectItem 必须把 project_entity.Project.LocalPathMissing 透给前端 DTO。
func TestToProjectItemIncludesLocalPathMissing(t *testing.T) {
	t.Parallel()

	got := toProjectItem(&project_entity.Project{
		ID: 9, Name: "agentre-hub", LocalPathMissing: true,
	})

	if got == nil {
		t.Fatal("expected a project item")
	}
	if !got.LocalPathMissing {
		t.Fatal("expected localPathMissing to be mapped from the entity")
	}

	configured := toProjectItem(&project_entity.Project{
		ID: 10, Name: "agentre", Path: "/tmp/agentre", LocalPathMissing: false,
	})
	if configured.LocalPathMissing {
		t.Fatal("expected localPathMissing=false for a configured project")
	}
}

// localPathRecordingProjects 只关心「本机路径是被指定了还是被清掉了」，其余方法交给
// 内嵌接口（碰到别的直接 nil panic）。
type localPathRecordingProjects struct {
	project_svc.ProjectSvc
	clearedIDs []int64
	setPaths   []string
	err        error
}

func (r *localPathRecordingProjects) ClearLocalPath(_ context.Context, id int64) (*project_entity.Project, error) {
	r.clearedIDs = append(r.clearedIDs, id)
	if r.err != nil {
		return nil, r.err
	}
	return &project_entity.Project{ID: id, Name: "agentre-hub", LocalPathMissing: true}, nil
}

func (r *localPathRecordingProjects) SetLocalPath(_ context.Context, id int64, path string) (*project_entity.Project, error) {
	r.setPaths = append(r.setPaths, path)
	return &project_entity.Project{ID: id, Path: path}, nil
}

func registerLocalPathRecordingProjects(t *testing.T, rec *localPathRecordingProjects) {
	t.Helper()
	prev := project_svc.Default()
	project_svc.SetDefault(rec)
	t.Cleanup(func() { project_svc.SetDefault(prev) })
}

// 本机路径此前在界面上只进不出：唯一的绑定 ProjectSetLocalPath 走的是
// project_svc.SetLocalPath，而它按设计拒绝空路径（ProjectInvalidPath）。服务层早就有
// ClearLocalPath，缺的是把它交给前端的这一个绑定。
//
// 清掉路径走的必须是 ClearLocalPath，不是「SetLocalPath 传空串」——空路径在那一条上
// 仍然是一个错误。
func TestProjectClearLocalPath_DelegatesToClearLocalPath(t *testing.T) {
	rec := &localPathRecordingProjects{}
	registerLocalPathRecordingProjects(t, rec)

	got, err := (&App{}).ProjectClearLocalPath(21)

	if err != nil {
		t.Fatalf("expected the binding to succeed, got %v", err)
	}
	if len(rec.clearedIDs) != 1 || rec.clearedIDs[0] != 21 {
		t.Fatalf("expected ClearLocalPath to be called once for project 21, got %v", rec.clearedIDs)
	}
	if len(rec.setPaths) != 0 {
		t.Fatalf("clearing must not go through SetLocalPath, got %v", rec.setPaths)
	}
	if got == nil || got.ID != 21 || !got.LocalPathMissing || got.Path != "" {
		t.Fatalf("expected the cleared project item back, got %#v", got)
	}
}

// 绑定层只做 parse → svc → return：清不掉时错误原样交给前端，不在这里吞掉，也不
// 交回一个半成品的项目。
func TestProjectClearLocalPath_SurfacesTheError(t *testing.T) {
	rec := &localPathRecordingProjects{err: errors.New("项目不存在")}
	registerLocalPathRecordingProjects(t, rec)

	got, err := (&App{}).ProjectClearLocalPath(404)

	if err == nil || err.Error() != "项目不存在" {
		t.Fatalf("expected the service error to surface unchanged, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected no project item on failure, got %#v", got)
	}
}
