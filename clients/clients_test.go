// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package clients

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redismock/v9"
	"github.com/steadybit/extension-redis/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseInfoResult_ValidInput(t *testing.T) {
	// Given
	info := `# Server
redis_version:7.0.0
redis_git_sha1:00000000
redis_git_dirty:0
os:Linux 5.4.0 x86_64

# Clients
connected_clients:10
blocked_clients:0

# Memory
used_memory:1048576
used_memory_human:1.00M
maxmemory:0`

	// When
	result := parseInfoResult(info)

	// Then
	assert.Equal(t, "7.0.0", result["redis_version"])
	assert.Equal(t, "00000000", result["redis_git_sha1"])
	assert.Equal(t, "10", result["connected_clients"])
	assert.Equal(t, "0", result["blocked_clients"])
	assert.Equal(t, "1048576", result["used_memory"])
	assert.Equal(t, "1.00M", result["used_memory_human"])
	assert.Equal(t, "0", result["maxmemory"])

	// Comments should not be included
	assert.NotContains(t, result, "# Server")
	assert.NotContains(t, result, "# Clients")
	assert.NotContains(t, result, "# Memory")
}

func TestParseInfoResult_EmptyInput(t *testing.T) {
	// When
	result := parseInfoResult("")

	// Then
	assert.Empty(t, result)
}

func TestParseInfoResult_OnlyComments(t *testing.T) {
	// Given
	info := `# Server
# Clients
# Memory`

	// When
	result := parseInfoResult(info)

	// Then
	assert.Empty(t, result)
}

func TestParseInfoResult_WhitespaceHandling(t *testing.T) {
	// Given
	info := `  redis_version:7.0.0
connected_clients:5

  # Comment
used_memory:1024  `

	// When
	result := parseInfoResult(info)

	// Then
	assert.Equal(t, "7.0.0", result["redis_version"])
	assert.Equal(t, "5", result["connected_clients"])
	assert.Equal(t, "1024", result["used_memory"])
}

func TestParseInfoResult_ValueWithColon(t *testing.T) {
	// Given - some Redis values might contain colons
	info := `executable:/usr/local/bin/redis-server
config_file:/etc/redis/redis.conf`

	// When
	result := parseInfoResult(info)

	// Then
	assert.Equal(t, "/usr/local/bin/redis-server", result["executable"])
	assert.Equal(t, "/etc/redis/redis.conf", result["config_file"])
}

func TestCreateRedisClient_ParsesURL(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL:      "redis://localhost:6379",
		Password: "secret",
		DB:       0,
	}

	// When
	client, err := CreateRedisClient(endpoint)

	// Then
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()
}

func TestCreateRedisClientFromURL_WithEndpointConfig(t *testing.T) {
	// Given - configure a known endpoint
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()

	config.Config.Endpoints = []config.RedisEndpoint{
		{
			URL:      "redis://myredis.local:6379",
			Password: "configured-password",
			Username: "configured-user",
		},
	}

	// When
	client, err := CreateRedisClientFromURL("redis://myredis.local:6379", "", 0)

	// Then
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()
}

func TestCreateRedisClientFromURL_WithOverride(t *testing.T) {
	// Given
	config.Config.Endpoints = []config.RedisEndpoint{}

	// When - password override should be applied
	client, err := CreateRedisClientFromURL("redis://localhost:6379", "override-password", 1)

	// Then
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()
}

func TestParseRedisURL_TLSConfig(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL:                "rediss://secure.redis.local:6379",
		InsecureSkipVerify: true,
	}

	// When
	opts, err := parseRedisURL(endpoint)

	// Then
	require.NoError(t, err)
	require.NotNil(t, opts.TLSConfig)
	assert.True(t, opts.TLSConfig.InsecureSkipVerify)
}

func TestParseRedisURL_DefaultSettings(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL: "redis://localhost:6379",
	}

	// When
	opts, err := parseRedisURL(endpoint)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 10, opts.PoolSize)
	assert.Equal(t, 1, opts.MinIdleConns)
}

func TestParseRedisURL_CredentialOverrides(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL:      "redis://localhost:6379",
		Password: "my-password",
		Username: "my-user",
		DB:       5,
	}

	// When
	opts, err := parseRedisURL(endpoint)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "my-password", opts.Password)
	assert.Equal(t, "my-user", opts.Username)
	assert.Equal(t, 5, opts.DB)
}

func TestParseRedisURL_InvalidURL(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL: "not-a-valid-url",
	}

	// When
	_, err := parseRedisURL(endpoint)

	// Then
	require.Error(t, err)
}

func TestCreateRedisClient_InvalidURL(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL: "invalid://not-valid:xyz",
	}

	// When
	_, err := CreateRedisClient(endpoint)

	// Then
	require.Error(t, err)
}

func TestCreateRedisClientFromURL_InvalidURL(t *testing.T) {
	// Given - clear endpoints to avoid matching
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()
	config.Config.Endpoints = []config.RedisEndpoint{}

	// When
	_, err := CreateRedisClientFromURL("invalid://not-valid", "", 0)

	// Then
	require.Error(t, err)
}

func TestCreateRedisClientFromURL_NegativeDB(t *testing.T) {
	// Given
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()
	config.Config.Endpoints = []config.RedisEndpoint{}

	// When - negative db should not override
	client, err := CreateRedisClientFromURL("redis://localhost:6379", "", -1)

	// Then
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()
}

func TestParseRedisURL_NoCredentialOverride(t *testing.T) {
	// Given - endpoint without password/username override
	endpoint := &config.RedisEndpoint{
		URL: "redis://localhost:6379",
		DB:  0,
	}

	// When
	opts, err := parseRedisURL(endpoint)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "", opts.Password)
	assert.Equal(t, "", opts.Username)
}

func TestPingRedis_Success(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL: fmt.Sprintf("redis://%s", mr.Addr()),
	}
	client, err := CreateRedisClient(endpoint)
	require.NoError(t, err)
	defer client.Close()

	// When
	err = PingRedis(context.Background(), client)

	// Then
	require.NoError(t, err)
}

func TestGetRedisInfo_WithSection(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL: fmt.Sprintf("redis://%s", mr.Addr()),
	}
	client, err := CreateRedisClient(endpoint)
	require.NoError(t, err)
	defer client.Close()

	// When - miniredis doesn't support most INFO sections, so test with empty section
	info, err := GetRedisInfo(context.Background(), client, "")

	// Then
	require.NoError(t, err)
	require.NotNil(t, info)
}

func TestGetRedisInfo_NoSection(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL: fmt.Sprintf("redis://%s", mr.Addr()),
	}
	client, err := CreateRedisClient(endpoint)
	require.NoError(t, err)
	defer client.Close()

	// When
	info, err := GetRedisInfo(context.Background(), client, "")

	// Then
	require.NoError(t, err)
	require.NotNil(t, info)
}

// Mock-based tests using redismock

func TestPingRedis_WithMock_Success(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectPing().SetVal("PONG")

	// When
	err := PingRedis(context.Background(), client)

	// Then
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPingRedis_WithMock_Error(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectPing().SetErr(errors.New("connection refused"))

	// When
	err := PingRedis(context.Background(), client)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRedisInfo_WithMock_WithSection(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	infoResponse := `# Server
redis_version:7.0.0
redis_git_sha1:00000000
os:Linux 5.4.0 x86_64

# Clients
connected_clients:10
blocked_clients:0`

	mock.ExpectInfo("server").SetVal(infoResponse)

	// When
	info, err := GetRedisInfo(context.Background(), client, "server")

	// Then
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "7.0.0", info["redis_version"])
	assert.Equal(t, "10", info["connected_clients"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRedisInfo_WithMock_NoSection(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	infoResponse := `# Memory
used_memory:1048576
used_memory_human:1.00M
maxmemory:0`

	mock.ExpectInfo().SetVal(infoResponse)

	// When
	info, err := GetRedisInfo(context.Background(), client, "")

	// Then
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "1048576", info["used_memory"])
	assert.Equal(t, "1.00M", info["used_memory_human"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRedisInfo_WithMock_Error(t *testing.T) {
	// Given
	client, mock := redismock.NewClientMock()
	defer client.Close()

	mock.ExpectInfo("memory").SetErr(errors.New("connection error"))

	// When
	info, err := GetRedisInfo(context.Background(), client, "memory")

	// Then
	require.Error(t, err)
	assert.Nil(t, info)
	require.NoError(t, mock.ExpectationsWereMet())
}
