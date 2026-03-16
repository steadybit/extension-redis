// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyDeleteAttack_Describe(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.database.key-delete", desc.Id)
	assert.Equal(t, "Delete Keys", desc.Label)
	assert.Contains(t, desc.Description, "Deletes keys matching a pattern")
	assert.Equal(t, TargetTypeDatabase, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	require.GreaterOrEqual(t, len(desc.Parameters), 2)

	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "pattern")
	assert.Contains(t, paramNames, "maxKeys")
	assert.Contains(t, paramNames, "restoreOnStop")
}

func TestKeyDeleteAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(100),
			"restoreOnStop": true,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestKeyDeleteAttack_Prepare_MissingPattern(t *testing.T) {
	// Given
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
			"pattern":       "",
			"maxKeys":       float64(100),
			"restoreOnStop": true,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")
}

func TestKeyDeleteAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
				AttrDatabaseIndex: {"2"},
			},
		},
		Config: map[string]any{
			"pattern":       "cache:*",
			"maxKeys":       float64(50),
			"restoreOnStop": true,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 2, state.DB)
	assert.Equal(t, "cache:*", state.Pattern)
	assert.Equal(t, 50, state.MaxKeys)
	assert.True(t, state.RestoreOnStop)
	assert.Empty(t, state.DeletedKeys)
	assert.NotNil(t, state.BackupData)
}

func TestKeyDeleteAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.NotNil(t, state.BackupData)
	assert.Empty(t, state.DeletedKeys)
}

func TestKeyDeleteAttack_Prepare_DefaultDB(t *testing.T) {
	// Given - no database index attribute
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(10),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 0, state.DB)
	assert.False(t, state.RestoreOnStop)
}

func TestKeyDeleteAttack_StartStatusStop(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create test keys
	mr.Set("test:key1", "value1")
	mr.Set("test:key2", "value2")

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "test:*",
		MaxKeys:       100,
		RestoreOnStop: true,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// When - Start
	startResult, err := action.Start(context.Background(), &state)

	// Then - Start
	require.NoError(t, err)
	require.NotNil(t, startResult)
	assert.Len(t, state.DeletedKeys, 2)

	// When - Stop (restore keys)
	stopResult, err := action.Stop(context.Background(), &state)

	// Then - Stop
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Verify keys are restored
	val, err := mr.Get("test:key1")
	require.NoError(t, err)
	assert.Equal(t, "value1", val)
}

func TestKeyDeleteAttack_Start_NoMatchingKeys(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "nonexistent:*",
		MaxKeys:       100,
		RestoreOnStop: false,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, state.DeletedKeys)
}

func TestKeyDeleteAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:    "redis://nonexistent:6379",
		DB:          0,
		Pattern:     "test:*",
		MaxKeys:     100,
		DeletedKeys: []string{},
		BackupData:  make(map[string]KeyBackupEntry),
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestNewKeyDeleteAttack(t *testing.T) {
	// When
	action := NewKeyDeleteAttack()

	// Then
	require.NotNil(t, action)
}

func TestKeyDeleteAttack_Start_LargeNumberOfKeys(t *testing.T) {
	// Given - Redis with many keys that require multiple SCAN iterations (batch size is 100)
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create 1500 keys to force multiple SCAN iterations
	keyCount := 1500
	for i := 0; i < keyCount; i++ {
		mr.Set(fmt.Sprintf("bulk:key:%04d", i), fmt.Sprintf("value-%d", i))
	}

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "bulk:key:*",
		MaxKeys:       0, // Unlimited
		RestoreOnStop: false,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.DeletedKeys, keyCount, "All %d keys should be deleted", keyCount)

	// Verify keys are actually deleted in Redis
	keys := mr.Keys()
	for _, k := range keys {
		assert.False(t, startsWith(k, "bulk:key:"), "Key %s should have been deleted", k)
	}
}

func TestKeyDeleteAttack_Start_LargeNumberOfKeys_WithMaxKeys(t *testing.T) {
	// Given - Redis with many keys but maxKeys limits the deletion
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	keyCount := 1500
	for i := 0; i < keyCount; i++ {
		mr.Set(fmt.Sprintf("limited:key:%04d", i), fmt.Sprintf("value-%d", i))
	}

	action := &keyDeleteAttack{}
	maxKeys := 250
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "limited:key:*",
		MaxKeys:       maxKeys,
		RestoreOnStop: false,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.DeletedKeys, maxKeys, "Only %d keys should be deleted due to maxKeys limit", maxKeys)

	// Verify remaining keys count
	remainingKeys := mr.Keys()
	matchingCount := 0
	for _, k := range remainingKeys {
		if startsWith(k, "limited:key:") {
			matchingCount++
		}
	}
	assert.Equal(t, keyCount-maxKeys, matchingCount, "Remaining keys should be %d", keyCount-maxKeys)
}

func TestKeyDeleteAttack_Start_LargeNumberOfKeys_WithRestoreOnStop(t *testing.T) {
	// Given - Redis with many keys, all backed up and restored
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	keyCount := 500
	for i := 0; i < keyCount; i++ {
		mr.Set(fmt.Sprintf("restore:key:%04d", i), fmt.Sprintf("value-%d", i))
	}

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "restore:key:*",
		MaxKeys:       0, // Unlimited
		RestoreOnStop: true,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// When - Start (deletes all keys)
	startResult, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, startResult)
	assert.Len(t, state.DeletedKeys, keyCount)
	assert.Len(t, state.BackupData, keyCount)

	// Verify all keys are deleted
	keys := mr.Keys()
	for _, k := range keys {
		assert.False(t, startsWith(k, "restore:key:"), "Key %s should have been deleted", k)
	}

	// When - Stop (restores all keys)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Then - verify all keys are restored with correct values
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("restore:key:%04d", i)
		val, err := mr.Get(key)
		require.NoError(t, err, "Key %s should exist after restore", key)
		assert.Equal(t, fmt.Sprintf("value-%d", i), val, "Key %s should have correct value", key)
	}
}

func TestKeyDeleteAttack_Start_LargeNumberOfKeys_MixedPatterns(t *testing.T) {
	// Given - Redis with many keys, only some matching the pattern
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	targetCount := 1000
	otherCount := 500
	for i := 0; i < targetCount; i++ {
		mr.Set(fmt.Sprintf("target:key:%04d", i), fmt.Sprintf("target-value-%d", i))
	}
	for i := 0; i < otherCount; i++ {
		mr.Set(fmt.Sprintf("other:key:%04d", i), fmt.Sprintf("other-value-%d", i))
	}

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "target:key:*",
		MaxKeys:       0,
		RestoreOnStop: false,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]KeyBackupEntry),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.DeletedKeys, targetCount, "Only target keys should be deleted")

	// Verify other keys are untouched
	remainingKeys := mr.Keys()
	otherRemaining := 0
	for _, k := range remainingKeys {
		if startsWith(k, "other:key:") {
			otherRemaining++
		}
	}
	assert.Equal(t, otherCount, otherRemaining, "Other keys should remain untouched")
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func TestKeyDeleteAttack_Stop_NoKeysToRestore(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "test:*",
		RestoreOnStop: true,
		DeletedKeys:   []string{}, // No keys to restore
		BackupData:    map[string]KeyBackupEntry{},
	}

	// When
	result, err := action.Stop(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestKeyDeleteAttack_Start_MixedTypes_DumpBackup(t *testing.T) {
	// Given - Redis with mixed key types. DUMP backup works for all types with
	// real Redis, but miniredis only supports DUMP for string keys.
	// This test verifies graceful handling: strings are backed up and restored,
	// non-string types are deleted with a logged warning about backup failure.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

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

	// Start — delete all, backup what DUMP supports
	startResult, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, startResult)

	// String key backed up via DUMP
	assert.Contains(t, state.BackupData, "mixed:str")
	assert.Equal(t, "string", state.BackupData["mixed:str"].KeyType)

	// All 3 keys should be deleted
	assert.Len(t, state.DeletedKeys, 3)

	// Stop — restore backed up keys
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// String key restored correctly
	val, mrErr := mr.Get("mixed:str")
	require.NoError(t, mrErr)
	assert.Equal(t, "string-value", val)
}
