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
