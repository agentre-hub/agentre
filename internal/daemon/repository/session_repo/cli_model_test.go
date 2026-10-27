package session_repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/repository/session_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func TestProviderTargetClearsCLIModel(t *testing.T) {
	convey.Convey("Given a session previously selecting a CLI model, when selecting a provider target, then the mutually exclusive CLI choice is cleared", t, func() {
		ctx, _, mock := testutils.Database(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `daemon_sessions` SET `cli_model`=\\?,`cli_model_mode`=\\?,`last_message_at`=\\?,`model_key`=\\?,`provider_key`=\\? WHERE peer_fingerprint = \\? AND conversation_id = \\?").WithArgs("", "", sqlmock.AnyArg(), "model", "provider", "peer", "session").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		_, err := session_repo.NewSession().SetModelTarget(ctx, "peer", "session", "provider", "model")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSessionCLIModelPersistence(t *testing.T) {
	convey.Convey("Given a daemon session, when selecting or resetting CLI model, then both fields and the mutually exclusive target are written", t, func() {
		for _, choice := range [][2]string{{"fixed", "sonnet"}, {"cli-default", ""}, {"", ""}} {
			ctx, _, mock := testutils.Database(t)
			repo, ok := session_repo.NewSession().(interface {
				SetCLIModel(context.Context, devicefp.Initiator, string, string, string) (int64, error)
			})
			require.True(t, ok, "session repository must persist CLI model choices")
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `daemon_sessions` SET `cli_model`=\\?,`cli_model_mode`=\\?,`last_message_at`=\\?,`model_key`=\\?,`provider_key`=\\? WHERE peer_fingerprint = \\? AND conversation_id = \\?").WithArgs(choice[1], choice[0], sqlmock.AnyArg(), "", "", "peer", "session").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			rows, err := repo.SetCLIModel(ctx, "peer", "session", choice[0], choice[1])
			require.NoError(t, err)
			require.EqualValues(t, 1, rows)
			require.NoError(t, mock.ExpectationsWereMet())
		}
	})
	convey.Convey("Given a failed database write, when switching CLI model, then failure is returned", t, func() {
		ctx, _, mock := testutils.Database(t)
		repo, ok := session_repo.NewSession().(interface {
			SetCLIModel(context.Context, devicefp.Initiator, string, string, string) (int64, error)
		})
		require.True(t, ok)
		rejected := errors.New("disk full")
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `daemon_sessions`").WillReturnError(rejected)
		mock.ExpectRollback()
		_, err := repo.SetCLIModel(ctx, "peer", "session", "fixed", "sonnet")
		require.ErrorIs(t, err, rejected)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
