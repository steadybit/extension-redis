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
	matchedKeys := make([]string, 20)
	for i := range 20 {
		key := fmt.Sprintf("exp-restore:key:%d", i)
		mr.Set(key, fmt.Sprintf("value-%d", i))
		matchedKeys[i] = key
	}

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		Pattern:        "exp-restore:key:*",
		TTLSeconds:     1,
		MaxKeys:        0,
		AffectedKeys:   []string{},
		MatchedKeys:    matchedKeys,
		BackupData:     make(map[string]KeyBackup),
		RestoreOnStop:  true,
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
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
	for i := range 20 {
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
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		Pattern:        "ttl-restore:*",
		TTLSeconds:     300,
		MaxKeys:        0,
		AffectedKeys:   []string{},
		MatchedKeys:    []string{"ttl-restore:key1", "ttl-restore:key2"},
		BackupData:     make(map[string]KeyBackup),
		RestoreOnStop:  true,
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
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
		RedisURL:         fmt.Sprintf("redis://%s", mr.Addr()),
		DB:               0,
		Pattern:          "mixed-exp:*",
		TTLSeconds:       1,
		MaxKeys:          0,
		AffectedKeys:     []string{},
		MatchedKeys:      []string{"mixed-exp:str1", "mixed-exp:str2"}, // Only string keys from Prepare
		BackupData:       make(map[string]KeyBackup),
		RestoreOnStop:    true,
		EndTime:          time.Now().Add(60 * time.Second).Unix(),
		SkippedNonString: 1, // Set by Prepare
		MaxBackupBytes:   100 * 1024 * 1024,
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
	// NOTE: miniredis doesn't support CONFIG GET/SET, so Stop will return an error.
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

	// Stop — attempts restore, fails because miniredis doesn't support CONFIG SET
	_, err = action.Stop(context.Background(), &state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restore failed")
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

	// Stop without Start — miniredis doesn't support CONFIG SET, so this returns an error
	_, err = action.Stop(context.Background(), &state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restore failed")
}

func TestMaxmemoryLimitAttack_Stop_KeepPolicyNotRestored(t *testing.T) {
	// When policy is "keep", Stop should NOT try to restore the policy.
	// We verify by checking that the error only mentions maxmemory (not policy).
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
	_, err = action.Stop(context.Background(), &state)
	require.Error(t, err)

	// The error should mention maxmemory but NOT policy (since NewPolicy is "keep")
	assert.Contains(t, err.Error(), "maxmemory")
	assert.NotContains(t, err.Error(), "policy")
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
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		Pattern:        "double-exp:*",
		TTLSeconds:     1,
		MaxKeys:        0,
		AffectedKeys:   []string{},
		MatchedKeys:    []string{"double-exp:key1"},
		BackupData:     make(map[string]KeyBackup),
		RestoreOnStop:  true,
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
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
