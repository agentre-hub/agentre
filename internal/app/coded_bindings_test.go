package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/data_svc"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/issue_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

type failingIssues struct {
	issue_svc.IssueSvc
	err error
}

func (f *failingIssues) Create(context.Context, *issue_svc.CreateIssueRequest) (*issue_svc.IssueDetail, error) {
	return nil, f.err
}
func (f *failingIssues) Update(context.Context, *issue_svc.UpdateIssueRequest) (*issue_svc.IssueDetail, error) {
	return nil, f.err
}
func (f *failingIssues) Delete(context.Context, int64) error { return f.err }
func (f *failingIssues) Move(context.Context, *issue_svc.MoveIssueRequest) (*issue_svc.IssueDetail, error) {
	return nil, f.err
}
func (f *failingIssues) CreateLabel(context.Context, *issue_svc.LabelRequest) (*issue_svc.LabelDetail, error) {
	return nil, f.err
}
func (f *failingIssues) UpdateLabel(context.Context, *issue_svc.LabelRequest) (*issue_svc.LabelDetail, error) {
	return nil, f.err
}
func (f *failingIssues) DeleteLabel(context.Context, int64) error { return f.err }

type failingSync struct {
	sync_svc.SyncSvc
	err error
}

func (f *failingSync) RestoreLostChange(context.Context, int64) (*sync_svc.RestoreOutcome, error) {
	return nil, f.err
}
func (f *failingSync) RecreateFromLostChange(context.Context, int64) error { return f.err }
func (f *failingSync) DiscardLostChange(context.Context, int64) error      { return f.err }
func (f *failingSync) SyncOnce(context.Context) error                      { return f.err }

// bridgeErrorMatches 报告 err 是否就是 want；want 以空格结尾时只比前缀
// （码固定、文案由 i18n 渲染）。
func bridgeErrorMatches(err error, want string) bool {
	if err == nil {
		return false
	}
	if strings.HasSuffix(want, " ") {
		return strings.HasPrefix(err.Error(), want)
	}
	return err.Error() == want
}

// S5：看板写入 / 同步 / 数据导入导出 / 设备升级的失败都要带着 `agentre-code:<码>`
// 过桥，前端才能分出「文件不是备份」「设备离线」等不同出路。
func TestWriteBindings_CarryTheBusinessCode(t *testing.T) {
	ctx := context.Background()
	a := &App{ctx: ctx}

	issueErr := &httputils.Error{Code: code.IssueNotFound, Msg: "任务不存在"}
	prevIssue := issue_svc.Default()
	issue_svc.SetDefault(&failingIssues{err: issueErr})
	t.Cleanup(func() { issue_svc.SetDefault(prevIssue) })

	devErr := &httputils.Error{Code: code.RemoteDeviceDialFailed, Msg: "连不上设备"}
	prevDev := remote_device_svc.Default()
	remote_device_svc.SetDefault(&upgradeSvcStub{err: devErr})
	t.Cleanup(func() { remote_device_svc.SetDefault(prevDev) })

	syncErr := &httputils.Error{Code: code.ServerUnreachable, Msg: "服务器不可达"}
	sync_svc.SetDefault(&failingSync{err: syncErr})
	t.Cleanup(func() { sync_svc.SetDefault(nil) })

	file := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(file, []byte("not a bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.lastImportPath = file

	cases := map[string]struct {
		want string
		call func() error
	}{
		"IssueCreate": {"agentre-code:18200 任务不存在", func() error { _, err := a.IssueCreate(&IssueCreateRequest{}); return err }},
		"IssueUpdate": {"agentre-code:18200 任务不存在", func() error { _, err := a.IssueUpdate(&IssueUpdateRequest{}); return err }},
		"IssueDelete": {"agentre-code:18200 任务不存在", func() error { return a.IssueDelete(1) }},
		"IssueMove":   {"agentre-code:18200 任务不存在", func() error { _, err := a.IssueMove(&IssueMoveRequest{}); return err }},
		"IssueCreateLabel": {"agentre-code:18200 任务不存在", func() error {
			_, err := a.IssueCreateLabel(&IssueLabelRequest{})
			return err
		}},
		"IssueUpdateLabel": {"agentre-code:18200 任务不存在", func() error {
			_, err := a.IssueUpdateLabel(&IssueLabelRequest{})
			return err
		}},
		"IssueDeleteLabel": {"agentre-code:18200 任务不存在", func() error { return a.IssueDeleteLabel(1) }},
		"ExportData": {"agentre-code:20702 ", func() error {
			_, err := a.ExportData(data_svc.ExportRequest{Scopes: []string{"bogus"}})
			return err
		}},
		"previewImportFile": {"agentre-code:20700 ", func() error {
			_, err := a.previewImportFile(file)
			return err
		}},
		"ApplyImportData": {"agentre-code:20700 ", func() error {
			_, err := a.ApplyImportData(ApplyImportFrontendRequest{})
			return err
		}},
		"RemoteDeviceUpgrade": {"agentre-code:20405 连不上设备", func() error {
			_, err := a.RemoteDeviceUpgrade(1, false)
			return err
		}},
		"SyncRestoreLostChange": {"agentre-code:20301 服务器不可达", func() error {
			_, err := a.SyncRestoreLostChange(1)
			return err
		}},
		"SyncRecreateFromLostChange": {"agentre-code:20301 服务器不可达", func() error { return a.SyncRecreateFromLostChange(1) }},
		"SyncDiscardLostChange":      {"agentre-code:20301 服务器不可达", func() error { return a.SyncDiscardLostChange(1) }},
		"SyncNow":                    {"agentre-code:20301 服务器不可达", func() error { return a.SyncNow() }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.call()
			if !bridgeErrorMatches(err, c.want) {
				t.Fatalf("expected %q across the bridge, got %v", c.want, err)
			}
		})
	}
}

// B1：组织架构的写入失败也要带着 `agentre-code:<码>` 过桥 —— 前端据此说「已经有同名
// Agent」这类具体事实，而不是把后端那句话整页贴出来。服务没有可替换的默认实现，所以
// 让仓储查不到那一行，由真实服务自己产出带码错误。
func TestOrgWriteBindings_CarryTheBusinessCode(t *testing.T) {
	ctx := context.Background()
	a := &App{ctx: ctx}
	ctrl := gomock.NewController(t)

	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	agents.EXPECT().Find(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	prevAgents := agent_repo.Agent()
	agent_repo.RegisterAgent(agents)
	t.Cleanup(func() { agent_repo.RegisterAgent(prevAgents) })

	departments := mock_department_repo.NewMockDepartmentRepo(ctrl)
	departments.EXPECT().Find(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	prevDepartments := department_repo.Department()
	department_repo.RegisterDepartment(departments)
	t.Cleanup(func() { department_repo.RegisterDepartment(prevDepartments) })

	cases := map[string]struct {
		want string
		call func() error
	}{
		"CreateAgent": {"agentre-code:10001 ", func() error {
			_, err := a.CreateAgent(&agent_svc.CreateAgentRequest{})
			return err
		}},
		"UpdateAgent": {"agentre-code:14000 ", func() error {
			_, err := a.UpdateAgent(&agent_svc.UpdateAgentRequest{ID: 9})
			return err
		}},
		"MoveAgent": {"agentre-code:14000 ", func() error {
			_, err := a.MoveAgent(&agent_svc.MoveAgentRequest{ID: 9})
			return err
		}},
		"ReorderAgents": {"agentre-code:10001 ", func() error {
			return a.ReorderAgents(&agent_svc.ReorderAgentsRequest{})
		}},
		"DeleteAgent": {"agentre-code:14000 ", func() error {
			_, err := a.DeleteAgent(&agent_svc.DeleteAgentRequest{ID: 9})
			return err
		}},
		"UploadAgentAvatar": {"agentre-code:14000 ", func() error {
			_, err := a.UploadAgentAvatar(&agent_svc.UploadAvatarRequest{ID: 9})
			return err
		}},
		"DeleteAgentAvatar": {"agentre-code:14000 ", func() error {
			_, err := a.DeleteAgentAvatar(&agent_svc.DeleteAvatarRequest{ID: 9})
			return err
		}},
		"CreateDepartment": {"agentre-code:10001 ", func() error {
			_, err := a.CreateDepartment(&department_svc.CreateDepartmentRequest{})
			return err
		}},
		"UpdateDepartment": {"agentre-code:13000 ", func() error {
			_, err := a.UpdateDepartment(&department_svc.UpdateDepartmentRequest{ID: 9})
			return err
		}},
		"MoveDepartment": {"agentre-code:13000 ", func() error {
			_, err := a.MoveDepartment(&department_svc.MoveDepartmentRequest{ID: 9})
			return err
		}},
		"DeleteDepartment": {"agentre-code:13000 ", func() error {
			_, err := a.DeleteDepartment(&department_svc.DeleteDepartmentRequest{ID: 9})
			return err
		}},
		"ReorderDepartments": {"agentre-code:10001 ", func() error {
			return a.ReorderDepartments(&department_svc.ReorderDepartmentsRequest{})
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.call()
			if !bridgeErrorMatches(err, c.want) {
				t.Fatalf("expected %q across the bridge, got %v", c.want, err)
			}
		})
	}
}
