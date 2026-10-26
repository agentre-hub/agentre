package chat_repo_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/cago-frame/cago/pkg/utils/testutils"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

func TestSessionCLIModelSurvivesTurnSave(t *testing.T) {
	Convey("Given a stale running-turn entity, When the user switches CLI model and the turn finishes, Then the new choice survives", t, func() {
		ctx, gdb, mock := testutils.Database(t)
		row := map[string]any{"cli_model_mode": "", "cli_model": ""}
		captureUpdatedRow(t, gdb, row)
		repo := chat_repo.NewSession()
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		So(repo.UpdateCLIModel(ctx, 42, "fixed", "sonnet"), ShouldBeNil)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `chat_sessions`").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		So(repo.Update(ctx, &chat_entity.Session{ID: 42, AgentStatus: "idle", Status: consts.ACTIVE}), ShouldBeNil)
		So(row["cli_model_mode"], ShouldEqual, "fixed")
		So(row["cli_model"], ShouldEqual, "sonnet")
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}

func TestSessionCLIModelPersistence(t *testing.T) {
	Convey("Given a session with a provider choice, When choosing a CLI model, Then one atomic update clears the provider and reports storage failure", t, func() {
		ctx, _, mock := testutils.Database(t)
		repo := chat_repo.NewSession()
		for _, fail := range []bool{false, true} {
			mock.ExpectBegin()
			e := mock.ExpectExec("UPDATE `chat_sessions` SET `cli_model`=\\?,`cli_model_mode`=\\?,`model_key`=\\?,`provider_key`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").WithArgs("sonnet", "fixed", "", "", sqlmock.AnyArg(), int64(42), 1)
			if fail {
				e.WillReturnError(errors.New("disk full"))
				mock.ExpectRollback()
			} else {
				e.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			err := repo.UpdateCLIModel(ctx, 42, "fixed", "sonnet")
			So(err != nil, ShouldEqual, fail)
		}
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
