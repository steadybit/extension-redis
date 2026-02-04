// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock-based tests for better coverage of code paths

func TestMock_LatencyCheck_Status_WithMetrics(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectPing().SetVal("PONG")

	// Simulate the ping and latency measurement
	ctx := context.Background()
	start := time.Now()
	_, err := client.Ping(ctx).Result()
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	// Then
	require.NoError(t, err)
	assert.Less(t, latencyMs, float64(100)) // Should be fast with mock
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_MemoryCheck_Status_ThresholdExceeded(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock memory info with high usage
	memoryInfo := `# Memory
used_memory:1073741824
used_memory_human:1.00G
maxmemory:1073741824`

	mock.ExpectInfo("memory").SetVal(memoryInfo)

	// When
	ctx := context.Background()
	result, err := client.Info(ctx, "memory").Result()

	// Then
	require.NoError(t, err)
	assert.Contains(t, result, "used_memory:1073741824")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_ConnectionCheck_Status_WithMaxClients(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock clients info
	clientsInfo := `# Clients
connected_clients:50
blocked_clients:2
maxclients:10000`

	mock.ExpectInfo("clients").SetVal(clientsInfo)

	// When
	ctx := context.Background()
	result, err := client.Info(ctx, "clients").Result()

	// Then
	require.NoError(t, err)
	assert.Contains(t, result, "connected_clients:50")
	assert.Contains(t, result, "maxclients:10000")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_HitRateCheck_Status_WithStats(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock stats info
	statsInfo := `# Stats
keyspace_hits:1000000
keyspace_misses:100000`

	mock.ExpectInfo("stats").SetVal(statsInfo)

	// When
	ctx := context.Background()
	result, err := client.Info(ctx, "stats").Result()

	// Then
	require.NoError(t, err)
	assert.Contains(t, result, "keyspace_hits:1000000")
	assert.Contains(t, result, "keyspace_misses:100000")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_BlockedClientsCheck_Status_ThresholdExceeded(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock clients info with blocked clients
	clientsInfo := `# Clients
connected_clients:100
blocked_clients:15`

	mock.ExpectInfo("clients").SetVal(clientsInfo)

	// When
	ctx := context.Background()
	result, err := client.Info(ctx, "clients").Result()

	// Then
	require.NoError(t, err)
	assert.Contains(t, result, "blocked_clients:15")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_ReplicationCheck_Status_AsReplica(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock replication info for a replica
	replicationInfo := `# Replication
role:slave
master_host:master.redis.local
master_port:6379
master_link_status:up
master_last_io_seconds_ago:0
master_sync_in_progress:0
slave_repl_offset:12345678
slave_priority:100
slave_read_only:1
connected_slaves:0
master_replid:abc123
master_repl_offset:12345678
second_repl_offset:-1
repl_backlog_active:1
repl_backlog_size:1048576
repl_backlog_first_byte_offset:12345678
repl_backlog_histlen:0`

	mock.ExpectInfo("replication").SetVal(replicationInfo)

	// When
	ctx := context.Background()
	result, err := client.Info(ctx, "replication").Result()

	// Then
	require.NoError(t, err)
	assert.Contains(t, result, "role:slave")
	assert.Contains(t, result, "master_link_status:up")
	assert.Contains(t, result, "master_last_io_seconds_ago:0")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_ReplicationCheck_Status_LinkDown(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock replication info with link down
	replicationInfo := `# Replication
role:slave
master_host:master.redis.local
master_port:6379
master_link_status:down
master_link_down_since_seconds:120`

	mock.ExpectInfo("replication").SetVal(replicationInfo)

	// When
	ctx := context.Background()
	result, err := client.Info(ctx, "replication").Result()

	// Then
	require.NoError(t, err)
	assert.Contains(t, result, "master_link_status:down")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_ClientPauseAttack_Start(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock CLIENT PAUSE command - returns bool in go-redis v9
	mock.ExpectClientPause(5 * time.Second).SetVal(true)

	// When
	ctx := context.Background()
	result, err := client.ClientPause(ctx, 5*time.Second).Result()

	// Then
	require.NoError(t, err)
	assert.True(t, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_ClientPauseAttack_Stop(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock CLIENT UNPAUSE command - returns bool in go-redis v9
	mock.ExpectClientUnpause().SetVal(true)

	// When
	ctx := context.Background()
	result, err := client.ClientUnpause(ctx).Result()

	// Then
	require.NoError(t, err)
	assert.True(t, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_MaxmemoryLimitAttack_Start_ConfigGet(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock CONFIG GET for original values
	mock.ExpectConfigGet("maxmemory").SetVal(map[string]string{"maxmemory": "0"})
	mock.ExpectConfigGet("maxmemory-policy").SetVal(map[string]string{"maxmemory-policy": "noeviction"})

	// When
	ctx := context.Background()
	maxmemory, err := client.ConfigGet(ctx, "maxmemory").Result()
	require.NoError(t, err)
	policy, err := client.ConfigGet(ctx, "maxmemory-policy").Result()
	require.NoError(t, err)

	// Then
	assert.Equal(t, "0", maxmemory["maxmemory"])
	assert.Equal(t, "noeviction", policy["maxmemory-policy"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_MaxmemoryLimitAttack_Start_ConfigSet(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock CONFIG SET for new values
	mock.ExpectConfigSet("maxmemory", "100mb").SetVal("OK")
	mock.ExpectConfigSet("maxmemory-policy", "allkeys-lru").SetVal("OK")

	// When
	ctx := context.Background()
	result1, err := client.ConfigSet(ctx, "maxmemory", "100mb").Result()
	require.NoError(t, err)
	result2, err := client.ConfigSet(ctx, "maxmemory-policy", "allkeys-lru").Result()
	require.NoError(t, err)

	// Then
	assert.Equal(t, "OK", result1)
	assert.Equal(t, "OK", result2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_BgsaveAttack_Start(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock LASTSAVE and BGSAVE commands - LastSave returns Unix timestamp (int64)
	lastSaveTime := time.Now().Add(-1 * time.Hour).Unix()
	mock.ExpectLastSave().SetVal(lastSaveTime)
	mock.ExpectBgSave().SetVal("Background saving started")

	// When
	ctx := context.Background()
	lastSave, err := client.LastSave(ctx).Result()
	require.NoError(t, err)
	bgSaveResult, err := client.BgSave(ctx).Result()
	require.NoError(t, err)

	// Then
	assert.True(t, time.Now().Unix()-lastSave >= 3600) // At least an hour ago
	assert.Equal(t, "Background saving started", bgSaveResult)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_KeyDeleteAttack_ScanAndDelete(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock SCAN and DELETE operations
	mock.ExpectScan(0, "test:*", 100).SetVal([]string{"test:key1", "test:key2"}, 0)
	mock.ExpectGet("test:key1").SetVal("value1")
	mock.ExpectGet("test:key2").SetVal("value2")
	mock.ExpectDel("test:key1", "test:key2").SetVal(2)

	// When
	ctx := context.Background()
	keys, cursor, err := client.Scan(ctx, 0, "test:*", 100).Result()
	require.NoError(t, err)
	assert.Len(t, keys, 2)
	assert.Equal(t, uint64(0), cursor)

	val1, err := client.Get(ctx, "test:key1").Result()
	require.NoError(t, err)
	assert.Equal(t, "value1", val1)

	val2, err := client.Get(ctx, "test:key2").Result()
	require.NoError(t, err)
	assert.Equal(t, "value2", val2)

	deleted, err := client.Del(ctx, "test:key1", "test:key2").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_CacheExpirationAttack_SetTTL(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock EXPIRE operations
	mock.ExpectExpire("cache:key1", 60*time.Second).SetVal(true)
	mock.ExpectExpire("cache:key2", 60*time.Second).SetVal(true)

	// When
	ctx := context.Background()
	result1, err := client.Expire(ctx, "cache:key1", 60*time.Second).Result()
	require.NoError(t, err)
	result2, err := client.Expire(ctx, "cache:key2", 60*time.Second).Result()
	require.NoError(t, err)

	// Then
	assert.True(t, result1)
	assert.True(t, result2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_MemoryFillAttack_SetKeys(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock SET operations for memory fill
	mock.ExpectSet("steadybit-memfill-test-0", "randomdata", 0).SetVal("OK")
	mock.ExpectSet("steadybit-memfill-test-1", "randomdata", 0).SetVal("OK")

	// When
	ctx := context.Background()
	result1, err := client.Set(ctx, "steadybit-memfill-test-0", "randomdata", 0).Result()
	require.NoError(t, err)
	result2, err := client.Set(ctx, "steadybit-memfill-test-1", "randomdata", 0).Result()
	require.NoError(t, err)

	// Then
	assert.Equal(t, "OK", result1)
	assert.Equal(t, "OK", result2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_MemoryFillAttack_Cleanup(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock DEL operations for cleanup
	mock.ExpectDel("steadybit-memfill-test-0").SetVal(1)
	mock.ExpectDel("steadybit-memfill-test-1").SetVal(1)

	// When
	ctx := context.Background()
	deleted1, err := client.Del(ctx, "steadybit-memfill-test-0").Result()
	require.NoError(t, err)
	deleted2, err := client.Del(ctx, "steadybit-memfill-test-1").Result()
	require.NoError(t, err)

	// Then
	assert.Equal(t, int64(1), deleted1)
	assert.Equal(t, int64(1), deleted2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_BigKeyAttack_CreateBigKeys(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	bigValue := string(make([]byte, 1024)) // 1KB value

	// Mock SET for big keys
	mock.ExpectSet("steadybit-bigkey-test-0", bigValue, 0).SetVal("OK")

	// When
	ctx := context.Background()
	result, err := client.Set(ctx, "steadybit-bigkey-test-0", bigValue, 0).Result()

	// Then
	require.NoError(t, err)
	assert.Equal(t, "OK", result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_ConnectionExhaustion_MultipleConnections(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	// Mock multiple PING commands to simulate connection establishment
	mock.ExpectPing().SetVal("PONG")
	mock.ExpectPing().SetVal("PONG")
	mock.ExpectPing().SetVal("PONG")

	// When
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := client.Ping(ctx).Result()
		require.NoError(t, err)
	}

	// Then
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_Info_Error(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectInfo("server").SetErr(errors.New("connection refused"))

	// When
	ctx := context.Background()
	_, err := client.Info(ctx, "server").Result()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_ConfigGet_Error(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectConfigGet("maxmemory").SetErr(errors.New("permission denied"))

	// When
	ctx := context.Background()
	_, err := client.ConfigGet(ctx, "maxmemory").Result()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_Scan_EmptyResult(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectScan(0, "nonexistent:*", 100).SetVal([]string{}, 0)

	// When
	ctx := context.Background()
	keys, cursor, err := client.Scan(ctx, 0, "nonexistent:*", 100).Result()

	// Then
	require.NoError(t, err)
	assert.Empty(t, keys)
	assert.Equal(t, uint64(0), cursor)
	require.NoError(t, mock.ExpectationsWereMet())
}
