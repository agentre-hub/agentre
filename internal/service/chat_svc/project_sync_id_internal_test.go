package chat_svc

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
)

// TestProjectSyncIDOfSession_ResolvesTheProjectsSyncID 覆盖「远端一轮要报的项目身份
// 是账号级同步标识,不是本地自增 id」。
//
// 对端(agentred)拿本地 project_id 毫无用处 —— 它是另一台机器的主键。之所以要由发起
// 端报而不是让服务端从 cwd 推:日活跃统计按项目分组,而那条通道只上行计数、不上行任何
// 路径。
func TestProjectSyncIDOfSession_ResolvesTheProjectsSyncID(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	project_repo.RegisterProject(project_repo.NewProject())

	mock.ExpectQuery("SELECT \\* FROM `projects`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "sync_id"}).AddRow(9, "01HXproj00000000000000000"))

	got, err := projectSyncIDOfSession(ctx, &chat_entity.Session{ID: 1, ProjectID: 9})
	require.NoError(t, err)
	assert.Equal(t, "01HXproj00000000000000000", got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestProjectSyncIDOfSession_FreeSessionDoesNotQuery 覆盖自由会话(ProjectID = 0):
// 它不属于任何项目,如实报空串,并且**不查库** —— 每一轮都为一次注定落空的查询往返
// 一次数据库,是纯粹的浪费。
func TestProjectSyncIDOfSession_FreeSessionDoesNotQuery(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	project_repo.RegisterProject(project_repo.NewProject())

	got, err := projectSyncIDOfSession(ctx, &chat_entity.Session{ID: 1, ProjectID: 0})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet(), "自由会话不该产生任何查询")
}
