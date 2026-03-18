// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Invalid Redis URL tests — covers malformed URLs, wrong schemes, etc.
// Users frequently misconfigure the Redis connection string.
// ============================================================

func TestCacheExpirationAttack_Start_MalformedURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "localhost:6379"},
		{"http scheme", "http://localhost:6379"},
		{"empty string", ""},
		{"garbage", "not-a-url"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := &cacheExpirationAttack{}
			state := CacheExpirationState{
				RedisURL:       tc.url,
				DB:             0,
				Pattern:        "test:*",
				TTLSeconds:     60,
				MaxKeys:        100,
				AffectedKeys:   []string{},
				MatchedKeys:    []string{"test:key1"},
				BackupData:     make(map[string]KeyBackup),
				EndTime:        time.Now().Add(60 * time.Second).Unix(),
				MaxBackupBytes: 100 * 1024 * 1024,
			}

			_, err := action.Start(context.Background(), &state)
			require.Error(t, err, "Should fail with URL: %q", tc.url)
		})
	}
}

func TestClientPauseAttack_Start_MalformedURL(t *testing.T) {
	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL:  "http://wrong:6379",
		DB:        0,
		PauseMode: "ALL",
		EndTime:   time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestConnectionExhaustionAttack_Start_MalformedURL(t *testing.T) {
	action := &connectionExhaustionAttack{}
	state := ConnectionExhaustionState{
		RedisURL:       "garbage-url",
		DB:             0,
		NumConnections: 5,
		EndTime:        time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestMaxmemoryLimitAttack_Start_MalformedURL(t *testing.T) {
	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:     "ftp://wrong:6379",
		DB:           0,
		NewMaxmemory: "100mb",
		NewPolicy:    "noeviction",
		EndTime:      time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

// ============================================================
// Wrong password tests — users often misconfigure auth credentials.
// miniredis supports RequireAuth to simulate this.
// ============================================================

func TestCacheExpirationAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")
	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		Password:       "wrong-password",
		DB:             0,
		Pattern:        "test:*",
		TTLSeconds:     60,
		MaxKeys:        100,
		AffectedKeys:   []string{},
		MatchedKeys:    []string{"test:key1"},
		BackupData:     make(map[string]KeyBackup),
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err, "Should fail with wrong password")
}

func TestClientPauseAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL:  fmt.Sprintf("redis://%s", mr.Addr()),
		Password:  "wrong-password",
		DB:        0,
		PauseMode: "ALL",
		EndTime:   time.Now().Add(5 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err, "Should fail with wrong password")
}

func TestMaxmemoryLimitAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		Password:     "wrong-password",
		DB:           0,
		NewMaxmemory: "100mb",
		NewPolicy:    "noeviction",
		EndTime:      time.Now().Add(5 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err, "Should fail with wrong password")
}

// ============================================================
// Prepare with empty/nil target attributes
// ============================================================

func TestCacheExpirationAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "test:*",
			"ttl":           float64(5),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestClientPauseAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &clientPauseAttack{}
	state := ClientPauseState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":  float64(30000),
			"pauseMode": "ALL",
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestConnectionExhaustionAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &connectionExhaustionAttack{}
	state := ConnectionExhaustionState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":       float64(60000),
			"numConnections": float64(10),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestMaxmemoryLimitAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":       float64(60000),
			"maxmemory":      "100mb",
			"evictionPolicy": "noeviction",
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

// ============================================================
// Prepare with URL present but empty value in attribute slice
// ============================================================

func TestCacheExpirationAttack_Prepare_EmptyURLValue(t *testing.T) {
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {}, // Empty slice
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "test:*",
			"ttl":           float64(5),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

// ============================================================
// Checks — invalid connection info
// ============================================================

func TestLatencyCheck_Start_MalformedURL(t *testing.T) {
	action := &latencyCheck{}
	state := LatencyCheckState{
		RedisURL:     "not-a-valid-url",
		MaxLatencyMs: 1000,
		EndTime:      time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestLatencyCheck_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &latencyCheck{}
	state := LatencyCheckState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		Password:     "wrong-password",
		MaxLatencyMs: 1000,
		EndTime:      time.Now().Add(5 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestMemoryCheck_Start_MalformedURL(t *testing.T) {
	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         "http://wrong-scheme:6379",
		MaxMemoryPercent: 90,
		EndTime:          time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestMemoryCheck_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         fmt.Sprintf("redis://%s", mr.Addr()),
		Password:         "wrong-password",
		MaxMemoryPercent: 90,
		EndTime:          time.Now().Add(5 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestConnectionCountCheck_Start_MalformedURL(t *testing.T) {
	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{
		RedisURL:          "garbage",
		MaxConnectionsPct: 90,
		MaxConnections:    10000,
		EndTime:           time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestConnectionCountCheck_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		Password:          "wrong-password",
		MaxConnectionsPct: 90,
		MaxConnections:    10000,
		EndTime:           time.Now().Add(5 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestReplicationLagCheck_Start_MalformedURL(t *testing.T) {
	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{
		RedisURL:      "ftp://wrong:6379",
		MaxLagSeconds: 60,
		EndTime:       time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestReplicationLagCheck_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		Password:      "wrong-password",
		MaxLagSeconds: 60,
		EndTime:       time.Now().Add(5 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err)
}

// ============================================================
// Stop with connection errors — Redis becomes unavailable during attack
// ============================================================

func TestCacheExpirationAttack_Stop_ConnectionLost(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	addr := mr.Addr()
	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:       fmt.Sprintf("redis://%s", addr),
		DB:             0,
		Pattern:        "test:*",
		TTLSeconds:     60,
		RestoreOnStop:  true,
		AffectedKeys:   []string{"test:key1"},
		BackupData:     map[string]KeyBackup{"test:key1": {Value: "value1", TTLSeconds: -1}},
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
	}

	// Close Redis before stop
	mr.Close()

	// Should handle gracefully (returns warning, not panic)
	result, err := action.Stop(context.Background(), &state)
	// The stop method returns a result with warning when connection fails
	_ = result
	_ = err
}

// ============================================================
// Prepare with various invalid config values
// ============================================================

func TestCacheExpirationAttack_Prepare_NegativeTTL(t *testing.T) {
	// Given — miniredis with matching keys
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {fmt.Sprintf("redis://%s", mr.Addr())},
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "test:*",
			"ttl":           float64(-5),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	_, err = action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 1, state.TTLSeconds, "Negative TTL should default to minimum of 1")
}

func TestCacheExpirationAttack_Prepare_ZeroTTL(t *testing.T) {
	// Given — miniredis with matching keys
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {fmt.Sprintf("redis://%s", mr.Addr())},
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "test:*",
			"ttl":           float64(0),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	_, err = action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 1, state.TTLSeconds, "Zero TTL should default to minimum of 1")
}

// ============================================================
// Prepare with invalid database index in target attributes
// ============================================================

func TestCacheExpirationAttack_Prepare_InvalidDatabaseIndex(t *testing.T) {
	// Given — miniredis with matching keys
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {fmt.Sprintf("redis://%s", mr.Addr())},
				AttrDatabaseIndex: {"abc"},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "test:*",
			"ttl":           float64(5),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	_, err = action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 0, state.DB, "Invalid DB index should default to 0")
}

// ============================================================
// Prepare with special characters in pattern
// ============================================================

// ============================================================
// Checks — Prepare with missing URL
// ============================================================

func TestLatencyCheck_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &latencyCheck{}
	state := LatencyCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":       float64(30000),
			"maxLatencyMs":   float64(100),
			"successRate":    float64(100),
			"commandTimeout": float64(5000),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestMemoryCheck_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &memoryCheck{}
	state := MemoryCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":         float64(30000),
			"maxMemoryPercent": float64(90),
			"maxMemoryBytes":   float64(0),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestConnectionCountCheck_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":          float64(30000),
			"maxConnectionsPct": float64(90),
			"maxConnections":    float64(10000),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestReplicationLagCheck_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":      float64(30000),
			"maxLagSeconds": float64(60),
			"requireLinkUp": false,
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}
