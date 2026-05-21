// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"testing"
	"time"

	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/stretchr/testify/assert"
)

func TestPauseRegistry_MarkAndIsPaused(t *testing.T) {
	defer ResetPauseRegistry()
	ResetPauseRegistry()

	url := "redis://example:6379"
	assert.False(t, IsPaused(url))

	MarkPaused(url, time.Now().Add(5*time.Second))
	assert.True(t, IsPaused(url))
}

func TestPauseRegistry_ExpiredEntryIsNotPaused(t *testing.T) {
	defer ResetPauseRegistry()
	ResetPauseRegistry()

	url := "redis://example:6379"
	MarkPaused(url, time.Now().Add(-1*time.Second))
	assert.False(t, IsPaused(url))
}

func TestPauseRegistry_ClearPause(t *testing.T) {
	defer ResetPauseRegistry()
	ResetPauseRegistry()

	url := "redis://example:6379"
	MarkPaused(url, time.Now().Add(5*time.Second))
	ClearPause(url)
	assert.False(t, IsPaused(url))
}

func TestPauseRegistry_RememberRecall(t *testing.T) {
	defer ResetPauseRegistry()
	ResetPauseRegistry()

	url := "redis://example:6379"
	targets := []discovery_kit_api.Target{{Id: "a"}, {Id: "b"}}

	_, ok := recallTargets(url)
	assert.False(t, ok)

	rememberTargets(url, targets)
	got, ok := recallTargets(url)
	assert.True(t, ok)
	assert.Equal(t, targets, got)
}
