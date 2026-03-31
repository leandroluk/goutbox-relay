package service

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/leandroluk/goutbox-relay/internal/config"
	"github.com/leandroluk/goutbox-relay/internal/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func miniRedisQueue(t *testing.T) *queue.RedisQueue {
	t.Helper()
	mr := miniredis.RunT(t)
	q, err := queue.NewRedisQueue("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { q.Close() })
	return q
}

func mockCfg() config.Config {
	return config.Config{
		OutboxTable: "outbox",
		ConfigTable: "relay_config",
		BatchSize:   10,
	}
}

func TestProcess_ReturnsErrorOnBeginTxFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

	svc := NewRelayService(mockDB, miniRedisQueue(t), mockCfg())
	_, err = svc.Process(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestProcess_ReturnsErrorOnCursorReadFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_id").WillReturnError(errors.New("cursor read failed"))
	mock.ExpectRollback()

	svc := NewRelayService(mockDB, miniRedisQueue(t), mockCfg())
	_, err = svc.Process(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read cursor")
}

func TestProcess_ReturnsErrorOnFetchEventsFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_id").WillReturnRows(sqlmock.NewRows([]string{"last_id"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT id, topic, payload").WillReturnError(errors.New("fetch failed"))
	mock.ExpectRollback()

	svc := NewRelayService(mockDB, miniRedisQueue(t), mockCfg())
	_, err = svc.Process(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fetch events")
}

func TestProcess_ReturnsErrorOnScanFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	badRows := sqlmock.NewRows([]string{"id", "topic", "payload"}).
		AddRow("not-an-int", "topic", []byte(`{}`))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_id").WillReturnRows(sqlmock.NewRows([]string{"last_id"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT id, topic, payload").WillReturnRows(badRows)
	mock.ExpectRollback()

	svc := NewRelayService(mockDB, miniRedisQueue(t), mockCfg())
	_, err = svc.Process(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scan row")
}

// TestProcess_ReturnsErrorOnRowsErrFailure covers the rows.Err() branch after the loop.
// sqlmock surfaces rows.Err() via CloseError: after Next() returns false (no rows),
// rows.Close() is called internally and exposes the error through rows.Err().
// count stays 0, so the code would return early at "count == 0" — but it still
// hits the rows.Err() check before that.
func TestProcess_ReturnsErrorOnRowsErrFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	rows := sqlmock.NewRows([]string{"id", "topic", "payload"}).
		CloseError(errors.New("rows close error"))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_id").WillReturnRows(sqlmock.NewRows([]string{"last_id"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT id, topic, payload").WillReturnRows(rows)
	mock.ExpectRollback()

	svc := NewRelayService(mockDB, miniRedisQueue(t), mockCfg())
	_, err = svc.Process(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "iterate rows")
}

func TestProcess_ReturnsErrorOnUpdateCursorFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	eventRows := sqlmock.NewRows([]string{"id", "topic", "payload"}).
		AddRow(int64(1), "test.topic", []byte(`{"x":1}`))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_id").WillReturnRows(sqlmock.NewRows([]string{"last_id"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT id, topic, payload").WillReturnRows(eventRows)
	mock.ExpectExec("UPDATE").WillReturnError(errors.New("update cursor failed"))
	mock.ExpectRollback()

	svc := NewRelayService(mockDB, miniRedisQueue(t), mockCfg())
	_, err = svc.Process(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update cursor")
}

func TestProcess_ReturnsErrorOnCommitFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	eventRows := sqlmock.NewRows([]string{"id", "topic", "payload"}).
		AddRow(int64(1), "test.topic", []byte(`{"x":1}`))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_id").WillReturnRows(sqlmock.NewRows([]string{"last_id"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT id, topic, payload").WillReturnRows(eventRows)
	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

	svc := NewRelayService(mockDB, miniRedisQueue(t), mockCfg())
	_, err = svc.Process(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "commit tx")
}

func TestProcess_ReturnsErrorOnPipelineExecFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	eventRows := sqlmock.NewRows([]string{"id", "topic", "payload"}).
		AddRow(int64(1), "test.topic", []byte(`{"x":1}`))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_id").WillReturnRows(sqlmock.NewRows([]string{"last_id"}).AddRow(int64(0)))
	mock.ExpectQuery("SELECT id, topic, payload").WillReturnRows(eventRows)
	mock.ExpectRollback()

	// Close miniredis before calling Process to force pipe.Exec to fail.
	mr := miniredis.RunT(t)
	q, err := queue.NewRedisQueue("redis://" + mr.Addr())
	require.NoError(t, err)
	mr.Close()

	svc := NewRelayService(mockDB, q, mockCfg())
	_, err = svc.Process(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis pipeline exec")
}
