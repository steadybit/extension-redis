// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestMaskURL(t *testing.T) {
	// Simple test - maskURL currently returns the URL as-is
	url := "redis://localhost:6379"
	assert.Equal(t, url, maskURL(url))
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
