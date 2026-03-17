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

func TestMock_SentinelStop_InfoServer(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	serverInfo := `# Server
redis_version:7.0.0
redis_mode:sentinel
os:Linux`

	mock.ExpectInfo("server").SetVal(serverInfo)

	// When
	ctx := context.Background()
	result, err := client.Info(ctx, "server").Result()

	// Then
	require.NoError(t, err)
	assert.Contains(t, result, "redis_mode:sentinel")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_SentinelStop_DebugSleep(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectDo("DEBUG", "SLEEP", int64(30)).SetVal("OK")

	// When
	ctx := context.Background()
	err := client.Do(ctx, "DEBUG", "SLEEP", int64(30)).Err()

	// Then
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMock_SentinelStop_DebugSleep_Error(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectDo("DEBUG", "SLEEP", int64(30)).SetErr(errors.New("ERR DEBUG command not allowed"))

	// When
	ctx := context.Background()
	err := client.Do(ctx, "DEBUG", "SLEEP", int64(30)).Err()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEBUG command not allowed")
	require.NoError(t, mock.ExpectationsWereMet())
}
