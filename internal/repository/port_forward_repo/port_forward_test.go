package port_forward_repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/port_forward_entity"
	"github.com/agentre-hub/agentre/internal/repository/port_forward_repo"
)

func setupPortForwardRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, port_forward_repo.PortForwardRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, port_forward_repo.NewPortForward()
}

// TestPortForwardRepo_Create_WritesRowWithTimestamps 覆盖新增：一条映射声明落库时
// 必须带上建行 / 更新时间——列表页按它排序，规格「数据」一节把时间戳列在字段里。
func TestPortForwardRepo_Create_WritesRowWithTimestamps(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `port_forwards`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	row := &port_forward_entity.PortForward{Port: 3000, Name: "dev server", Enabled: true}
	require.NoError(t, repo.Create(ctx, row))
	assert.NotZero(t, row.Createtime, "建行时间必须被填上")
	assert.NotZero(t, row.Updatetime)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_Create_DisabledStaysDisabled 钉住「写进去的就是调用方给的那一位」。
//
// gorm 把实体上的 default 标注当成「零值即未填」的替换值：Enabled 带着 default:1 时,
// 一条 Enabled=false 的新增会被就地改写成 true 再写出去(连调用方手上那个结构体一起
// 改),这张表因此根本插不进一条停用的声明 —— 而且不报错、回读也自洽,没有任何一处
// 会说出这件事。所以判据打在**送进驱动的那几个参数**上,而不只是「有没有 INSERT」。
func TestPortForwardRepo_Create_DisabledStaysDisabled(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `port_forwards`").
		WithArgs(3000, "dev server", false, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	row := &port_forward_entity.PortForward{Port: 3000, Name: "dev server", Enabled: false}
	require.NoError(t, repo.Create(ctx, row))
	assert.False(t, row.Enabled, "调用方交进来的那个结构体被就地改掉了")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_Create_DuplicatePortSurfacesError 覆盖「端口在一台设备下唯一」：
// 库上的 UNIQUE 索引（迁移里的 ux_port_forwards_port）是唯一性的真相源，仓储层只需要
// 不吞掉这个错误——新增第二条同端口的映射必须原样把冲突错误交回调用方，而不是
// 静默成功或换成另一种含糊的失败。
func TestPortForwardRepo_Create_DuplicatePortSurfacesError(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	dup := errors.New("Error 1062 (23000): Duplicate entry '3000' for key 'ux_port_forwards_port'")
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `port_forwards`").
		WillReturnError(dup)
	mock.ExpectRollback()

	err := repo.Create(ctx, &port_forward_entity.PortForward{Port: 3000, Name: "dev server", Enabled: true})
	require.Error(t, err)
	assert.ErrorIs(t, err, dup)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_FindByPort_Found 覆盖端口点查：设备侧 open 判定与新增前的
// 唯一性预检都走它。
func TestPortForwardRepo_FindByPort_Found(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `port_forwards` WHERE port = \\?").
		WithArgs(3000, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "port", "name", "enabled"}).
			AddRow(int64(1), 3000, "dev server", true))

	got, err := repo.FindByPort(ctx, 3000)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(1), got.ID)
	assert.Equal(t, 3000, got.Port)
	assert.True(t, got.Enabled)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_FindByPort_NotFound 覆盖未声明的端口：按 (nil, nil) 返回，
// 调用方据此判定「端口不在声明集内」，而不是把 ErrRecordNotFound 当 I/O 故障。
func TestPortForwardRepo_FindByPort_NotFound(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `port_forwards` WHERE port = \\?").
		WithArgs(9999, 1).
		WillReturnError(gorm.ErrRecordNotFound)

	got, err := repo.FindByPort(ctx, 9999)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_Get_NotFound 覆盖按主键取行的未命中分支。
func TestPortForwardRepo_Get_NotFound(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `port_forwards` WHERE id = \\?").
		WithArgs(int64(42), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	got, err := repo.Get(ctx, 42)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_List_OrdersByID 覆盖列举：整份声明按 id 升序（新增顺序）交回，
// 不按来源拆分——规格「任何一个有权连上这台设备的客户端都能列举...改动落在那台
// 设备上，另一个客户端下次列举就看得到」。
func TestPortForwardRepo_List_OrdersByID(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `port_forwards` ORDER BY id ASC").
		WillReturnRows(sqlmock.NewRows([]string{"id", "port", "name", "enabled"}).
			AddRow(int64(1), 3000, "dev server", true).
			AddRow(int64(2), 8080, "api", false))

	rows, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, 3000, rows[0].Port)
	assert.Equal(t, 8080, rows[1].Port)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_SetEnabled_TogglesOneRow 覆盖启停：只改这一行的 enabled 列，
// 声明本身保留（规格「映射有启用开关。停用的映射保留声明，但访问一律拒绝」）。
func TestPortForwardRepo_SetEnabled_TogglesOneRow(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `port_forwards` SET").
		WithArgs(false, sqlmock.AnyArg(), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	n, err := repo.SetEnabled(ctx, 1, false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_SetEnabled_MissingRowIsNotAnError 覆盖对一条已经不存在的映射
// 启停：返回 0 行、不报错——调用方（重放的停用指令）不应因此收到错误。
func TestPortForwardRepo_SetEnabled_MissingRowIsNotAnError(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `port_forwards` SET").
		WithArgs(true, sqlmock.AnyArg(), int64(404)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	n, err := repo.SetEnabled(ctx, 404, true)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_Delete_RemovesRow 覆盖删除：规格「删除立即生效：正在进行的
// 转发流被关闭」——本仓储只负责把行删掉，流的收尾在 handler 层（任务 3/4）。
func TestPortForwardRepo_Delete_RemovesRow(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `port_forwards` WHERE id = \\?").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	n, err := repo.Delete(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPortForwardRepo_Delete_MissingRowIsNotAnError 覆盖重复删除：行已经不在时删掉
// 零行、不报错，删除必须幂等。
func TestPortForwardRepo_Delete_MissingRowIsNotAnError(t *testing.T) {
	ctx, mock, repo := setupPortForwardRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `port_forwards` WHERE id = \\?").
		WithArgs(int64(404)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	n, err := repo.Delete(ctx, 404)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}
