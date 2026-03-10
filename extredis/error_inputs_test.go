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

func TestKeyDeleteAttack_Start_MalformedURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "localhost:6379"},
		{"http scheme", "http://localhost:6379"},
		{"empty string", ""},
		{"just scheme", "redis://"},
		{"garbage", "not-a-url-at-all"},
		{"ftp scheme", "ftp://localhost:6379"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := &keyDeleteAttack{}
			state := KeyDeleteState{
				RedisURL:    tc.url,
				DB:          0,
				Pattern:     "test:*",
				MaxKeys:     100,
				DeletedKeys: []string{},
				BackupData:  make(map[string]string),
			}

			_, err := action.Start(context.Background(), &state)
			require.Error(t, err, "Should fail with URL: %q", tc.url)
		})
	}
}

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
				RedisURL:     tc.url,
				DB:           0,
				Pattern:      "test:*",
				TTLSeconds:   60,
				MaxKeys:      100,
				AffectedKeys: []string{},
				BackupData:   make(map[string]KeyBackup),
				EndTime:      time.Now().Add(60 * time.Second).Unix(),
			}

			_, err := action.Start(context.Background(), &state)
			require.Error(t, err, "Should fail with URL: %q", tc.url)
		})
	}
}

func TestBigKeyAttack_Start_MalformedURL(t *testing.T) {
	action := &bigKeyAttack{}
	state := BigKeyState{
		RedisURL:    "http://wrong-scheme:6379",
		DB:          0,
		KeyPrefix:   "test-",
		KeySize:     1024,
		NumKeys:     1,
		ExecutionId: "test",
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestCachePenetrationAttack_Start_MalformedURL(t *testing.T) {
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{
		RedisURL:    "not-a-url",
		DB:          0,
		Concurrency: 5,
		KeyPrefix:   "test-",
		EndTime:     time.Now().Add(5 * time.Second).Unix(),
		AttackKey:   "test",
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
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

func TestMemoryFillAttack_Start_MalformedURL(t *testing.T) {
	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    "http://not-redis:6379",
		DB:          0,
		KeyPrefix:   "test-",
		ValueSize:   1024,
		FillRate:    100,
		MaxMemory:   1,
		EndTime:     time.Now().Add(5 * time.Second).Unix(),
		CreatedKeys: []string{},
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

func TestBgsaveAttack_Start_MalformedURL(t *testing.T) {
	action := &bgsaveAttack{}
	state := BgsaveState{
		RedisURL: "http://not-redis:6379",
		DB:       0,
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

// ============================================================
// Wrong password tests — users often misconfigure auth credentials.
// miniredis supports RequireAuth to simulate this.
// ============================================================

func TestKeyDeleteAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")
	mr.Set("test:key1", "value1")

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		Password:    "wrong-password",
		DB:          0,
		Pattern:     "test:*",
		MaxKeys:     100,
		DeletedKeys: []string{},
		BackupData:  make(map[string]string),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err, "Should fail with wrong password")
}

func TestCacheExpirationAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")
	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		Password:     "wrong-password",
		DB:           0,
		Pattern:      "test:*",
		TTLSeconds:   60,
		MaxKeys:      100,
		AffectedKeys: []string{},
		BackupData:   make(map[string]KeyBackup),
		EndTime:      time.Now().Add(60 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err, "Should fail with wrong password")
}

func TestBigKeyAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &bigKeyAttack{}
	state := BigKeyState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		Password:    "wrong-password",
		DB:          0,
		KeyPrefix:   "test-",
		KeySize:     1024,
		NumKeys:     1,
		ExecutionId: "test-auth",
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err, "Should fail with wrong password")
}

func TestCachePenetrationAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &cachePenetrationAttack{}
	state := CachePenetrationState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		Password:    "wrong-password",
		DB:          0,
		Concurrency: 5,
		KeyPrefix:   "test-",
		EndTime:     time.Now().Add(5 * time.Second).Unix(),
		AttackKey:   "test-auth",
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

func TestMemoryFillAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		Password:    "wrong-password",
		DB:          0,
		KeyPrefix:   "test-",
		ValueSize:   1024,
		FillRate:    100,
		MaxMemory:   1,
		EndTime:     time.Now().Add(5 * time.Second).Unix(),
		CreatedKeys: []string{},
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err, "Should fail with wrong password")
}

func TestBgsaveAttack_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &bgsaveAttack{}
	state := BgsaveState{
		RedisURL: fmt.Sprintf("redis://%s", mr.Addr()),
		Password: "wrong-password",
		DB:       0,
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err, "Should fail with wrong password")
}

// ============================================================
// Prepare with empty/nil target attributes
// ============================================================

func TestKeyDeleteAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(100),
			"restoreOnStop": true,
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

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

func TestBigKeyAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &bigKeyAttack{}
	state := BigKeyState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration": float64(60000),
			"keySize":  float64(10),
			"numKeys":  float64(1),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestCachePenetrationAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":    float64(60000),
			"concurrency": float64(10),
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

func TestMemoryFillAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &memoryFillAttack{}
	state := MemoryFillState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":  float64(60000),
			"valueSize": float64(1024),
			"fillRate":  float64(100),
			"maxMemory": float64(1),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestBgsaveAttack_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &bgsaveAttack{}
	state := BgsaveState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config:      map[string]any{},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

// ============================================================
// Prepare with URL present but empty value in attribute slice
// ============================================================

func TestKeyDeleteAttack_Prepare_EmptyURLValue(t *testing.T) {
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {}, // Empty slice
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(100),
			"restoreOnStop": true,
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

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

func TestBlockedClientsCheck_Start_MalformedURL(t *testing.T) {
	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{
		RedisURL:          "http://wrong:6379",
		MaxBlockedClients: 100,
		EndTime:           time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestBlockedClientsCheck_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		Password:          "wrong-password",
		MaxBlockedClients: 100,
		EndTime:           time.Now().Add(5 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestCacheHitRateCheck_Start_MalformedURL(t *testing.T) {
	action := &cacheHitRateCheck{}
	state := CacheHitRateCheckState{
		RedisURL:   "not-a-url",
		MinHitRate: 80,
		FirstCheck: true,
		EndTime:    time.Now().Add(5 * time.Second).Unix(),
	}

	_, err := action.Start(context.Background(), &state)
	require.Error(t, err)
}

func TestCacheHitRateCheck_Start_WrongPassword(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.RequireAuth("correct-password")

	action := &cacheHitRateCheck{}
	state := CacheHitRateCheckState{
		RedisURL:   fmt.Sprintf("redis://%s", mr.Addr()),
		Password:   "wrong-password",
		MinHitRate: 80,
		FirstCheck: true,
		EndTime:    time.Now().Add(5 * time.Second).Unix(),
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

func TestKeyDeleteAttack_Stop_ConnectionLost(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	addr := mr.Addr()
	mr.Set("test:key1", "value1")

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", addr),
		DB:            0,
		Pattern:       "test:*",
		MaxKeys:       100,
		RestoreOnStop: true,
		DeletedKeys:   []string{"test:key1"},
		BackupData:    map[string]string{"test:key1": "value1"},
	}

	// Close Redis before stop — simulates connection loss
	mr.Close()

	// Stop should handle connection loss gracefully
	_, err = action.Stop(context.Background(), &state)
	// May or may not error, but should not panic
	_ = err
}

func TestCacheExpirationAttack_Stop_ConnectionLost(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	addr := mr.Addr()
	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:      fmt.Sprintf("redis://%s", addr),
		DB:            0,
		Pattern:       "test:*",
		TTLSeconds:    60,
		RestoreOnStop: true,
		AffectedKeys:  []string{"test:key1"},
		BackupData:    map[string]KeyBackup{"test:key1": {Value: "value1", TTLSeconds: -1}},
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
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

func TestKeyDeleteAttack_Prepare_NegativeMaxKeys(t *testing.T) {
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(-10),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// Negative maxKeys should be accepted (treated as 0/unlimited by int conversion)
	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	// Verify state was set (negative value is stored as-is)
	assert.Equal(t, -10, state.MaxKeys)
}

func TestKeyDeleteAttack_Start_NegativeMaxKeys_BehavesAsUnlimited(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	for i := 0; i < 50; i++ {
		mr.Set(fmt.Sprintf("neg:key:%d", i), fmt.Sprintf("value-%d", i))
	}

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "neg:key:*",
		MaxKeys:       -5, // Negative: the check `state.MaxKeys > 0` is false, so unlimited
		RestoreOnStop: false,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]string),
	}

	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.DeletedKeys, 50, "Negative maxKeys should behave as unlimited")
}

func TestCacheExpirationAttack_Prepare_NegativeTTL(t *testing.T) {
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
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

	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 1, state.TTLSeconds, "Negative TTL should default to minimum of 1")
}

func TestCacheExpirationAttack_Prepare_ZeroTTL(t *testing.T) {
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
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

	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 1, state.TTLSeconds, "Zero TTL should default to minimum of 1")
}

func TestBigKeyAttack_Prepare_NegativeKeySize(t *testing.T) {
	action := &bigKeyAttack{}
	state := BigKeyState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration": float64(30000),
			"keySize":  float64(-5),
			"numKeys":  float64(-3),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 1*1024*1024, state.KeySize, "Negative keySize should default to 1MB")
	assert.Equal(t, 1, state.NumKeys, "Negative numKeys should default to 1")
}

func TestCachePenetrationAttack_Prepare_NegativeConcurrency(t *testing.T) {
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":    float64(60000),
			"concurrency": float64(-5),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 10, state.Concurrency, "Negative concurrency should default to 10")
}

// ============================================================
// Prepare with invalid database index in target attributes
// ============================================================

func TestKeyDeleteAttack_Prepare_InvalidDatabaseIndex(t *testing.T) {
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
				AttrDatabaseIndex: {"not-a-number"},
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// strconv.Atoi will fail silently and return 0
	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 0, state.DB, "Invalid DB index should default to 0")
}

func TestCacheExpirationAttack_Prepare_InvalidDatabaseIndex(t *testing.T) {
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
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

	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 0, state.DB, "Invalid DB index should default to 0")
}

func TestKeyDeleteAttack_Prepare_NegativeDatabaseIndex(t *testing.T) {
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
				AttrDatabaseIndex: {"-1"},
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, -1, state.DB, "Negative DB index is accepted (Redis will reject at connection time)")
}

func TestKeyDeleteAttack_Prepare_VeryLargeDatabaseIndex(t *testing.T) {
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
				AttrDatabaseIndex: {"99999"},
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.NoError(t, err)
	assert.Equal(t, 99999, state.DB, "Large DB index is accepted (Redis will reject at connection time)")
}

// ============================================================
// Prepare with special characters in pattern
// ============================================================

func TestKeyDeleteAttack_Prepare_SpecialCharPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{"glob with question mark", "test:key?"},
		{"glob with brackets", "test:key[0-9]"},
		{"unicode pattern", "test:clé:*"},
		{"spaces in pattern", "test: key :*"},
		{"very long pattern", "a:b:c:d:e:f:g:h:i:j:k:l:m:n:o:p:q:r:s:t:u:v:w:x:y:z:*"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := &keyDeleteAttack{}
			state := KeyDeleteState{}
			req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
				Target: &action_kit_api.Target{
					Attributes: map[string][]string{
						AttrRedisURL:      {"redis://localhost:6379"},
						AttrDatabaseIndex: {"0"},
					},
				},
				Config: map[string]any{
					"pattern":       tc.pattern,
					"maxKeys":       float64(100),
					"restoreOnStop": false,
				},
				ExecutionId: uuid.New(),
			})

			_, err := action.Prepare(context.Background(), &state, req)
			require.NoError(t, err, "Pattern %q should be accepted at prepare time", tc.pattern)
			assert.Equal(t, tc.pattern, state.Pattern)
		})
	}
}

func TestKeyDeleteAttack_Start_SpecialCharPattern_NoMatch(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("test:key1", "value1")

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		Pattern:     "test:key[a-z]", // Won't match "test:key1"
		MaxKeys:     100,
		DeletedKeys: []string{},
		BackupData:  make(map[string]string),
	}

	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, state.DeletedKeys, "Pattern should not match numeric keys")
}

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

func TestBlockedClientsCheck_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":          float64(30000),
			"maxBlockedClients": float64(100),
		},
		ExecutionId: uuid.New(),
	})

	_, err := action.Prepare(context.Background(), &state, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestCacheHitRateCheck_Prepare_EmptyTargetAttributes(t *testing.T) {
	action := &cacheHitRateCheck{}
	state := CacheHitRateCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":        float64(30000),
			"minHitRate":      float64(80),
			"minObservedRate": float64(100),
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
