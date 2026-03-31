package db

import (
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/leandroluk/goutbox-relay/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewPostgres_FailsOnUnknownDriver hits the sql.Open error branch by using
// a driver name that has never been registered with database/sql.
func TestNewPostgres_FailsOnUnknownDriver(t *testing.T) {
	_, err := newPostgresWithDriver("definitely-not-a-driver", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "open connection")
}

func TestSetupSchema_ReturnsErrorOnExecFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer mockDB.Close()

	cfg := config.Config{
		OutboxTable: "outbox",
		ConfigTable: "relay_config",
	}

	mock.ExpectExec(".*").WillReturnError(errors.New("exec failed"))

	err = SetupSchema(mockDB, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "setup schema")
}

func TestSetupSchema_ReturnsErrorOnSecondExecFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer mockDB.Close()

	cfg := config.Config{
		OutboxTable: "outbox",
		ConfigTable: "relay_config",
	}

	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(".*").WillReturnError(errors.New("second exec failed"))

	err = SetupSchema(mockDB, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "setup schema")
}
