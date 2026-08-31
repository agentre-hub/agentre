package chat_repo_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

func assertResetActiveSessions(t *testing.T, ctx context.Context, mock sqlmock.Sqlmock, affectedRows int64) {
	t.Helper()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `agent_status`=\\?,`updatetime`=\\? WHERE agent_status IN \\(\\?,\\?\\) AND status = \\? "+
		"AND \\(exec_device_id <= \\? OR exec_device_fingerprint = \\?\\)").
		WithArgs("error", sqlmock.AnyArg(), "running", "waiting", consts.ACTIVE, int64(0), "").
		WillReturnResult(sqlmock.NewResult(0, affectedRows))
	mock.ExpectCommit()

	n, err := chat_repo.NewSession().ResetActiveSessions(ctx)
	assert.NoError(t, err)
	assert.Equal(t, affectedRows, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionRepo_Find(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE id = \\? AND status = \\? ORDER BY `chat_sessions`.`id` LIMIT \\?").
		WithArgs(int64(1), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "title", "agent_status", "last_message_at", "status"}).
			AddRow(1, 7, "hi", "waiting", 1700000000000, consts.ACTIVE))

	got, err := chat_repo.NewSession().Find(ctx, 1)
	assert.NoError(t, err)
	assert.Equal(t, int64(7), got.AgentID)
	assert.True(t, got.NeedsAttention, "needsAttention is derived from agent_status=waiting, not stored as a DB column")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionRepo_ListByAgent(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE .agent_id = \\? AND status = \\?. AND purpose <> \\? ORDER BY last_message_at DESC, id DESC LIMIT \\?").
		WithArgs(int64(7), consts.ACTIVE, chat_entity.SessionPurposeSubagent, 5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "title", "agent_status", "last_message_at", "status"}).
			AddRow(2, 7, "later", "idle", 1700000020000, consts.ACTIVE).
			AddRow(1, 7, "earlier", "idle", 1700000010000, consts.ACTIVE))

	got, err := chat_repo.NewSession().ListByAgent(ctx, 7, 5)
	assert.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, int64(2), got[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionRepo_CountRunningByAgents(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	// 只计入 agent_status='running' 且未软删除的会话 —— 历史 idle 会话不应让前端亮"运行中"呼吸灯。
	// 子 agent 委派会话被 purpose 过滤排除(亮灯却点不进去会留死角)。
	mock.ExpectQuery("SELECT agent_id, COUNT\\(\\*\\) AS n FROM `chat_sessions` WHERE .agent_id IN \\(\\?,\\?\\) AND agent_status = \\? AND status = \\?. AND purpose <> \\? GROUP BY `agent_id`").
		WithArgs(int64(1), int64(2), "running", consts.ACTIVE, chat_entity.SessionPurposeSubagent).
		WillReturnRows(sqlmock.NewRows([]string{"agent_id", "n"}).
			AddRow(1, 2))

	got, err := chat_repo.NewSession().CountRunningByAgents(ctx, []int64{1, 2})
	assert.NoError(t, err)
	assert.Equal(t, 2, got[1])
	assert.Equal(t, 0, got[2], "agent 2 只有 idle 会话，GROUP BY 不返回行，map 缺省读出 0")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionRepo_ListAttentionByAgent(t *testing.T) {
	t.Run("running / waiting / error 三种各 1 行", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE .agent_id = \\? AND status = \\? AND agent_status IN \\(\\?,\\?,\\?\\). AND purpose <> \\? ORDER BY last_message_at DESC, id DESC LIMIT \\?").
			WithArgs(int64(7), consts.ACTIVE, "running", "waiting", "error", chat_entity.SessionPurposeSubagent, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "title", "agent_status", "last_message_at", "status"}).
				AddRow(3, 7, "approve me", "waiting", 1700000030000, consts.ACTIVE).
				AddRow(2, 7, "boom", "error", 1700000020000, consts.ACTIVE).
				AddRow(1, 7, "live", "running", 1700000010000, consts.ACTIVE))

		got, err := chat_repo.NewSession().ListAttentionByAgent(ctx, 7, 20)
		assert.NoError(t, err)
		assert.Len(t, got, 3)
		assert.Equal(t, int64(3), got[0].ID)
		assert.True(t, got[0].NeedsAttention)
		assert.Equal(t, "error", got[1].AgentStatus)
		assert.Equal(t, "running", got[2].AgentStatus)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("全部 idle → 返回空", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE .agent_id = \\? AND status = \\? AND agent_status IN \\(\\?,\\?,\\?\\). AND purpose <> \\? ORDER BY last_message_at DESC, id DESC LIMIT \\?").
			WithArgs(int64(7), consts.ACTIVE, "running", "waiting", "error", chat_entity.SessionPurposeSubagent, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		got, err := chat_repo.NewSession().ListAttentionByAgent(ctx, 7, 20)
		assert.NoError(t, err)
		assert.Empty(t, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSessionRepo_ListByAgentPaged(t *testing.T) {
	t.Run("正常分页 offset>0", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE .agent_id = \\? AND status = \\?. AND purpose <> \\? ORDER BY last_message_at DESC, id DESC LIMIT \\? OFFSET \\?").
			WithArgs(int64(7), consts.ACTIVE, chat_entity.SessionPurposeSubagent, 20, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "title", "agent_status", "last_message_at", "status"}).
				AddRow(22, 7, "session-22", "idle", 1700000220000, consts.ACTIVE).
				AddRow(21, 7, "session-21", "idle", 1700000210000, consts.ACTIVE))

		got, err := chat_repo.NewSession().ListByAgentPaged(ctx, 7, 20, 20)
		assert.NoError(t, err)
		assert.Len(t, got, 2)
		assert.Equal(t, int64(22), got[0].ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("首页 offset=0 不带 OFFSET 子句", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE .agent_id = \\? AND status = \\?. AND purpose <> \\? ORDER BY last_message_at DESC, id DESC LIMIT \\?$").
			WithArgs(int64(7), consts.ACTIVE, chat_entity.SessionPurposeSubagent, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "title", "agent_status", "last_message_at", "status"}).
				AddRow(1, 7, "only", "idle", 1700000010000, consts.ACTIVE))

		got, err := chat_repo.NewSession().ListByAgentPaged(ctx, 7, 0, 20)
		assert.NoError(t, err)
		assert.Len(t, got, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("agent 无任何会话返回空切片", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE .agent_id = \\? AND status = \\?. AND purpose <> \\? ORDER BY last_message_at DESC, id DESC LIMIT \\?").
			WithArgs(int64(99), consts.ACTIVE, chat_entity.SessionPurposeSubagent, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		got, err := chat_repo.NewSession().ListByAgentPaged(ctx, 99, 0, 20)
		assert.NoError(t, err)
		assert.Empty(t, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSessionRepo_ListIDsByAgents(t *testing.T) {
	t.Run("Given multiple agents and active sessions, When listing ids, Then groups active ids by agent in sidebar order", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT agent_id, id FROM `chat_sessions` WHERE .agent_id IN \\(\\?,\\?\\) AND status = \\?. AND purpose <> \\? ORDER BY agent_id ASC, last_message_at DESC, id DESC").
			WithArgs(int64(7), int64(8), consts.ACTIVE, chat_entity.SessionPurposeSubagent).
			WillReturnRows(sqlmock.NewRows([]string{"agent_id", "id"}).
				AddRow(7, 12).
				AddRow(7, 11).
				AddRow(8, 21))

		got, err := chat_repo.NewSession().ListIDsByAgents(ctx, []int64{7, 8})
		assert.NoError(t, err)
		assert.Equal(t, []int64{12, 11}, got[7])
		assert.Equal(t, []int64{21}, got[8])
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given no agent ids, When listing ids, Then it returns empty map without SQL", func(t *testing.T) {
		ctx, _, _ := testutils.Database(t)

		got, err := chat_repo.NewSession().ListIDsByAgents(ctx, nil)
		assert.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestSessionRepo_CountByAgents(t *testing.T) {
	t.Run("批量返回每个 agent 的会话数；缺席 agent 在 map 里读出 0", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT agent_id, COUNT\\(\\*\\) AS n FROM `chat_sessions` WHERE .agent_id IN \\(\\?,\\?,\\?\\) AND status = \\?. AND purpose <> \\? GROUP BY `agent_id`").
			WithArgs(int64(1), int64(2), int64(3), consts.ACTIVE, chat_entity.SessionPurposeSubagent).
			WillReturnRows(sqlmock.NewRows([]string{"agent_id", "n"}).
				AddRow(1, 12).
				AddRow(2, 3))

		got, err := chat_repo.NewSession().CountByAgents(ctx, []int64{1, 2, 3})
		assert.NoError(t, err)
		assert.Equal(t, int64(12), got[1])
		assert.Equal(t, int64(3), got[2])
		assert.Equal(t, int64(0), got[3], "agent 3 无会话，GROUP BY 不返回行，map 缺省读 0")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("空 agentIDs 不发 SQL，直接返回空 map", func(t *testing.T) {
		ctx, _, _ := testutils.Database(t)
		got, err := chat_repo.NewSession().CountByAgents(ctx, nil)
		assert.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestSessionRepo_CountByAgent(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `chat_sessions` WHERE .agent_id = \\? AND status = \\?. AND purpose <> \\?").
		WithArgs(int64(7), consts.ACTIVE, chat_entity.SessionPurposeSubagent).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	got, err := chat_repo.NewSession().CountByAgent(ctx, 7)
	assert.NoError(t, err)
	assert.Equal(t, int64(42), got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionRepo_Create(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `chat_sessions`").
		WithArgs(
			int64(7), "draft", "idle", int64(0), int64(0), "", // agent_id, title, agent_status, last_message_at, last_read_at, provider_session_id
			int64(0),          // project_id
			"",                // cwd —— 不钉工作目录,按老规矩每轮现算
			"",                // purpose
			0, "", "", "", "", // context_window, permission_mode, permission_mode_at_launch, provider_key, model_key
			int64(0), "", int64(0), // exec_device_id, exec_device_fingerprint, event_cursor —— 新建会话默认本机执行、无游标
			int64(0),                                          // exec_agent_backend_id —— 新建会话默认未钉住任何一档
			consts.ACTIVE, sqlmock.AnyArg(), sqlmock.AnyArg(), // status, createtime, updatetime
		).
		WillReturnResult(sqlmock.NewResult(99, 1))
	mock.ExpectCommit()

	s := &chat_entity.Session{AgentID: 7, Title: "draft", AgentStatus: "idle", Status: consts.ACTIVE}
	err := chat_repo.NewSession().Create(ctx, s)
	assert.NoError(t, err)
	assert.Equal(t, int64(99), s.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionRepo_CountActiveByProject(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `chat_sessions`").
		WithArgs(int64(7), consts.ACTIVE, "running", "waiting", chat_entity.SessionPurposeSubagent).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	n, err := chat_repo.NewSession().CountActiveByProject(ctx, 7, []string{"running", "waiting"})
	assert.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionRepo_CountActive(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `chat_sessions`").
		WithArgs(consts.ACTIVE, "running", "waiting", chat_entity.SessionPurposeSubagent).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

	n, err := chat_repo.NewSession().CountActive(ctx, []string{"running", "waiting"})
	assert.NoError(t, err)
	assert.Equal(t, int64(4), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionRepo_MarkRead(t *testing.T) {
	t.Run("ts > current last_read_at 时正常 UPDATE 1 行", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `chat_sessions` SET `last_read_at`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\? AND last_read_at < \\?").
			WithArgs(int64(5000), sqlmock.AnyArg(), int64(7), consts.ACTIVE, int64(5000)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		err := chat_repo.NewSession().MarkRead(ctx, 7, 5000)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ts <= current 时 WHERE 不命中，0 行更新仍算成功", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `chat_sessions` SET `last_read_at`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\? AND last_read_at < \\?").
			WithArgs(int64(1000), sqlmock.AnyArg(), int64(7), consts.ACTIVE, int64(1000)).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()

		err := chat_repo.NewSession().MarkRead(ctx, 7, 1000)
		assert.NoError(t, err, "未匹配到行不应当报错 —— MarkRead 语义是「尝试推进」")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSessionRepo_SoftDelete(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `status`=\\?,`updatetime`=\\? WHERE id = \\?").
		WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := chat_repo.NewSession().SoftDelete(ctx, 5)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_ResetActiveSessions 钉死启动期残留清理 SQL:任何 agent_status
// 是 running / waiting 的未软删 session 都翻成 error。
// 主 Wails 实例 Startup 后调一次,防止 app crash / restart 留下永远卡 RUNNING 的会话。
//
// **跑在远端 daemon 上的会话被排除在外**:那一轮的执行者是另一台机器上的进程,它不随
// 桌面 App 退出而消亡(R4:断连不终止会话)。把它一并翻成 error 是在报一个假失败 ——
// 会话此刻很可能正在远端跑着,而且如果它在桌面端离线期间什么新内容都没产出,补齐重放
// 不会写任何状态,这条假失败就永久留在界面上。它们的去向改由补齐按 daemon 交回的
// 生命周期逐条判定(见 chat_svc.CatchUpRemoteSessions / ResetActiveSessionsByIDs)。
func TestSessionRepo_ResetActiveSessions(t *testing.T) {
	t.Run("有残留时把 running / waiting 翻成 error 并返回受影响行数", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		assertResetActiveSessions(t, ctx, mock, 3)
	})

	t.Run("没残留时也走 SQL,返回 0 行不报错", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		assertResetActiveSessions(t, ctx, mock, 0)
	})
}

// TestSessionRepo_ResetActiveSessionsByIDs 钉死「补齐判完之后收尾这几条」的 SQL:
// 只动点名的那些会话,且只动其中还停在 running / waiting 的行。
//
// 它是启动期清理在远端那一半的落点:blanket 的 ResetActiveSessions 不再碰远端会话,
// 因为在连上 daemon 之前根本无从知道它是不是还在跑;连上之后 daemon 说它不在跑了,
// 才由这一条按 id 收尾。空 id 列表不发 SQL —— 那会退化成 WHERE id IN () 的全表扫描。
func TestSessionRepo_ResetActiveSessionsByIDs(t *testing.T) {
	t.Run("点名的会话里还在 running / waiting 的翻成 error", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `chat_sessions` SET `agent_status`=\\?,`updatetime`=\\? "+
			"WHERE agent_status IN \\(\\?,\\?\\) AND status = \\? AND id IN \\(\\?,\\?\\)").
			WithArgs("error", sqlmock.AnyArg(), "running", "waiting", consts.ACTIVE, int64(100), int64(101)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		n, err := chat_repo.NewSession().ResetActiveSessionsByIDs(ctx, []int64{100, 101})
		assert.NoError(t, err)
		assert.Equal(t, int64(1), n)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("id 多到超过 SQLite 参数上限时分批发,不塞进一条 IN ?", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		ids := make([]int64, 0, 900)
		for i := 0; i < 900; i++ {
			ids = append(ids, int64(1000+i))
		}
		// SQLite 的预编译参数上限最保守是 999(SQLITE_MAX_VARIABLE_NUMBER):一条
		// 900 个 id 的 IN ? 再加上 SET 与其余 WHERE,整条语句直接被拒 —— 用户攒够
		// 会话之后,启动补齐的收尾这一步就永远报错,判定结果一条都落不了库。
		// 每批一条独立的语句(而不是一条横跨全部 id 的大事务):批次之间别的写入插得
		// 进来,不会把流式写入按在 SQLite 的 busy timeout 上。
		for start := 0; start < len(ids); start += 400 {
			end := min(start+400, len(ids))
			args := make([]driver.Value, 0, 5+end-start)
			args = append(args, "error", sqlmock.AnyArg(), "running", "waiting", consts.ACTIVE)
			for _, id := range ids[start:end] {
				args = append(args, id)
			}
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `chat_sessions` SET `agent_status`=\\?,`updatetime`=\\? " +
				"WHERE agent_status IN \\(\\?,\\?\\) AND status = \\? AND id IN ").
				WithArgs(args...).
				WillReturnResult(sqlmock.NewResult(0, int64(end-start)))
			mock.ExpectCommit()
		}

		n, err := chat_repo.NewSession().ResetActiveSessionsByIDs(ctx, ids)
		assert.NoError(t, err)
		assert.Equal(t, int64(900), n, "受影响行数是各批之和")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("没有点名任何会话时一条 SQL 都不发", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		n, err := chat_repo.NewSession().ResetActiveSessionsByIDs(ctx, nil)
		assert.NoError(t, err)
		assert.Zero(t, n)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestSessionRepo_UpdatePermissionModeAtLaunch 验证 spawn 时 runner 调用的
// 单字段更新 SQL —— 不能把 permission_mode 一起冲掉。
func TestSessionRepo_UpdatePermissionModeAtLaunch(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").
		WithArgs("bypassPermissions", sqlmock.AnyArg(), int64(42), 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdatePermissionModeAtLaunch(ctx, 42, "bypassPermissions"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateContextWindow 钉死 runtime 探到 model 上下文窗口时的单列写入。
//
// sess-2974:这一列此前是「改内存实体再 Update 整行」落库的,而带外轮(自主续轮 / 后台
// subagent 活动轮)手里的实体是它起步时读出来的快照 —— 用户随后发的新一轮把 agent_status
// 写成 running 之后,带外轮再收到一帧 context window 就把整行(含 agent_status、
// last_message_at)拍回旧值,会话在库里退回 idle。改成单列 UPDATE 后,这条路径在结构上
// 就碰不到别的列。
func TestSessionRepo_UpdateContextWindow(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `context_window`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs(1000000, sqlmock.AnyArg(), int64(42), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateContextWindow(ctx, 42, 1000000))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateExecDaemon 钉死「这条会话跑在哪台 daemon 上、钉在哪一档」的
// 写入 SQL(R15b / 决策36)。关键不变式:(实例标识, 游标) 必须始终是同一条通知日志上
// 的一对 —— 改绑到另一台 daemon 时,老游标指的是老 daemon 日志里的位置,必须在同一
// 条语句里归零;换成两次写(先改绑再清游标)会留下一个「游标看起来对新 daemon 有效」
// 的崩溃窗口。exec_agent_backend_id 与设备/实例标识同一条语句一并写。
func TestSessionRepo_UpdateExecDaemon(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `event_cursor`=CASE WHEN exec_device_fingerprint = \\? THEN event_cursor ELSE 0 END,`exec_agent_backend_id`=\\?,`exec_device_fingerprint`=\\?,`exec_device_id`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs("sha256:beef", int64(51), "sha256:beef", int64(3), sqlmock.AnyArg(), int64(42), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateExecDaemon(ctx, 42, 3, "sha256:beef", 51))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateEventCursor 钉死游标推进的写入 SQL:只动 event_cursor,
// 不能把执行位置或实例标识一起改掉(否则游标与 daemon 的绑定关系就断了);
// 且 WHERE 必须带上实例标识 —— 会话已改绑到别的 daemon 后,老连接上迟到的一条
// 通知不得把老日志的 seq 写到新 daemon 的记录上。
func TestSessionRepo_UpdateEventCursor(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `event_cursor`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\? AND exec_device_fingerprint = \\?").
		WithArgs(int64(17), sqlmock.AnyArg(), int64(42), consts.ACTIVE, "sha256:beef").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateEventCursor(ctx, 42, "sha256:beef", 17))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateKeepsRemoteExecColumns 回归(R12):轮次收尾的整行回写不得
// 抹掉轮次进行中定向写下的执行位置与游标。
//
// 真机复现的形态(逐 250ms 采样,两次独立运行一致):轮次进行中三列都对
// (exec_device_id=2 / 指纹 / 游标 15→33,补齐也确实用上了),一到 running → idle
// 收尾就一起变成 0 / 空串 / 0。收尾走的 chat_svc.persistSessionStatus 拿的是**轮次
// 开始时**读出来的那份实体(那会儿还没 borrow 到远端,三列都是零值),而 Update 是
// 整行 Save —— 内存里的旧零值把这中间两次定向写(UpdateExecDaemon / UpdateEventCursor)
// 刚落库的值盖回去。
//
// 后果不止丢三个字段:ListRemoteExecSessions 的取材条件是 exec_device_id > 0 且
// exec_device_fingerprint 非空,空闲的远端会话因此永远进不了启动补齐 ——
// 「退出桌面 App 之后下次打开能看到这段时间里发生的全部内容」对它们完全失效。
//
// 所以断言落在「这一行最后是什么」,而不是收尾那条 SQL 长什么样:缺陷出在两次写
// 之间的相互作用,单看任何一条语句都是对的。
func TestSessionRepo_UpdateKeepsRemoteExecColumns(t *testing.T) {
	ctx, gdb, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	// 库里那一行。sqlmock 不是真引擎、不维护行状态,这里按仓储实际发出的 SET 子句
	// 逐列跟着改(见 captureUpdatedRow)。
	row := map[string]any{
		"agent_status":            "running",
		"exec_device_id":          int64(0),
		"exec_device_fingerprint": "",
		"exec_agent_backend_id":   int64(0),
		"event_cursor":            int64(0),
	}
	captureUpdatedRow(t, gdb, row)

	// 轮次开始时读出来的实体:还没 borrow 到远端,四列都是零值。
	sess := &chat_entity.Session{ID: 42, AgentStatus: "running", Status: consts.ACTIVE}

	// 写 1:borrow 到 daemon 2 时记下执行位置与钉住的档(chat_svc.recordExecDaemon)。
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.UpdateExecDaemon(ctx, 42, 2, "sha256:beef", 51))

	// 写 2:消费到 seq 33 时推进游标(chat_svc 的游标端口 SaveCursor)。
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.UpdateEventCursor(ctx, 42, "sha256:beef", 33))

	// 写 3:running → idle 收尾,用的是上面那份**没跟着变**的内存实体。
	sess.AgentStatus = "idle"
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Update(ctx, sess))

	assert.Equal(t, int64(2), row["exec_device_id"], "收尾不得把执行位置抹回本机")
	assert.Equal(t, "sha256:beef", row["exec_device_fingerprint"], "收尾不得抹掉 daemon 实例标识")
	assert.Equal(t, int64(51), row["exec_agent_backend_id"], "收尾不得把钉住的执行目标档抹回未钉住")
	assert.Equal(t, int64(33), row["event_cursor"], "收尾不得把游标冲回 0")
	assert.Equal(t, "idle", row["agent_status"], "收尾本来要写的状态照常落库")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateKeepsExecAgentBackendID 单独锁住会话粘性回归守卫
// (R15b / 决策36):exec_agent_backend_id 是与 exec_device_id / exec_device_fingerprint
// 并列的第四列,由同一条 UpdateExecDaemon 语句一并写入、一并加进 Update 的 Omit 清单;
// 轮次收尾的整行 Save 用的是轮次开始时读出的旧实体(那时这一列还是 0),若它没被 Omit,
// 收尾会把刚钉住的档抹回 0 —— 下一轮又变成重挑,直接违反决策36「不因为它离线就改派」
// 的前提(钉住的值本身就会消失)。
func TestSessionRepo_UpdateKeepsExecAgentBackendID(t *testing.T) {
	ctx, gdb, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	row := map[string]any{
		"agent_status":          "running",
		"exec_agent_backend_id": int64(0),
	}
	captureUpdatedRow(t, gdb, row)

	// 轮次开始时读出来的实体:还没钉住,这一列是零值。
	sess := &chat_entity.Session{ID: 42, AgentStatus: "running", Status: consts.ACTIVE}

	// 写 1:首轮挑到 backend 51,PickExecTarget 的结果钉住并写回。
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.UpdateExecDaemon(ctx, 42, 0, "", 51))

	// 写 2:running → idle 收尾,用的是上面那份**没跟着变**的内存实体。
	sess.AgentStatus = "idle"
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Update(ctx, sess))

	assert.Equal(t, int64(51), row["exec_agent_backend_id"], "收尾不得把钉住的执行目标档抹回未钉住")
	assert.Equal(t, "idle", row["agent_status"], "收尾本来要写的状态照常落库")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateKeepsCwd 回归:整行回写不得抹掉会话钉住的工作目录。
//
// cwd 由导入在建档时写入(spec 2026-08-26「续跑」:工作目录取磁盘转录里记录的 cwd),
// 此后没有任何路径会再写它;而轮次收尾走的是整行 Save,手里那份实体可能是别处
// 现造的(例如带外轮的旧快照)。不 Omit 的话它会把这一列拍成空串 —— 会话从此按
// agent 默认目录起 CLI,那条 provider session id 在那儿根本不存在,续跑当场断掉。
func TestSessionRepo_UpdateKeepsCwd(t *testing.T) {
	ctx, gdb, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	row := map[string]any{"agent_status": "running", "cwd": "/Code/agentre"}
	captureUpdatedRow(t, gdb, row)

	// 手里这份实体没带 cwd(轮次开始时读出的旧快照 / 别处现造的都是这个形态)。
	sess := &chat_entity.Session{ID: 42, AgentStatus: "idle", Status: consts.ACTIVE}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Update(ctx, sess))

	assert.Equal(t, "/Code/agentre", row["cwd"], "整行回写不得把会话的工作目录抹成空串")
	assert.Equal(t, "idle", row["agent_status"], "收尾本来要写的状态照常落库")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateModelTarget 钉死会话级 ModelTarget 切换的写入 SQL(spec
// 2026-08-11 决策 1):provider_key 与 model_key 两条在**同一条** UPDATE 语句里原子写入,
// 只动这两列(+ updatetime),不能顺带把并发轮次正在写的状态列一起盖掉 —— 切换允许在轮
// 中发生,整行 Save 在这里是错的。
func TestSessionRepo_UpdateModelTarget(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `model_key`=\\?,`provider_key`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs("mk-haiku", "anthropic-main", sqlmock.AnyArg(), int64(42), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateModelTarget(ctx, 42, "anthropic-main", "mk-haiku"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateModelTargetDefault 空 providerKey = 改回「跟随 agent 绑定」
// (inherit-agent),modelKey 一并清空,同一条语句照常写入(不是 no-op,也不走 gorm 的
// 零值跳过)。
func TestSessionRepo_UpdateModelTargetDefault(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `model_key`=\\?,`provider_key`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs("", "", sqlmock.AnyArg(), int64(42), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateModelTarget(ctx, 42, "", ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateModelTargetProviderDefault 非空 providerKey + 空 modelKey =
// provider-default(每轮解析该 Provider 当前默认模型)。model_key 仍显式写空串,不能靠
// gorm 零值跳过(那会让它保留旧值,把用户刚切回 provider-default 的会话留在固定模型上)。
func TestSessionRepo_UpdateModelTargetProviderDefault(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `model_key`=\\?,`provider_key`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs("", "anthropic-main", sqlmock.AnyArg(), int64(42), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateModelTarget(ctx, 42, "anthropic-main", ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateKeepsModelTarget 回归(spec 2026-08-11 决策 1/2):ModelTarget
// 切换允许发生在轮中,而轮次收尾的整行 Save 用的是**轮次开始时**读出的那份实体 —— 那
// 会儿 provider_key / model_key 还是旧值。这两列若不在 Update 的 Omit 清单里,收尾就
// 会把用户刚切好的 target 悄悄冲回去,下一轮又打回旧目标(症状与 R12 的执行位置被抹平
// 同形)。
//
// 断言落在「这一行最后是什么」,而不是某条语句长什么样:缺陷出在两次写之间的相互作用。
func TestSessionRepo_UpdateKeepsModelTarget(t *testing.T) {
	ctx, gdb, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	row := map[string]any{
		"agent_status": "running",
		"provider_key": "old-provider",
		"model_key":    "old-model",
	}
	captureUpdatedRow(t, gdb, row)

	// 轮次开始时读出来的实体:带的是切换前的 target。
	sess := &chat_entity.Session{ID: 42, AgentStatus: "running", Status: consts.ACTIVE, ProviderKey: "old-provider", ModelKey: "old-model"}

	// 写 1:轮中用户切到另一个 target(chat_svc.SetChatSessionModelTarget)。
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.UpdateModelTarget(ctx, 42, "new-provider", "new-model"))

	// 写 2:running → idle 收尾,用的是上面那份**没跟着变**的内存实体。
	sess.AgentStatus = "idle"
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Update(ctx, sess))

	assert.Equal(t, "new-provider", row["provider_key"], "收尾不得把轮中切好的会话供应商冲回旧值")
	assert.Equal(t, "new-model", row["model_key"], "收尾不得把轮中切好的会话模型冲回旧值")
	assert.Equal(t, "idle", row["agent_status"], "收尾本来要写的状态照常落库")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateKeepsContextWindow 回归 Problem 18 / 决策 23:旧的 Omit
// 黑名单漏了 context_window——它有专用窄写方法 UpdateContextWindow(运行时轮中
// 上报),却不在 Omit 清单里。轮次收尾用的是**轮次开始时读出的旧实体**,那一刻
// context_window 还是旧值;若整行 Save 把它一起写回去,就会把轮中刚探到的新窗口
// 值拍回旧值(sess-2974 同款间歇性覆盖,只是这次是 context_window 而不是
// agent_status)。改成白名单后,这一列不在「配置列」之列,不会出现在 SET 子句里。
func TestSessionRepo_UpdateKeepsContextWindow(t *testing.T) {
	ctx, gdb, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	row := map[string]any{"agent_status": "running", "context_window": 100}
	captureUpdatedRow(t, gdb, row)

	// 轮次开始时读出来的实体:上下文窗口还是旧值 100。
	sess := &chat_entity.Session{ID: 42, AgentStatus: "running", Status: consts.ACTIVE, ContextWindow: 100}

	// 写 1:轮中 runtime 探到新的窗口大小 500(contextWindowWriterAdapter 走窄写)。
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.UpdateContextWindow(ctx, 42, 500))

	// 写 2:running → idle 收尾,用的是上面那份**没跟着变**的内存实体(ContextWindow 仍是 100)。
	sess.AgentStatus = "idle"
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Update(ctx, sess))

	assert.Equal(t, 500, row["context_window"], "收尾不得把轮中上报的上下文窗口拍回旧值")
	assert.Equal(t, "idle", row["agent_status"], "收尾本来要写的状态照常落库")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateWritesOnlyWhitelistedColumns 锁住决策 23 的白名单契约本身:
// 整行回写只写「配置列」(agent_id/title/agent_status/last_message_at/
// provider_session_id/project_id/purpose/updatetime),窄写方法已经覆盖的列——
// exec_* 四列、permission_mode(_at_launch)、provider_key/model_key、cwd、
// context_window,以及新纳入这一类的 status(SoftDelete 专用窄写)和
// last_read_at(MarkRead 专用窄写)——一律不出现在 SET 子句里。漏了配置列,这条用例
// 立刻红(那次写入不发生);混进了本该窄写的列,就是黑名单那套间歇性覆盖事故重演。
func TestSessionRepo_UpdateWritesOnlyWhitelistedColumns(t *testing.T) {
	ctx, gdb, mock := testutils.Database(t)
	repo := chat_repo.NewSession()

	// 库里那一行:配置列是「旧」值,窄写方法覆盖的列是各自的哨兵值。
	row := map[string]any{
		"agent_id":                  int64(1),
		"title":                     "old title",
		"agent_status":              "idle",
		"last_message_at":           int64(1),
		"provider_session_id":       "old-provider-session",
		"project_id":                int64(1),
		"purpose":                   "",
		"permission_mode":           "sentinel-permission-mode",
		"permission_mode_at_launch": "sentinel-permission-mode-at-launch",
		"provider_key":              "sentinel-provider-key",
		"model_key":                 "sentinel-model-key",
		"cwd":                       "sentinel-cwd",
		"context_window":            999,
		"exec_device_id":            int64(77),
		"exec_device_fingerprint":   "sentinel-fingerprint",
		"exec_agent_backend_id":     int64(88),
		"event_cursor":              int64(66),
		"status":                    consts.DELETE,
		"last_read_at":              int64(55),
	}
	captureUpdatedRow(t, gdb, row)

	// 手里这份实体:配置列是新值(本次收尾要写的),窄写覆盖的列全是**不同于库里**的
	// 陈旧值——如果它们混进了 SET 子句,断言会立刻抓到。
	sess := &chat_entity.Session{
		ID:                     42,
		AgentID:                2,
		Title:                  "new title",
		AgentStatus:            "idle",
		LastMessageAt:          2,
		ProviderSessionID:      "new-provider-session",
		ProjectID:              2,
		Purpose:                "",
		Status:                 consts.ACTIVE,
		PermissionMode:         "stale-permission-mode",
		PermissionModeAtLaunch: "stale-permission-mode-at-launch",
		ProviderKey:            "stale-provider-key",
		ModelKey:               "stale-model-key",
		Cwd:                    "stale-cwd",
		ContextWindow:          1,
		ExecDeviceID:           1,
		ExecDeviceFingerprint:  "stale-fingerprint",
		ExecAgentBackendID:     1,
		EventCursor:            1,
		LastReadAt:             1,
	}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Update(ctx, sess))

	assert.Equal(t, int64(2), row["agent_id"], "配置列 agent_id 应当照常写入新值")
	assert.Equal(t, "new title", row["title"], "配置列 title 应当照常写入新值")
	assert.Equal(t, int64(2), row["last_message_at"], "配置列 last_message_at 应当照常写入新值")
	assert.Equal(t, "new-provider-session", row["provider_session_id"], "配置列 provider_session_id 应当照常写入新值")
	assert.Equal(t, int64(2), row["project_id"], "配置列 project_id 应当照常写入新值")

	assert.Equal(t, "sentinel-permission-mode", row["permission_mode"], "permission_mode 有专用窄写,整行回写不得碰它")
	assert.Equal(t, "sentinel-permission-mode-at-launch", row["permission_mode_at_launch"], "permission_mode_at_launch 有专用窄写,整行回写不得碰它")
	assert.Equal(t, "sentinel-provider-key", row["provider_key"], "provider_key 有专用窄写,整行回写不得碰它")
	assert.Equal(t, "sentinel-model-key", row["model_key"], "model_key 有专用窄写,整行回写不得碰它")
	assert.Equal(t, "sentinel-cwd", row["cwd"], "cwd 只在建档时写入,整行回写不得碰它")
	assert.Equal(t, 999, row["context_window"], "context_window 有专用窄写,整行回写不得碰它")
	assert.Equal(t, int64(77), row["exec_device_id"], "exec_device_id 有专用窄写,整行回写不得碰它")
	assert.Equal(t, "sentinel-fingerprint", row["exec_device_fingerprint"], "exec_device_fingerprint 有专用窄写,整行回写不得碰它")
	assert.Equal(t, int64(88), row["exec_agent_backend_id"], "exec_agent_backend_id 有专用窄写,整行回写不得碰它")
	assert.Equal(t, int64(66), row["event_cursor"], "event_cursor 有专用窄写,整行回写不得碰它")
	assert.Equal(t, consts.DELETE, row["status"], "status 由 SoftDelete 专用窄写,整行回写不得把软删的会话拍回 ACTIVE")
	assert.Equal(t, int64(55), row["last_read_at"], "last_read_at 由 MarkRead 专用窄写,整行回写不得碰它")
	require.NoError(t, mock.ExpectationsWereMet())
}

// captureUpdatedRow 把仓储实际发出的 UPDATE 语句应用到 row 上,补出 sqlmock 没有的
// 行状态。取的是 GORM 生成的 SET 子句本身,不预设哪条语句该写哪些列。
func captureUpdatedRow(t *testing.T, gdb *gorm.DB, row map[string]any) {
	t.Helper()
	require.NoError(t, gdb.Callback().Update().After("gorm:update").
		Register("test:capture_updated_row", func(tx *gorm.DB) {
			applySetClause(row, tx.Statement.SQL.String(), tx.Statement.Vars)
		}))
}

// applySetClause 解析 "UPDATE ... SET `col`=?,... WHERE ..." 的 SET 子句并赋值。
// 右值不是单个占位符的列(UpdateExecDaemon 的 event_cursor CASE 表达式)由引擎求值,
// 这里保持原值 —— 本用例走到那一步时 CASE 的结果与原值同为 0。
func applySetClause(row map[string]any, sql string, vars []any) {
	set := sql[strings.Index(sql, " SET ")+len(" SET ") : strings.Index(sql, " WHERE ")]
	arg := 0
	for _, assign := range strings.Split(set, ",") {
		col, rhs, ok := strings.Cut(assign, "=")
		if !ok {
			continue
		}
		col = strings.Trim(col, "`")
		if rhs == "?" {
			if _, tracked := row[col]; tracked {
				row[col] = vars[arg]
			}
		}
		arg += strings.Count(rhs, "?")
	}
}

// TestSessionRepo_ListRemoteExecSessions 钉死「App 启动后该连谁」那一问的读取 SQL。
//
// exec_device_id 在此之前是只写列:写进去、再没人读。桌面端因此在重启后不知道哪些
// 会话跑在哪台 daemon 上,补齐三步无从发起 —— 用户故事「退出 App 后下次打开看到这
// 段时间发生的全部内容」直接不成立。
//
// 过滤条件的两半都是硬的:exec_device_id > 0 排除本机会话(它们的真相源是本地库,
// 没有可补齐的远端日志),exec_device_fingerprint <> ” 排除没有实例标识的行 ——
// 游标只在它所属的那条通知日志里有意义,标识为空时 LoadCursor 一律判失效,拿它去
// attach 只会白发一轮 RPC。
//
// 取材还必须**有界**:补齐会为每条会话装一个消费方、加一份池连接引用、开一条自主轮
// 监视,而这条查询原本返回「历史上曾远端执行过的每一条」会话 —— 用得久了就是几千条。
// 界只剩条数(catchUpLimit),不再有时间截止:时间窗当初与 daemon 的通知日志留存窗口
// 对齐(更老的会话补齐回来是空的),agentred 现在永久保存通知日志、不再回收(规格
// 决策 8),那条依据随之消失 —— 一条本地停在 idle、远端却由后台任务续过轮的老会话,
// 日志今天还在,不该因为「上次本地写它是 40 天前」就永远补不回来。
//
// 因此这里连**带不带** updatetime 截止一起钉:多出那半个条件就是把还能补的内容挡在
// 外面。排序仍把 running / waiting 排在最前 —— 条数上限砍谁由它决定,只有 daemon 能
// 给那些行判据,漏掉一条就是界面上一条永远转圈的会话。
func TestSessionRepo_ListRemoteExecSessions(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE exec_device_id > \\? AND exec_device_fingerprint <> \\? AND status = \\? "+
		"ORDER BY CASE WHEN agent_status IN \\('running','waiting'\\) THEN 0 ELSE 1 END, updatetime DESC, id DESC LIMIT \\?").
		WithArgs(int64(0), "", consts.ACTIVE, 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "agent_status", "status", "exec_device_id", "exec_device_fingerprint", "event_cursor"}).
			AddRow(1, 7, "running", consts.ACTIVE, 3, "sha256:beef", 17).
			AddRow(2, 7, "idle", consts.ACTIVE, 4, "sha256:cafe", 0))

	got, err := chat_repo.NewSession().ListRemoteExecSessions(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, int64(3), got[0].ExecDeviceID)
	assert.Equal(t, "sha256:beef", got[0].ExecDeviceFingerprint)
	assert.Equal(t, int64(17), got[0].EventCursor)
	assert.Equal(t, int64(4), got[1].ExecDeviceID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_ListExecAgentBackendRefs 回归决策 24:后端墓碑回收判据与悬空
// 引用巡检都要知道"这个 backend id 有没有被任何会话提到过"——包含软删的会话
// (不限 status),否则回收会把一个仍被某条(哪怕已软删的)会话记着的后端墓碑
// 物理删掉,巡检也会漏报。
func TestSessionRepo_ListExecAgentBackendRefs(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT id AS session_id, exec_agent_backend_id AS agent_backend_id FROM `chat_sessions` WHERE exec_agent_backend_id > \\?").
		WithArgs(int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "agent_backend_id"}).
			AddRow(int64(1), int64(51)).
			AddRow(int64(2), int64(52)))

	got, err := chat_repo.NewSession().ListExecAgentBackendRefs(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, int64(1), got[0].SessionID)
	assert.Equal(t, int64(51), got[0].AgentBackendID)
	assert.Equal(t, int64(2), got[1].SessionID)
	assert.Equal(t, int64(52), got[1].AgentBackendID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_ReassignProject 钉死 R11a 的一句：合并后不允许留下任何指向已消失
// 项目的引用。WHERE 里只能有 project_id —— 一旦加上 status 或 purpose，软删的会话
// 与子 agent 委派会话就会被留在原地。
func TestSessionRepo_ReassignProject(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_sessions` SET `project_id`=\\?,`updatetime`=\\? WHERE project_id = \\?$").
		WithArgs(int64(9), sqlmock.AnyArg(), int64(4)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, chat_repo.NewSession().ReassignProject(ctx, 4, 9))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── 会话索引：一个 filter 一套查询 ──────────────────────────────────────────
//
// 索引的四条轴（时间 / 项目 / 随手对话 / 机器）与 agent 的会话列表，问的是同一个问题
// 的不同收窄：可见性口径（未软删 + 非子 agent 委派）与排序（最近活动优先）永远一样，
// 变的只有「按哪一维收窄」和「标题/名字要不要命中关键词」。所以只有 ListIndexPaged /
// CountIndex 这一对方法，收窄条件全部走 SessionIndexFilter —— 每加一维就多开两个方法
// 的话，加上关键词这一维会直接翻倍。
// 见 docs/specs/2026-08-16-unified-chat-index.md。

func int64p(v int64) *int64 { return &v }

func TestSessionRepo_ListIndexPaged(t *testing.T) {
	t.Run("Given an empty filter, When listing a page, Then it orders by last activity without filtering on agent or project", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE chat_sessions.status = \\? AND chat_sessions.purpose <> \\? ORDER BY chat_sessions.last_message_at DESC, chat_sessions.id DESC LIMIT \\? OFFSET \\?").
			WithArgs(consts.ACTIVE, chat_entity.SessionPurposeSubagent, 20, 40).
			WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "project_id", "last_message_at", "status"}).
				AddRow(int64(9), int64(1), int64(3), 1700000090000, consts.ACTIVE).
				AddRow(int64(8), int64(2), int64(0), 1700000080000, consts.ACTIVE))

		rows, err := chat_repo.NewSession().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{}, 40, 20)
		assert.NoError(t, err)
		assert.Len(t, rows, 2)
		assert.Equal(t, int64(9), rows[0].ID)
		// 自由会话（project_id = 0）与挂了项目的会话在这一档同列。
		assert.Equal(t, int64(0), rows[1].ProjectID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given the first page, When listing, Then no OFFSET clause is emitted", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("ORDER BY chat_sessions.last_message_at DESC, chat_sessions.id DESC LIMIT \\?$").
			WithArgs(consts.ACTIVE, chat_entity.SessionPurposeSubagent, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))

		rows, err := chat_repo.NewSession().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{}, 0, 20)
		assert.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given ProjectID 0, When listing, Then it narrows to free sessions instead of dropping the dimension", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("WHERE chat_sessions.project_id = \\? AND chat_sessions.status = \\? AND chat_sessions.purpose <> \\?").
			WithArgs(int64(0), consts.ACTIVE, chat_entity.SessionPurposeSubagent, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id", "project_id"}).AddRow(int64(5), int64(0)))

		rows, err := chat_repo.NewSession().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{ProjectID: int64p(0)}, 0, 20)
		assert.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.Equal(t, int64(0), rows[0].ProjectID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given a ProjectID, When listing, Then it narrows to that project", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("WHERE chat_sessions.project_id = \\? AND chat_sessions.status = \\? AND chat_sessions.purpose <> \\? ORDER BY .* LIMIT \\? OFFSET \\?").
			WithArgs(int64(7), consts.ACTIVE, chat_entity.SessionPurposeSubagent, 5, 5).
			WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "project_id"}).AddRow(int64(101), int64(42), int64(7)))

		rows, err := chat_repo.NewSession().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{ProjectID: int64p(7)}, 5, 5)
		assert.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given DeviceID 0, When listing, Then exec_device_id stays in the WHERE — 0 is this machine, not 'any machine'", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("WHERE chat_sessions.exec_device_id = \\? AND chat_sessions.status = \\? AND chat_sessions.purpose <> \\?").
			WithArgs(int64(0), consts.ACTIVE, chat_entity.SessionPurposeSubagent, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id", "exec_device_id"}).AddRow(int64(5), int64(0)))

		rows, err := chat_repo.NewSession().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{DeviceID: int64p(0)}, 0, 20)
		assert.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.Equal(t, int64(0), rows[0].ExecDeviceID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given an AgentID, When listing, Then it narrows to that agent", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("WHERE chat_sessions.agent_id = \\? AND chat_sessions.status = \\? AND chat_sessions.purpose <> \\?").
			WithArgs(int64(42), consts.ACTIVE, chat_entity.SessionPurposeSubagent, 5, 5).
			WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id"}).AddRow(int64(101), int64(42)))

		rows, err := chat_repo.NewSession().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{AgentID: int64p(42)}, 5, 5)
		assert.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestSessionRepo_ListIndexPagedKeyword 钉死搜索的命中口径。它是这次修复的核心：
// 搜索此前只在前端已加载的那一页上做子串匹配，命中范围等于「首屏窗口」而不是全量。
//
// 三个字段（会话标题 / agent 名 / 项目名）与合并前的前端口径同源（决策 8），但语义现在
// 只有 SQL 这一份 —— 名字两维靠 LEFT JOIN 取回，所以 SELECT 必须显式收成
// `chat_sessions.*`，否则 agents/projects 的 id、name、status 会覆盖掉会话自己的列。
func TestSessionRepo_ListIndexPagedKeyword(t *testing.T) {
	t.Run("Given a keyword, When listing, Then title, agent name and project name are all matched", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT chat_sessions.\\* FROM `chat_sessions` "+
			"LEFT JOIN agents ON agents.id = chat_sessions.agent_id "+
			"LEFT JOIN projects ON projects.id = chat_sessions.project_id "+
			"WHERE chat_sessions.project_id = \\? AND chat_sessions.status = \\? AND chat_sessions.purpose <> \\? "+
			"AND \\(chat_sessions.title LIKE \\? ESCAPE '\\\\' OR agents.name LIKE \\? ESCAPE '\\\\' OR projects.name LIKE \\? ESCAPE '\\\\'\\)").
			WithArgs(int64(1), consts.ACTIVE, chat_entity.SessionPurposeSubagent, "%happy%", "%happy%", "%happy%", 50).
			WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow(int64(3495), "看看happy是怎么实现中继的"))

		rows, err := chat_repo.NewSession().ListIndexPaged(ctx,
			chat_repo.SessionIndexFilter{ProjectID: int64p(1), Keyword: "happy"}, 0, 50)
		assert.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, int64(3495), rows[0].ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given a keyword holding LIKE wildcards, When listing, Then they are escaped into literals", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		// 不转义的话「100%」会退化成「1、0、0 加任意后缀」，搜得越具体命中越宽。
		mock.ExpectQuery("LEFT JOIN agents").
			WithArgs(consts.ACTIVE, chat_entity.SessionPurposeSubagent,
				"%100\\%\\_a\\\\b%", "%100\\%\\_a\\\\b%", "%100\\%\\_a\\\\b%", 20).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		_, err := chat_repo.NewSession().ListIndexPaged(ctx,
			chat_repo.SessionIndexFilter{Keyword: `100%_a\b`}, 0, 20)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given a blank keyword, When listing, Then no join and no LIKE are emitted at all", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT \\* FROM `chat_sessions` WHERE chat_sessions.status = \\? AND chat_sessions.purpose <> \\? ORDER BY").
			WithArgs(consts.ACTIVE, chat_entity.SessionPurposeSubagent, 20).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))

		rows, err := chat_repo.NewSession().ListIndexPaged(ctx,
			chat_repo.SessionIndexFilter{Keyword: "   "}, 0, 20)
		assert.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSessionRepo_CountIndex(t *testing.T) {
	t.Run("Given an empty filter, When counting, Then it counts every visible session", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT count\\(\\*\\) FROM `chat_sessions`").
			WithArgs(consts.ACTIVE, chat_entity.SessionPurposeSubagent).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(37))

		n, err := chat_repo.NewSession().CountIndex(ctx, chat_repo.SessionIndexFilter{})
		assert.NoError(t, err)
		assert.Equal(t, int64(37), n)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Given a device filter, When counting, Then it narrows to that machine", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT count\\(\\*\\) FROM `chat_sessions` WHERE chat_sessions.exec_device_id = \\?").
			WithArgs(int64(7), consts.ACTIVE, chat_entity.SessionPurposeSubagent).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(12))

		n, err := chat_repo.NewSession().CountIndex(ctx, chat_repo.SessionIndexFilter{DeviceID: int64p(7)})
		assert.NoError(t, err)
		assert.Equal(t, int64(12), n)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// 总数必须和列表用同一个 filter 算：搜索生效时组头写的「会话 N」与「查看全部 N」
	// 都是这个数，拿未过滤的总数去配过滤后的列表，就是 44 条的项目顶着一行搜索结果。
	t.Run("Given a keyword, When counting, Then the same join and LIKE narrow the count", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)

		mock.ExpectQuery("SELECT count\\(\\*\\) FROM `chat_sessions` "+
			"LEFT JOIN agents ON agents.id = chat_sessions.agent_id "+
			"LEFT JOIN projects ON projects.id = chat_sessions.project_id "+
			"WHERE chat_sessions.status = \\? AND chat_sessions.purpose <> \\? AND \\(chat_sessions.title LIKE \\?").
			WithArgs(consts.ACTIVE, chat_entity.SessionPurposeSubagent, "%happy%", "%happy%", "%happy%").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(17))

		n, err := chat_repo.NewSession().CountIndex(ctx, chat_repo.SessionIndexFilter{Keyword: "happy"})
		assert.NoError(t, err)
		assert.Equal(t, int64(17), n)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestSessionRepo_ListIDsByProviderSessions 钉死导入判重的读取口径（2026-08-26 导入
// 本地会话 spec 决策 18）：**去重一律以库里的 provider_session_id 为准** —— pi 的会话
// 文件在磁盘上没有任何来源标记，靠 entrypoint / originator 判重必漏。
//
// 两处刻意的取值：
//   - 不挂 nonSubagentScope。这一问是「这条 provider session 是不是已经在库里」，
//     子 agent 委派会话同样占着一个 provider_session_id，把它排除掉就会让同一条
//     CLI 会话被导入第二次，两条 agentre 会话争同一个 --resume 目标。
//   - 空串不进 WHERE。绝大多数会话的 provider_session_id 是空串（还没跑过第一轮），
//     一个空串候选会把它们全部命中。
func TestSessionRepo_ListIDsByProviderSessions(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT `provider_session_id`,`id` FROM `chat_sessions` WHERE provider_session_id IN \\(\\?,\\?\\) AND status = \\?").
		WithArgs("sess-a", "sess-b", consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"provider_session_id", "id"}).
			AddRow("sess-a", 11).
			AddRow("sess-b", 12))

	got, err := chat_repo.NewSession().ListIDsByProviderSessions(ctx, []string{"sess-a", "", "sess-b", "sess-a"})
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"sess-a": 11, "sess-b": 12}, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_ListIDsByProviderSessions_NoIDs 空列表（或只有空串）一条 SQL 都不发：
// `WHERE provider_session_id IN ()` 在不同方言下要么语法错、要么退化成全表扫描。
func TestSessionRepo_ListIDsByProviderSessions_NoIDs(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	got, err := chat_repo.NewSession().ListIDsByProviderSessions(ctx, []string{"", "   "})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}
