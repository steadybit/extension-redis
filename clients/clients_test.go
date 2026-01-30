// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package clients

import (
	"testing"

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
