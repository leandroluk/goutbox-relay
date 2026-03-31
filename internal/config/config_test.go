// internal/config/config_test.go
package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- getEnv -----------------------------------------------------------------

func TestGetEnv_ReturnsEnvValue(t *testing.T) {
	t.Setenv("TEST_KEY", "hello")
	assert.Equal(t, "hello", getEnv("TEST_KEY", "fallback"))
}

func TestGetEnv_ReturnsFallbackWhenAbsent(t *testing.T) {
	assert.Equal(t, "fallback", getEnv("TEST_KEY_ABSENT", "fallback"))
}

func TestGetEnv_ReturnsFallbackWhenEmpty(t *testing.T) {
	t.Setenv("TEST_KEY_EMPTY", "")
	assert.Equal(t, "fallback", getEnv("TEST_KEY_EMPTY", "fallback"))
}

// --- getEnvInt --------------------------------------------------------------

func TestGetEnvInt_ReturnsIntValue(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	assert.Equal(t, 42, getEnvInt("TEST_INT", 99))
}

func TestGetEnvInt_ReturnsFallbackWhenAbsent(t *testing.T) {
	assert.Equal(t, 99, getEnvInt("TEST_INT_ABSENT", 99))
}

func TestGetEnvInt_ReturnsFallbackOnNonNumeric(t *testing.T) {
	t.Setenv("TEST_INT_BAD", "not-a-number")
	assert.Equal(t, 99, getEnvInt("TEST_INT_BAD", 99))
}

func TestGetEnvInt_ReturnsFallbackOnZero(t *testing.T) {
	// Zero is not a valid value for any of our int configs (batch size,
	// poll interval, retention days must all be positive).
	t.Setenv("TEST_INT_ZERO", "0")
	assert.Equal(t, 99, getEnvInt("TEST_INT_ZERO", 99))
}

func TestGetEnvInt_ReturnsFallbackOnNegative(t *testing.T) {
	t.Setenv("TEST_INT_NEG", "-5")
	assert.Equal(t, 99, getEnvInt("TEST_INT_NEG", 99))
}

// --- requireEnv -------------------------------------------------------------

func TestRequireEnv_ReturnsValue(t *testing.T) {
	t.Setenv("TEST_REQUIRED", "value")
	assert.Equal(t, "value", requireEnv("TEST_REQUIRED"))
}

func TestRequireEnv_FatalsWhenAbsent(t *testing.T) {
	original := logFatalf
	t.Cleanup(func() { logFatalf = original })

	var gotFormat string
	logFatalf = func(format string, _ ...any) {
		gotFormat = format
	}

	t.Setenv("TEST_REQUIRED_ABSENT_KEY", "")
	requireEnv("TEST_REQUIRED_ABSENT_KEY")

	assert.Contains(t, gotFormat, "required environment variable")
}

func TestRequireEnv_FatalsWhenNotSet(t *testing.T) {
	original := logFatalf
	t.Cleanup(func() { logFatalf = original })

	var called bool
	logFatalf = func(_ string, _ ...any) {
		called = true
	}

	requireEnv("TEST_KEY_DEFINITELY_NOT_SET_XYZ")

	assert.True(t, called)
}

// --- Load -------------------------------------------------------------------

func TestLoad_ReturnsDefaults(t *testing.T) {
	// Set only the required variables; everything else should use defaults.
	t.Setenv("POSTGRES_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")

	// Unset optional vars in case the test environment has them set.
	t.Setenv("TABLE_OUTBOX", "")
	t.Setenv("TABLE_CONFIG", "")
	t.Setenv("BATCH_SIZE", "")
	t.Setenv("POLL_INTERVAL", "")
	t.Setenv("RETENTION_DAYS", "")

	cfg := Load()

	assert.Equal(t, "postgres://localhost/test", cfg.PostgresURL)
	assert.Equal(t, "redis://localhost:6379", cfg.RedisURL)
	assert.Equal(t, "outbox", cfg.OutboxTable)
	assert.Equal(t, "relay_config", cfg.ConfigTable)
	assert.Equal(t, 500, cfg.BatchSize)
	assert.Equal(t, 10*time.Second, cfg.PollInterval)
	assert.Equal(t, 7*24*time.Hour, cfg.Retention)
}

func TestLoad_ReadsAllEnvVars(t *testing.T) {
	t.Setenv("POSTGRES_URL", "postgres://custom/db")
	t.Setenv("REDIS_URL", "redis://custom:6380")
	t.Setenv("TABLE_OUTBOX", "my_outbox")
	t.Setenv("TABLE_CONFIG", "my_config")
	t.Setenv("BATCH_SIZE", "250")
	t.Setenv("POLL_INTERVAL", "30")
	t.Setenv("RETENTION_DAYS", "14")

	cfg := Load()

	require.Equal(t, "postgres://custom/db", cfg.PostgresURL)
	require.Equal(t, "redis://custom:6380", cfg.RedisURL)
	assert.Equal(t, "my_outbox", cfg.OutboxTable)
	assert.Equal(t, "my_config", cfg.ConfigTable)
	assert.Equal(t, 250, cfg.BatchSize)
	assert.Equal(t, 30*time.Second, cfg.PollInterval)
	assert.Equal(t, 14*24*time.Hour, cfg.Retention)
}
