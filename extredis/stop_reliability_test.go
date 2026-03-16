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
// Key-delete: partial Start failure → Stop must restore what was backed up
// ============================================================

func TestKeyDeleteAttack_Stop_RestoresBackedUpKeys_EvenIfStartDeletedPartially(t *testing.T) {
	// Scenario: Start() backed up and deleted some keys, then we call Stop().
	// Stop() should restore every key present in BackupData.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create 10 keys, but only delete 5 of them via Start
	for i := 0; i < 10; i++ {
		mr.Set(fmt.Sprintf("partial:key:%d", i), fmt.Sprintf("value-%d", i))
	}

	// Use Start() to delete first 5 keys (with maxKeys=5)
	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "partial:key:*",
		MaxKeys:       5,
		RestoreOnStop: true,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)
	assert.Len(t, state.DeletedKeys, 5)
	assert.Len(t, state.BackupData, 5)

	// When
	result, err := action.Stop(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify: all 10 keys should now exist
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("partial:key:%d", i)
		val, mrErr := mr.Get(key)
		require.NoError(t, mrErr, "Key %s should exist", key)
		assert.Equal(t, fmt.Sprintf("value-%d", i), val)
	}
}

func TestKeyDeleteAttack_Stop_AllKeyTypesBackedUp(t *testing.T) {
	// Start() now backs up ALL key types via DUMP/RESTORE.
	// NOTE: miniredis does not support DUMP for non-string types, so those
	// keys log a warning and are not backed up in this test. With real Redis,
	// DUMP works for all types. We test string backup/restore here and
	// verify that non-string DUMP failures are handled gracefully.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create mixed types
	mr.Set("mixed:str", "string-value")
	mr.Lpush("mixed:list", "item1")
	mr.Lpush("mixed:list", "item2")
	mr.HSet("mixed:hash", "field1", "hashval")

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "mixed:*",
		MaxKeys:       0,
		RestoreOnStop: true,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// Start deletes all matching keys; DUMP works for strings, warns for others in miniredis
	startResult, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, startResult)

	// String key should be backed up
	assert.Contains(t, state.BackupData, "mixed:str")
	// In miniredis, non-string DUMP fails gracefully — keys are deleted but not backed up
	// With real Redis, all types would be backed up

	// All keys should be deleted
	assert.False(t, mr.Exists("mixed:str"))
	assert.False(t, mr.Exists("mixed:list"))
	assert.False(t, mr.Exists("mixed:hash"))

	// Stop restores backed up keys
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// String key restored
	val, mrErr := mr.Get("mixed:str")
	require.NoError(t, mrErr)
	assert.Equal(t, "string-value", val)
}

func TestKeyDeleteAttack_Stop_BackupWithSpecialValues(t *testing.T) {
	// Keys with empty values, very long values, or special characters
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("special:empty", "")
	mr.Set("special:newlines", "line1\nline2\nline3")
	mr.Set("special:unicode", "héllo wörld 日本語")
	mr.Set("special:binary-like", "\x00\x01\x02\xff")
	longVal := strings.Repeat("x", 100000)
	mr.Set("special:long", longVal)

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "special:*",
		MaxKeys:       0,
		RestoreOnStop: true,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// Start — delete and backup via DUMP
	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)
	assert.Len(t, state.BackupData, 5) // All string keys backed up

	// Stop — restore
	_, err = action.Stop(context.Background(), &state)
	require.NoError(t, err)

	// Verify all values restored correctly
	val, _ := mr.Get("special:empty")
	assert.Equal(t, "", val)

	val, _ = mr.Get("special:newlines")
	assert.Equal(t, "line1\nline2\nline3", val)

	val, _ = mr.Get("special:unicode")
	assert.Equal(t, "héllo wörld 日本語", val)

	val, _ = mr.Get("special:long")
	assert.Equal(t, longVal, val)
}

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
// Key-delete & cache-expiration: Stop called twice (idempotency)
// ============================================================

func TestKeyDeleteAttack_Stop_CalledTwice(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("double-stop:key1", "value1")
	mr.Set("double-stop:key2", "value2")

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "double-stop:*",
		MaxKeys:       0,
		RestoreOnStop: true,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// Start — delete
	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	// First stop — restore
	result1, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result1)

	// Second stop — should not panic or corrupt data
	result2, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result2)

	// Keys should still exist (restored by first stop, not deleted by second)
	val, mrErr := mr.Get("double-stop:key1")
	require.NoError(t, mrErr)
	assert.Equal(t, "value1", val)
}

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
