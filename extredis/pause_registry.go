/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"sync"
	"time"

	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
)

// pauseRegistry tracks endpoints with an active `CLIENT PAUSE ALL` attack so the
// discovery loop can skip probing them — Redis pauses every client connection,
// including ours, so a fresh ping/info would time out and the targets would
// disappear from the platform mid-experiment. While an endpoint is paused we
// return the last-known set of targets instead.
type pauseRegistry struct {
	mu       sync.RWMutex
	pausedTo map[string]time.Time
	lastSeen map[string][]discovery_kit_api.Target
}

var registry = &pauseRegistry{
	pausedTo: make(map[string]time.Time),
	lastSeen: make(map[string][]discovery_kit_api.Target),
}

// MarkPaused records that endpointURL is paused until `until`. Use this only
// for `ALL` pause mode — `WRITE` mode lets discovery reads (`PING`, `INFO`)
// through so there is nothing to mitigate.
func MarkPaused(endpointURL string, until time.Time) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pausedTo[endpointURL] = until
}

// ClearPause removes any pause marker for endpointURL.
func ClearPause(endpointURL string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.pausedTo, endpointURL)
}

// IsPaused reports whether endpointURL has an active pause window. Expired
// entries are cleaned up lazily.
func IsPaused(endpointURL string) bool {
	registry.mu.RLock()
	until, ok := registry.pausedTo[endpointURL]
	registry.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().Before(until) {
		return true
	}
	registry.mu.Lock()
	if expired, stillThere := registry.pausedTo[endpointURL]; stillThere && !time.Now().Before(expired) {
		delete(registry.pausedTo, endpointURL)
	}
	registry.mu.Unlock()
	return false
}

func rememberTargets(endpointURL string, targets []discovery_kit_api.Target) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.lastSeen[endpointURL] = targets
}

func recallTargets(endpointURL string) ([]discovery_kit_api.Target, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	targets, ok := registry.lastSeen[endpointURL]
	return targets, ok
}

// ResetPauseRegistry clears all pause state and cached targets. For tests.
func ResetPauseRegistry() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.pausedTo = make(map[string]time.Time)
	registry.lastSeen = make(map[string][]discovery_kit_api.Target)
}
