// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEndpointByURL_ReturnsNilOnEmptyConfig(t *testing.T) {
	// Given
	Config.Endpoints = []RedisEndpoint{}

	// When
	ep := GetEndpointByURL("redis://localhost:6379")

	// Then
	assert.Nil(t, ep)
}

func TestGetEndpointByURL_NotFound(t *testing.T) {
	// Given
	Config.Endpoints = []RedisEndpoint{
		{
			URL:      "redis://redis-a.local:6379",
			Password: "secret",
			Name:     "redis-a",
		},
		{
			URL:      "redis://redis-b.local:6379",
			Password: "s3cr3t",
			Name:     "redis-b",
		},
	}

	// When
	ep := GetEndpointByURL("redis://unknown:6379")

	// Then
	assert.Nil(t, ep)
}

func TestGetEndpointByURL_Found(t *testing.T) {
	// Given
	want := RedisEndpoint{
		URL:      "redis://redis-a.local:6379",
		Password: "secret",
		Username: "alice",
		Name:     "redis-a",
		DB:       0,
	}
	Config.Endpoints = []RedisEndpoint{
		want,
		{
			URL:      "redis://redis-b.local:6379",
			Password: "s3cr3t",
			Name:     "redis-b",
		},
	}

	// When
	got := GetEndpointByURL("redis://redis-a.local:6379")

	// Then
	assert.NotNil(t, got)
	assert.Equal(t, want.URL, got.URL)
	assert.Equal(t, want.Username, got.Username)
	assert.Equal(t, want.Password, got.Password)
	assert.Equal(t, want.Name, got.Name)
}

func TestGetEndpointByURL_ExactMatchOnly(t *testing.T) {
	// Given: two endpoints with similar URLs
	Config.Endpoints = []RedisEndpoint{
		{URL: "redis://redis.local:6379", Name: "default"},
		{URL: "redis://redis.local:6380", Name: "secondary"},
	}

	// When
	got := GetEndpointByURL("redis://redis.local:6379")

	// Then
	assert.NotNil(t, got)
	assert.Equal(t, "redis://redis.local:6379", got.URL)
	assert.Equal(t, "default", got.Name)

	// And: querying the other URL returns that one
	got2 := GetEndpointByURL("redis://redis.local:6380")
	assert.NotNil(t, got2)
	assert.Equal(t, "redis://redis.local:6380", got2.URL)
	assert.Equal(t, "secondary", got2.Name)
}

func TestSanitizeRedisURL(t *testing.T) {
	// Credentials are stripped; scheme, host, port and database path are preserved.
	assert.Equal(t, "redis://localhost:6379", SanitizeRedisURL("redis://:secret@localhost:6379"))
	assert.Equal(t, "redis://localhost:6379", SanitizeRedisURL("redis://alice:s3cr3t@localhost:6379"))
	assert.Equal(t, "rediss://host:6380/2", SanitizeRedisURL("rediss://alice:pw@host:6380/2"))
	// A URL without credentials is unchanged.
	assert.Equal(t, "redis://localhost:6379", SanitizeRedisURL("redis://localhost:6379"))
}

func TestGetEndpointByURL_MatchesCredentialStrippedURL(t *testing.T) {
	// Given an endpoint configured with credentials embedded in its URL...
	Config.Endpoints = []RedisEndpoint{
		{URL: "redis://alice:s3cr3t@redis-a.local:6379", Name: "redis-a"},
	}

	// ...a lookup by the credential-stripped URL (as published to the platform) still resolves it.
	got := GetEndpointByURL("redis://redis-a.local:6379")

	assert.NotNil(t, got)
	assert.Equal(t, "redis-a", got.Name)
	assert.Equal(t, "redis://alice:s3cr3t@redis-a.local:6379", got.URL)
}

func TestRedisEndpoint_Fields(t *testing.T) {
	// Test that RedisEndpoint struct has expected fields
	endpoint := RedisEndpoint{
		URL:                "redis://localhost:6379",
		Password:           "secret",
		Username:           "admin",
		DB:                 5,
		InsecureSkipVerify: true,
		Name:               "test-redis",
	}

	assert.Equal(t, "redis://localhost:6379", endpoint.URL)
	assert.Equal(t, "secret", endpoint.Password)
	assert.Equal(t, "admin", endpoint.Username)
	assert.Equal(t, 5, endpoint.DB)
	assert.True(t, endpoint.InsecureSkipVerify)
	assert.Equal(t, "test-redis", endpoint.Name)
}

func TestSpecification_DefaultValues(t *testing.T) {
	// Test default values in Specification
	spec := Specification{
		DiscoveryIntervalInstanceSeconds: 30,
		DiscoveryIntervalDatabaseSeconds: 60,
	}

	assert.Equal(t, 30, spec.DiscoveryIntervalInstanceSeconds)
	assert.Equal(t, 60, spec.DiscoveryIntervalDatabaseSeconds)
	assert.Empty(t, spec.DiscoveryAttributesExcludesInstances)
	assert.Empty(t, spec.DiscoveryAttributesExcludesDatabases)
}

func TestValidateConfiguration_Success(t *testing.T) {
	// Given
	Config.EndpointsJSON = `[{"url":"redis://localhost:6379","name":"test"}]`

	// When
	ValidateConfiguration()

	// Then
	require.Len(t, Config.Endpoints, 1)
	assert.Equal(t, "redis://localhost:6379", Config.Endpoints[0].URL)
	assert.Equal(t, "test", Config.Endpoints[0].Name)
}

func TestValidateConfiguration_MultipleEndpoints(t *testing.T) {
	// Given
	Config.EndpointsJSON = `[{"url":"redis://host1:6379","name":"a"},{"url":"redis://host2:6379","name":"b","password":"secret","db":3}]`

	// When
	ValidateConfiguration()

	// Then
	require.Len(t, Config.Endpoints, 2)
	assert.Equal(t, "redis://host1:6379", Config.Endpoints[0].URL)
	assert.Equal(t, "redis://host2:6379", Config.Endpoints[1].URL)
	assert.Equal(t, "secret", Config.Endpoints[1].Password)
	assert.Equal(t, 3, Config.Endpoints[1].DB)
}

func TestGetEndpointByURL_MultipleMatches(t *testing.T) {
	// Given - multiple endpoints, should return first exact match
	Config.Endpoints = []RedisEndpoint{
		{URL: "redis://a.local:6379", Name: "a"},
		{URL: "redis://b.local:6379", Name: "b"},
		{URL: "redis://c.local:6379", Name: "c"},
	}

	// When
	ep := GetEndpointByURL("redis://b.local:6379")

	// Then
	assert.NotNil(t, ep)
	assert.Equal(t, "b", ep.Name)
}

func TestGetEndpointByURL_ResolvesRegisteredClusterNode(t *testing.T) {
	// Given a cluster reached through a seed endpoint, and one of its nodes registered by discovery
	Config.Endpoints = []RedisEndpoint{
		{URL: "rediss://redis-cluster.local:6379", Password: "secret", Username: "alice", DB: 3, InsecureSkipVerify: true, Name: "my-cluster", MaxBackupSizeBytes: 42},
	}
	SetClusterNodes(&Config.Endpoints[0], []string{"rediss://10.0.0.5:6380"})

	// When an action looks up the node's published URL
	got := GetEndpointByURL("rediss://10.0.0.5:6380")

	// Then it gets the seed endpoint's credentials and TLS settings, pinned to the node itself
	require.NotNil(t, got)
	assert.Equal(t, "rediss://10.0.0.5:6380", got.URL)
	assert.Equal(t, "secret", got.Password)
	assert.Equal(t, "alice", got.Username)
	assert.True(t, got.InsecureSkipVerify)
	assert.Equal(t, "my-cluster", got.Name)
	assert.Equal(t, int64(42), got.MaxBackupSizeBytes)
	assert.Equal(t, 0, got.DB)
	assert.Equal(t, "standalone", got.ClusterMode)

	// And the configured endpoint itself is unchanged
	assert.Equal(t, "rediss://redis-cluster.local:6379", Config.Endpoints[0].URL)
	assert.Equal(t, 3, Config.Endpoints[0].DB)
}

func TestGetEndpointByURL_ClusterNodeKeepsCredentialsEmbeddedInURL(t *testing.T) {
	// Given a seed endpoint whose credentials are embedded in its URL
	Config.Endpoints = []RedisEndpoint{
		{URL: "redis://alice:s3cr3t@redis-cluster.local:6379", Name: "my-cluster"},
	}
	SetClusterNodes(&Config.Endpoints[0], []string{"redis://10.0.0.6:6379"})

	// When
	got := GetEndpointByURL("redis://10.0.0.6:6379")

	// Then the node endpoint carries the same embedded credentials
	require.NotNil(t, got)
	assert.Equal(t, "redis://alice:s3cr3t@10.0.0.6:6379", got.URL)
}

func TestGetEndpointByURL_ConfiguredEndpointWinsOverClusterNode(t *testing.T) {
	// Given a node URL that is also configured as an endpoint of its own
	Config.Endpoints = []RedisEndpoint{
		{URL: "redis://redis-cluster.local:6379", Password: "seed", Name: "seed"},
		{URL: "redis://10.0.0.7:6379", Password: "own", Name: "own"},
	}
	SetClusterNodes(&Config.Endpoints[0], []string{"redis://10.0.0.7:6379"})

	// When
	got := GetEndpointByURL("redis://10.0.0.7:6379")

	// Then the explicitly configured endpoint is used
	require.NotNil(t, got)
	assert.Equal(t, "own", got.Name)
	assert.Equal(t, "own", got.Password)
}

func TestSetClusterNodes_DropsNodesThatLeftTheCluster(t *testing.T) {
	// Given two clusters, each with registered nodes
	Config.Endpoints = []RedisEndpoint{
		{URL: "redis://cluster-a.local:6379", Password: "a", Name: "cluster-a"},
		{URL: "redis://cluster-b.local:6379", Password: "b", Name: "cluster-b"},
	}
	SetClusterNodes(&Config.Endpoints[0], []string{"redis://10.0.1.1:6379", "redis://10.0.1.2:6379"})
	SetClusterNodes(&Config.Endpoints[1], []string{"redis://10.0.2.1:6379"})

	// When cluster-a's next discovery no longer lists 10.0.1.2
	SetClusterNodes(&Config.Endpoints[0], []string{"redis://10.0.1.1:6379"})

	// Then that node stops resolving, while the remaining nodes of both clusters still do
	assert.Nil(t, GetEndpointByURL("redis://10.0.1.2:6379"))
	require.NotNil(t, GetEndpointByURL("redis://10.0.1.1:6379"))
	assert.Equal(t, "a", GetEndpointByURL("redis://10.0.1.1:6379").Password)
	require.NotNil(t, GetEndpointByURL("redis://10.0.2.1:6379"))
	assert.Equal(t, "b", GetEndpointByURL("redis://10.0.2.1:6379").Password)
}
