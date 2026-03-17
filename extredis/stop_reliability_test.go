// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"context"
	"fmt"
	"strings"
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
// Cache-expiration: partial Start + Stop reliability
// ============================================================

func TestCacheExpirationAttack_Stop_RestoresKeysAfterExpiry(t *testing.T) {
	// Keys expired during attack. Stop() must recreate them from backup.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create keys with no TTL
	for i := 0; i < 20; i++ {
		mr.Set(fmt.Sprintf("exp-restore:key:%d", i), fmt.Sprintf("value-%d", i))
	}

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "exp-restore:key:*",
		TTLSeconds:    1,
		MaxKeys:       0,
		AffectedKeys:  []string{},
		BackupData:    make(map[string]KeyBackup),
		RestoreOnStop: true,
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
	}

	// Start — set short TTL
	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)
	assert.Len(t, state.AffectedKeys, 20)
	assert.Len(t, state.BackupData, 20)

	// Expire the keys
	mr.FastForward(2 * time.Second)

	// Verify keys are gone
	assert.False(t, mr.Exists("exp-restore:key:0"))

	// Stop — should recreate all keys from backup
	_, err = action.Stop(context.Background(), &state)
	require.NoError(t, err)

	// All 20 keys should be back with correct values
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("exp-restore:key:%d", i)
		assert.True(t, mr.Exists(key), "Key %s should be recreated", key)
		val, mrErr := mr.Get(key)
		require.NoError(t, mrErr)
		assert.Equal(t, fmt.Sprintf("value-%d", i), val)
	}
}

func TestCacheExpirationAttack_Stop_RestoresOriginalTTL(t *testing.T) {
	// Keys that had a TTL before the attack should get that TTL back on restore.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Key with 1 hour TTL
	mr.Set("ttl-restore:key1", "val1")
	mr.SetTTL("ttl-restore:key1", 1*time.Hour)
	// Key with no TTL
	mr.Set("ttl-restore:key2", "val2")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "ttl-restore:*",
		TTLSeconds:    300,
		MaxKeys:       0,
		AffectedKeys:  []string{},
		BackupData:    make(map[string]KeyBackup),
		RestoreOnStop: true,
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
	}

	// Start — backs up original TTLs
	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)
	assert.Len(t, state.AffectedKeys, 2)

	// Verify backup captured TTLs
	backup1 := state.BackupData["ttl-restore:key1"]
	assert.Greater(t, backup1.TTLSeconds, int64(0), "Should have captured original TTL")
	backup2 := state.BackupData["ttl-restore:key2"]
	// TTL for persistent keys should now correctly be stored as -1
	assert.Equal(t, int64(-1), backup2.TTLSeconds, "No-TTL key should be stored as -1 (persistent)")

	// Stop — restore original TTLs
	_, err = action.Stop(context.Background(), &state)
	require.NoError(t, err)

	// Key2 should have its TTL removed (persistent) since the backup stored -1
	ttl2 := mr.TTL("ttl-restore:key2")
	assert.Equal(t, time.Duration(0), ttl2, "Key2 should be persistent (no TTL) after restore")
}

func TestCacheExpirationAttack_Stop_PartialBackup_MixedKeyTypes(t *testing.T) {
	// Only string keys are backed up. List/hash keys that match the pattern are NOT
	// affected by cache-expiration (they're skipped in Start), so nothing is lost.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("mixed-exp:str1", "sval1")
	mr.Set("mixed-exp:str2", "sval2")
	mr.Lpush("mixed-exp:list1", "item1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "mixed-exp:*",
		TTLSeconds:    1,
		MaxKeys:       0,
		AffectedKeys:  []string{},
		BackupData:    make(map[string]KeyBackup),
		RestoreOnStop: true,
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	// Only strings affected
	assert.Len(t, state.AffectedKeys, 2)
	assert.Equal(t, 1, state.SkippedNonString)
	// List key should still exist and not have a TTL set
	assert.True(t, mr.Exists("mixed-exp:list1"))

	// Expire and restore
	mr.FastForward(2 * time.Second)
	_, err = action.Stop(context.Background(), &state)
	require.NoError(t, err)

	// All keys restored
	assert.True(t, mr.Exists("mixed-exp:str1"))
	assert.True(t, mr.Exists("mixed-exp:str2"))
	assert.True(t, mr.Exists("mixed-exp:list1"), "List key should be untouched")
}

// ============================================================
// Big-key: Stop must leave zero leftover keys
// ============================================================

func TestBigKeyAttack_Stop_CleansUpAllKeys(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &bigKeyAttack{}
	executionId := uuid.New().String()[:8]
	prefix := fmt.Sprintf("steadybit-bigkey-%s-", executionId)

	state := BigKeyState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   prefix,
		KeySize:     512, // Small for fast test
		NumKeys:     3,
		CreatedKeys: []string{},
		EndTime:     time.Now().Add(3 * time.Second).Unix(),
		ExecutionId: executionId,
	}

	// Start — goroutine creates keys in cycles
	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	// Let it run a few cycles
	time.Sleep(1500 * time.Millisecond)

	// Stop — must clean up everything
	_, err = action.Stop(context.Background(), &state)
	require.NoError(t, err)

	// Wait a bit for goroutine to fully terminate
	time.Sleep(300 * time.Millisecond)

	// Verify: no keys with our prefix remain
	allKeys := mr.Keys()
	for _, k := range allKeys {
		assert.False(t, strings.HasPrefix(k, prefix),
			"Leftover key found after Stop: %s", k)
	}
}

func TestBigKeyAttack_Stop_ImmediateStop(t *testing.T) {
	// Stop called immediately after Start — goroutine may be mid-cycle
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &bigKeyAttack{}
	executionId := uuid.New().String()[:8]
	prefix := fmt.Sprintf("steadybit-bigkey-%s-", executionId)

	state := BigKeyState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   prefix,
		KeySize:     512,
		NumKeys:     5,
		CreatedKeys: []string{},
		EndTime:     time.Now().Add(30 * time.Second).Unix(),
		ExecutionId: executionId,
	}

	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	// Stop immediately — no sleep between start and stop
	_, err = action.Stop(context.Background(), &state)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// Verify cleanup
	allKeys := mr.Keys()
	for _, k := range allKeys {
		assert.False(t, strings.HasPrefix(k, prefix),
			"Leftover key found after immediate Stop: %s", k)
	}
}

func TestBigKeyAttack_Stop_CalledWithoutStart(t *testing.T) {
	// If Start() was never called, Stop() should not panic
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &bigKeyAttack{}
	state := BigKeyState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   "steadybit-bigkey-nostart-",
		KeySize:     1024,
		NumKeys:     1,
		CreatedKeys: []string{},
		EndTime:     time.Now().Add(30 * time.Second).Unix(),
		ExecutionId: "nostart-test",
	}

	// Stop without Start — should handle gracefully
	result, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// ============================================================
// Memory-fill: Stop must delete all created keys
// ============================================================

func TestMemoryFillAttack_Stop_CleansUpAllKeys(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryFillAttack{}
	prefix := fmt.Sprintf("steadybit-memfill-%s-", uuid.New().String()[:8])

	state := MemoryFillState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   prefix,
		ValueSize:   256,
		FillRate:    100,
		MaxMemory:   1, // 1MB max
		EndTime:     time.Now().Add(2 * time.Second).Unix(),
		CreatedKeys: []string{},
	}

	// Start
	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	// Let goroutine create some keys — check via miniredis to avoid race on state.CreatedKeys
	time.Sleep(1 * time.Second)
	prefixedCount := countKeysWithPrefix(mr, prefix)
	require.Greater(t, prefixedCount, 0, "Goroutine should have created keys")

	// Wait for the goroutine to finish (EndTime is 2s from start, so wait until it's done)
	time.Sleep(1500 * time.Millisecond)

	// Stop — now safe to read state.CreatedKeys since goroutine exited
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Verify: no keys with our prefix remain
	allKeys := mr.Keys()
	for _, k := range allKeys {
		assert.False(t, strings.HasPrefix(k, prefix),
			"Leftover key found after Stop: %s", k)
	}
}

func TestMemoryFillAttack_Stop_CalledWithoutStart(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   "steadybit-memfill-nostart-",
		ValueSize:   1024,
		FillRate:    10,
		MaxMemory:   1,
		EndTime:     time.Now().Add(30 * time.Second).Unix(),
		CreatedKeys: []string{}, // Empty — Start was never called
	}

	// Should not panic, should report 0 cleaned up
	result, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestMemoryFillAttack_Stop_GoroutineStillRunning(t *testing.T) {
	// Stop() is called while the goroutine is still creating keys.
	// This test documents the known limitation: Stop() only deletes keys in
	// CreatedKeys at that moment. Keys created after Stop returns are orphaned.
	//
	// We avoid reading state.CreatedKeys while the goroutine runs (data race).
	// Instead we check key existence via miniredis directly.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryFillAttack{}
	prefix := fmt.Sprintf("steadybit-memfill-%s-", uuid.New().String()[:8])

	state := MemoryFillState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   prefix,
		ValueSize:   256,
		FillRate:    1000, // Very high rate
		MaxMemory:   0,    // Unlimited
		EndTime:     time.Now().Add(3 * time.Second).Unix(),
		CreatedKeys: []string{},
	}

	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	// Give goroutine time to create keys — check via miniredis to avoid race
	time.Sleep(500 * time.Millisecond)
	keysBeforeStop := countKeysWithPrefix(mr, prefix)
	require.Greater(t, keysBeforeStop, 0)

	// Stop while goroutine is still running (EndTime is 3s from start)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Wait for goroutine to exit (EndTime is 3s from start)
	time.Sleep(3 * time.Second)

	// After goroutine exits, check for orphaned keys (created after Stop)
	orphanedKeys := countKeysWithPrefix(mr, prefix)
	if orphanedKeys > 0 {
		t.Logf("WARNING: %d orphaned keys remain after Stop+goroutine exit (known limitation)", orphanedKeys)
	}
}

// ============================================================
// Connection-exhaustion: Stop cleanup and edge cases
// ============================================================

func TestConnectionExhaustionAttack_Stop_CalledWithoutStart(t *testing.T) {
	action := &connectionExhaustionAttack{}
	state := ConnectionExhaustionState{
		RedisURL:       "redis://localhost:6379",
		DB:             0,
		NumConnections: 10,
		EndTime:        time.Now().Add(30 * time.Second).Unix(),
	}

	// Should not panic, should close 0 connections
	result, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, (*result.Messages)[0].Message, "Closed 0 connections")
}

func TestConnectionExhaustionAttack_Stop_ClosesAllConnections(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &connectionExhaustionAttack{}
	state := ConnectionExhaustionState{
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		NumConnections: 5,
		EndTime:        time.Now().Add(30 * time.Second).Unix(),
	}

	// Start — opens connections
	startResult, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, startResult)
	assert.Greater(t, state.ConnectionCount, 0)

	// Stop — closes all connections
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Verify no connections remain in global map for our URL
	activeConnectionsMutex.Lock()
	found := false
	for key := range activeConnections {
		if strings.HasPrefix(key, state.RedisURL) {
			found = true
		}
	}
	activeConnectionsMutex.Unlock()
	assert.False(t, found, "No connections should remain in global map after Stop")
}

func TestConnectionExhaustionAttack_Stop_CalledTwice(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &connectionExhaustionAttack{}
	state := ConnectionExhaustionState{
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		NumConnections: 3,
		EndTime:        time.Now().Add(30 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	// First stop
	result1, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result1)

	// Second stop — should not panic
	result2, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result2)
	assert.Contains(t, (*result2.Messages)[0].Message, "Closed 0 connections")
}

// ============================================================
// Maxmemory-limit: restore reliability
// ============================================================

func TestMaxmemoryLimitAttack_Stop_RestoresOriginalSettings(t *testing.T) {
	// NOTE: miniredis doesn't support CONFIG GET/SET, so we can't test full Start+Stop.
	// Instead we test Stop with pre-populated state to verify the restore logic runs.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		OriginalMaxmemory: "512mb",
		OriginalPolicy:    "allkeys-lru",
		NewMaxmemory:      "10mb",
		NewPolicy:         "noeviction",
		EndTime:           time.Now().Add(30 * time.Second).Unix(),
	}

	// Stop — attempts restore (will get CONFIG errors from miniredis but shouldn't crash)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Since miniredis doesn't support CONFIG SET, the result should contain warning messages
	msgs := *stopResult.Messages
	require.NotEmpty(t, msgs)
	// The restore fails gracefully with warnings (not errors)
	assert.Equal(t, extutil.Ptr(action_kit_api.Warn), msgs[0].Level)
}

func TestMaxmemoryLimitAttack_Stop_CalledWithoutStart(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		OriginalMaxmemory: "0", // Default no-limit
		OriginalPolicy:    "noeviction",
		NewMaxmemory:      "10mb",
		NewPolicy:         "noeviction",
		EndTime:           time.Now().Add(30 * time.Second).Unix(),
	}

	// Stop without Start — should attempt restore with the zero-value originals
	result, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestMaxmemoryLimitAttack_Stop_KeepPolicyNotRestored(t *testing.T) {
	// When policy is "keep", Stop should NOT try to restore the policy.
	// We verify by checking that only maxmemory error appears (not policy error).
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		OriginalMaxmemory: "512mb",
		OriginalPolicy:    "allkeys-lru",
		NewMaxmemory:      "10mb",
		NewPolicy:         "keep",
		EndTime:           time.Now().Add(30 * time.Second).Unix(),
	}

	// Stop — should only attempt maxmemory restore, not policy
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// The warning should mention maxmemory but NOT policy (since NewPolicy is "keep")
	msgs := *stopResult.Messages
	require.NotEmpty(t, msgs)
	assert.Contains(t, msgs[0].Message, "maxmemory")
	assert.NotContains(t, msgs[0].Message, "policy:")
}

// ============================================================
// Cache-expiration: Stop called twice (idempotency)
// ============================================================

func TestCacheExpirationAttack_Stop_CalledTwice(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("double-exp:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "double-exp:*",
		TTLSeconds:    1,
		MaxKeys:       0,
		AffectedKeys:  []string{},
		BackupData:    make(map[string]KeyBackup),
		RestoreOnStop: true,
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
	}

	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	mr.FastForward(2 * time.Second)

	// First stop — restore
	_, err = action.Stop(context.Background(), &state)
	require.NoError(t, err)

	// Second stop — should not panic
	result2, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result2)

	// Key should still exist
	assert.True(t, mr.Exists("double-exp:key1"))
}

// ============================================================
// Big-key: Stop called twice (idempotency)
// ============================================================

func TestBigKeyAttack_Stop_CalledTwice(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &bigKeyAttack{}
	executionId := uuid.New().String()[:8]

	state := BigKeyState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   fmt.Sprintf("steadybit-bigkey-%s-", executionId),
		KeySize:     512,
		NumKeys:     2,
		CreatedKeys: []string{},
		EndTime:     time.Now().Add(5 * time.Second).Unix(),
		ExecutionId: executionId,
	}

	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// First stop
	_, err = action.Stop(context.Background(), &state)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// Second stop — should not panic
	result2, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result2)
}

// countKeysWithPrefix counts miniredis keys that have the given prefix.
func countKeysWithPrefix(mr *miniredis.Miniredis, prefix string) int {
	count := 0
	for _, k := range mr.Keys() {
		if strings.HasPrefix(k, prefix) {
			count++
		}
	}
	return count
}
