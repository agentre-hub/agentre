package agent_backend_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
)

type engineProfileMocks struct {
	profiles  *mock_agent_backend_repo.MockEngineProfileRepo
	agents    *mock_agent_repo.MockAgentRepo
	depts     *mock_department_repo.MockDepartmentRepo
	providers *mock_llm_provider_repo.MockLLMProviderRepo
	tx        *recordingTx
}

// recordingTx 是不连库的 TxRunner：在同一个 ctx 上直接调用回调，并记下调用次数，
// 用来断言「这几笔写入在一个事务里」。
type recordingTx struct{ runs int }

func (r *recordingTx) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	r.runs++
	return fn(ctx)
}

func setupEngineProfileTest(t *testing.T) (context.Context, engineProfileMocks, *engineProfileSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := engineProfileMocks{
		profiles:  mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl),
		agents:    mock_agent_repo.NewMockAgentRepo(ctrl),
		depts:     mock_department_repo.NewMockDepartmentRepo(ctrl),
		providers: mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl),
		tx:        &recordingTx{},
	}
	agent_backend_repo.RegisterEngineProfile(m.profiles)
	agent_repo.RegisterAgent(m.agents)
	department_repo.RegisterDepartment(m.depts)
	llm_provider_repo.RegisterLLMProvider(m.providers)
	svc := &engineProfileSvc{
		now:      func() int64 { return 1700000000000 },
		tx:       m.tx,
		backends: &agentBackendSvc{secrets: keychain.NewMemory()},
	}
	return context.Background(), m, svc
}

func requireSvcCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

func activeProfile(id int64, typ string, isDefault bool) *agent_backend_entity.EngineProfile {
	return &agent_backend_entity.EngineProfile{
		ID: id, Type: typ, Name: agent_backend_entity.DefaultEngineProfileName(typ),
		EnvJSON: "{}", ModelRoutes: "{}", IsAccountDefault: isDefault, Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01P"},
	}
}

func TestEngineProfileSvc_Create(t *testing.T) {
	convey.Convey("新建引擎配置", t, func() {
		ctx, m, svc := setupEngineProfileTest(t)

		convey.Convey("Given no account default yet When the first profile is created without a name Then it takes the runtime name and becomes the account default", func() {
			m.profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(nil, nil)
			var created *agent_backend_entity.EngineProfile
			m.profiles.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p *agent_backend_entity.EngineProfile) error {
				created = p
				p.ID = 1
				return nil
			})

			resp, err := svc.Create(ctx, &CreateEngineProfileRequest{Type: string(agent_backend_entity.TypeClaudeCode)})
			require.NoError(t, err)
			assert.Equal(t, "Claude Code", created.Name)
			assert.True(t, created.IsAccountDefault)
			assert.True(t, resp.Item.IsAccountDefault)
			assert.True(t, resp.Item.Active)
		})

		convey.Convey("Given an account default exists When another profile with the same name is created Then it is allowed and is not the default", func() {
			m.profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(activeProfile(1, "codex", true), nil)
			var created *agent_backend_entity.EngineProfile
			m.profiles.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p *agent_backend_entity.EngineProfile) error {
				created = p
				return nil
			})

			_, err := svc.Create(ctx, &CreateEngineProfileRequest{Type: "codex", Name: "Codex", Sandbox: "read-only"})
			require.NoError(t, err)
			assert.False(t, created.IsAccountDefault)
			assert.Equal(t, "read-only", created.Sandbox)
		})

		convey.Convey("Given an unknown runtime type When created Then it is rejected and nothing is written", func() {
			_, err := svc.Create(ctx, &CreateEngineProfileRequest{Type: "nope", Name: "x"})
			requireSvcCode(t, err, code.AgentBackendInvalidType)
		})

		convey.Convey("Given a bound provider that does not exist When created Then it is rejected and nothing is written", func() {
			m.providers.EXPECT().FindByKey(gomock.Any(), "missing").Return(nil, nil)
			_, err := svc.Create(ctx, &CreateEngineProfileRequest{Type: "claudecode", LLMProviderKey: "missing"})
			requireSvcCode(t, err, code.AgentBackendLLMProviderNotFound)
		})
	})
}

func TestEngineProfileSvc_Update(t *testing.T) {
	convey.Convey("更新引擎配置", t, func() {
		ctx, m, svc := setupEngineProfileTest(t)

		convey.Convey("Given an existing codex profile When updated Then the settings change but the runtime type and the default mark stay", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(3)).Return(activeProfile(3, "codex", true), nil)
			var saved *agent_backend_entity.EngineProfile
			m.profiles.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p *agent_backend_entity.EngineProfile) error {
				saved = p
				return nil
			})

			resp, err := svc.Update(ctx, &UpdateEngineProfileRequest{ID: 3, Name: "Codex 快", ReasoningEffort: "low", Approval: "never"})
			require.NoError(t, err)
			assert.Equal(t, "codex", saved.Type)
			assert.Equal(t, "Codex 快", saved.Name)
			assert.Equal(t, "low", saved.ReasoningEffort)
			assert.Equal(t, "never", saved.Approval)
			assert.True(t, saved.IsAccountDefault)
			assert.Equal(t, int64(1700000000000), saved.Updatetime)
			assert.Equal(t, "Codex 快", resp.Item.Name)
		})

		convey.Convey("Given the profile is gone When updated Then it reports not found", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(3)).Return(nil, nil)
			_, err := svc.Update(ctx, &UpdateEngineProfileRequest{ID: 3, Name: "x"})
			requireSvcCode(t, err, code.EngineProfileNotFound)
		})

		convey.Convey("Given a blank name When updated Then it is rejected and nothing is written", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(3)).Return(activeProfile(3, "codex", false), nil)
			_, err := svc.Update(ctx, &UpdateEngineProfileRequest{ID: 3, Name: "  "})
			requireSvcCode(t, err, code.InvalidParameter)
		})
	})
}

func TestEngineProfileSvc_Delete(t *testing.T) {
	convey.Convey("删除引擎配置", t, func() {
		ctx, m, svc := setupEngineProfileTest(t)

		convey.Convey("Given the account default When deleted Then it is refused and nothing is written", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(1)).Return(activeProfile(1, "claudecode", true), nil)
			_, err := svc.Delete(ctx, &DeleteEngineProfileRequest{ID: 1})
			requireSvcCode(t, err, code.EngineProfileAccountDefaultUndeletable)
			assert.Zero(t, m.tx.runs)
		})

		convey.Convey("Given members and departments refer to it When deleted Then they fall back to the next level in the same transaction and are reported", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(activeProfile(4, "codex", false), nil)
			m.agents.EXPECT().ListByEngineProfile(gomock.Any(), int64(4)).Return([]*agent_entity.Agent{{ID: 11, Name: "Eva"}}, nil)
			m.depts.EXPECT().ListByDefaultEngineProfile(gomock.Any(), int64(4)).Return([]*department_entity.Department{{ID: 7, Name: "工程部"}}, nil)
			gomock.InOrder(
				m.agents.EXPECT().ClearEngineProfile(gomock.Any(), int64(4)).Return(nil),
				m.depts.EXPECT().ClearDefaultEngineProfile(gomock.Any(), int64(4)).Return(nil),
				m.profiles.EXPECT().Delete(gomock.Any(), int64(4)).Return(nil),
			)

			resp, err := svc.Delete(ctx, &DeleteEngineProfileRequest{ID: 4})
			require.NoError(t, err)
			assert.Equal(t, 1, m.tx.runs)
			assert.Equal(t, []EngineProfileRef{{ID: 11, Name: "Eva"}}, resp.References.Agents)
			assert.Equal(t, []EngineProfileRef{{ID: 7, Name: "工程部"}}, resp.References.Departments)
		})

		convey.Convey("Given clearing the references fails When deleted Then the profile itself is not deleted", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(activeProfile(4, "codex", false), nil)
			m.agents.EXPECT().ListByEngineProfile(gomock.Any(), int64(4)).Return(nil, nil)
			m.depts.EXPECT().ListByDefaultEngineProfile(gomock.Any(), int64(4)).Return(nil, nil)
			m.agents.EXPECT().ClearEngineProfile(gomock.Any(), int64(4)).Return(errors.New("boom"))

			_, err := svc.Delete(ctx, &DeleteEngineProfileRequest{ID: 4})
			assert.EqualError(t, err, "boom")
		})

		convey.Convey("Given the profile is gone When deleted Then it reports not found", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(nil, nil)
			_, err := svc.Delete(ctx, &DeleteEngineProfileRequest{ID: 4})
			requireSvcCode(t, err, code.EngineProfileNotFound)
		})
	})
}

func TestEngineProfileSvc_SetAccountDefault(t *testing.T) {
	convey.Convey("设为账号默认", t, func() {
		ctx, m, svc := setupEngineProfileTest(t)

		convey.Convey("Given an active profile When made the account default Then the repository moves the mark to it", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(activeProfile(4, "codex", false), nil)
			m.profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(activeProfile(1, "claudecode", true), nil)
			m.profiles.EXPECT().SetAccountDefault(gomock.Any(), int64(4)).Return(nil)

			resp, err := svc.SetAccountDefault(ctx, &SetAccountDefaultEngineProfileRequest{ID: 4})
			require.NoError(t, err)
			assert.True(t, resp.Item.IsAccountDefault)
		})

		convey.Convey("Given a deleted profile When made the account default Then it reports not found", func() {
			m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(nil, nil)
			_, err := svc.SetAccountDefault(ctx, &SetAccountDefaultEngineProfileRequest{ID: 4})
			requireSvcCode(t, err, code.EngineProfileNotFound)
		})
	})
}

func TestEngineProfileSvc_Reads(t *testing.T) {
	convey.Convey("读取引擎配置", t, func() {
		ctx, m, svc := setupEngineProfileTest(t)

		convey.Convey("Given a deleted profile a session is still pinned to When read by id Then its last settings come back marked inactive", func() {
			p := activeProfile(4, "codex", false)
			p.Status = consts.DELETE
			p.Sandbox = "read-only"
			m.profiles.EXPECT().FindAnyStatus(gomock.Any(), int64(4)).Return(p, nil)

			resp, err := svc.Get(ctx, &GetEngineProfileRequest{ID: 4})
			require.NoError(t, err)
			assert.False(t, resp.Item.Active)
			assert.Equal(t, "read-only", resp.Item.Sandbox)
		})

		convey.Convey("When listed Then each row carries its direct member count and the default mark", func() {
			m.profiles.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.EngineProfile{
				activeProfile(1, "claudecode", true), activeProfile(2, "codex", false),
			}, nil)
			m.agents.EXPECT().CountByEngineProfiles(gomock.Any(), []int64{1, 2}).Return(map[int64]int64{2: 3}, nil)

			resp, err := svc.List(ctx, &ListEngineProfilesRequest{})
			require.NoError(t, err)
			require.Len(t, resp.Items, 2)
			assert.True(t, resp.Items[0].IsAccountDefault)
			assert.Equal(t, int64(0), resp.Items[0].AgentCount)
			assert.Equal(t, int64(3), resp.Items[1].AgentCount)
		})

		convey.Convey("When the references of a profile are asked Then members and departments that point at it directly are listed", func() {
			m.agents.EXPECT().ListByEngineProfile(gomock.Any(), int64(4)).Return([]*agent_entity.Agent{{ID: 11, Name: "Eva"}}, nil)
			m.depts.EXPECT().ListByDefaultEngineProfile(gomock.Any(), int64(4)).Return(nil, nil)

			refs, err := svc.ListReferences(ctx, &ListEngineProfileReferencesRequest{ID: 4})
			require.NoError(t, err)
			assert.Equal(t, []EngineProfileRef{{ID: 11, Name: "Eva"}}, refs.Agents)
			assert.Empty(t, refs.Departments)
		})
	})
}
