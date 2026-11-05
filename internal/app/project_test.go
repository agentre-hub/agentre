package app

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
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

// mergePreviewRecordingProjects 只接 MergePreview，其余方法交给内嵌接口。
type mergePreviewRecordingProjects struct {
	project_svc.ProjectSvc
	gotSource, gotTarget int64
	preview              *project_svc.MergePreview
	err                  error
}

func (r *mergePreviewRecordingProjects) MergePreview(_ context.Context, sourceID, targetID int64) (*project_svc.MergePreview, error) {
	r.gotSource, r.gotTarget = sourceID, targetID
	return r.preview, r.err
}

// S4：合并对话框的「合并后保留 / 并入后消失」由这一个只读绑定给出，前端不猜。
// 绑定把两个 id 原样交给服务，再把两边的摘要（名称、父路径、是否账号侧）透给前端。
func TestProjectMergePreview_MapsBothSides(t *testing.T) {
	rec := &mergePreviewRecordingProjects{preview: &project_svc.MergePreview{
		Keep: project_svc.MergePreviewSide{ID: 2, Name: "api", ParentPath: []string{"agentre-server"}, Claimed: true},
		Drop: project_svc.MergePreviewSide{ID: 1, Name: "api", ParentPath: []string{"agentre"}},
	}}
	prev := project_svc.Default()
	project_svc.SetDefault(rec)
	t.Cleanup(func() { project_svc.SetDefault(prev) })

	got, err := (&App{}).ProjectMergePreview(1, 2)

	if err != nil {
		t.Fatalf("expected the binding to succeed, got %v", err)
	}
	if rec.gotSource != 1 || rec.gotTarget != 2 {
		t.Fatalf("expected ids (1, 2) passed through, got (%d, %d)", rec.gotSource, rec.gotTarget)
	}
	if got == nil || got.Keep.ID != 2 || got.Keep.Name != "api" || !got.Keep.Claimed ||
		len(got.Keep.ParentPath) != 1 || got.Keep.ParentPath[0] != "agentre-server" {
		t.Fatalf("expected keep side mapped, got %#v", got)
	}
	if got.Drop.ID != 1 || got.Drop.Claimed || len(got.Drop.ParentPath) != 1 || got.Drop.ParentPath[0] != "agentre" {
		t.Fatalf("expected drop side mapped, got %#v", got.Drop)
	}
}

// 预览失败时错误原样交给前端，不交回半份预览。
func TestProjectMergePreview_SurfacesTheError(t *testing.T) {
	rec := &mergePreviewRecordingProjects{err: errors.New("项目不存在")}
	prev := project_svc.Default()
	project_svc.SetDefault(rec)
	t.Cleanup(func() { project_svc.SetDefault(prev) })

	got, err := (&App{}).ProjectMergePreview(1, 404)

	if err == nil || err.Error() != "项目不存在" {
		t.Fatalf("expected the service error to surface unchanged, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected no preview on failure, got %#v", got)
	}
}

// codedFailingProjects 让每条写绑定都以一个带业务码的 cago 错误失败。
type codedFailingProjects struct {
	project_svc.ProjectSvc
	err error
}

func (f *codedFailingProjects) Reorder(context.Context, *project_svc.ReorderProjectsRequest) error {
	return f.err
}

func (f *codedFailingProjects) Delete(context.Context, int64) error { return f.err }

func (f *codedFailingProjects) Merge(context.Context, *project_svc.MergeProjectsRequest) (*project_entity.Project, error) {
	return nil, f.err
}

func (f *codedFailingProjects) MergePreview(context.Context, int64, int64) (*project_svc.MergePreview, error) {
	return nil, f.err
}

// S5：项目页按错误类别给出口（「项目已经不在了」与「还有活跃会话」出路不同），
// 前端只能凭码分得开 —— 而 cago 的 Error() 只剩文案。项目类写绑定的失败要带着
// `agentre-code:<码>` 过桥，与 coded_error.go 那条契约同形。
func TestProjectBindings_CarryTheBusinessCode(t *testing.T) {
	svcErr := &httputils.Error{Code: code.ProjectHasActiveSessions, Msg: "项目下还有未归档会话"}
	want := "agentre-code:18011 项目下还有未归档会话"
	prev := project_svc.Default()
	project_svc.SetDefault(&codedFailingProjects{err: svcErr})
	t.Cleanup(func() { project_svc.SetDefault(prev) })
	a := &App{}

	cases := map[string]func() error{
		"ProjectReorder": func() error {
			return a.ProjectReorder(&ProjectReorderRequest{OrderedIDs: []int64{2, 1}})
		},
		"ProjectDelete": func() error { return a.ProjectDelete(7) },
		"ProjectMerge": func() error {
			_, err := a.ProjectMerge(&ProjectMergeRequest{SourceID: 1, TargetID: 2})
			return err
		},
		"ProjectMergePreview": func() error {
			_, err := a.ProjectMergePreview(1, 2)
			return err
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil || err.Error() != want {
				t.Fatalf("expected %q across the bridge, got %v", want, err)
			}
		})
	}
}
