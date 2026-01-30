/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
)

type bigKeyAttack struct{}

type BigKeyState struct {
	RedisURL       string   `json:"redisUrl"`
	Password       string   `json:"password"`
	DB             int      `json:"db"`
	KeyPrefix      string   `json:"keyPrefix"`
	KeySize        int      `json:"keySize"` // Size in bytes
	NumKeys        int      `json:"numKeys"`
	CreatedKeys    []string `json:"createdKeys"`
	EndTime        int64    `json:"endTime"`
	ExecutionId    string   `json:"executionId"`
	CycleCount     int      `json:"cycleCount"`
	TotalCreated   int      `json:"totalCreated"`
	TotalDeleted   int      `json:"totalDeleted"`
	LastError      string   `json:"lastError"`
}

// Track running attacks for background goroutines
type bigKeyAttackStats struct {
	stopChan     chan struct{}
	cycleCount   int
	totalCreated int
	totalDeleted int
	lastError    string
	mu           sync.Mutex
}

var (
	runningAttacks   = make(map[string]*bigKeyAttackStats)
	runningAttacksMu sync.Mutex
)

var _ action_kit_sdk.Action[BigKeyState] = (*bigKeyAttack)(nil)
var _ action_kit_sdk.ActionWithStop[BigKeyState] = (*bigKeyAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[BigKeyState] = (*bigKeyAttack)(nil)

func NewBigKeyAttack() action_kit_sdk.Action[BigKeyState] {
	return &bigKeyAttack{}
}

func (a *bigKeyAttack) NewEmptyState() BigKeyState {
	return BigKeyState{}
}

func (a *bigKeyAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.big-key",
		Label:       "Create Big Keys",
		Description: "Continuously creates and deletes large keys to stress Redis memory handling. Keys are created, held briefly, then deleted in cycles throughout the attack duration. Note: The total size of all keys (keySize × numKeys) must fit within Redis maxmemory limit.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(redisIcon),
		TargetSelection: extutil.Ptr(action_kit_api.TargetSelection{
			TargetType: TargetTypeInstance,
			SelectionTemplates: extutil.Ptr([]action_kit_api.TargetSelectionTemplate{
				{
					Label:       "by host and port",
					Description: extutil.Ptr("Find Redis instance by host and port"),
					Query:       "redis.host=\"\" AND redis.port=\"\"",
				},
			}),
		}),
		Technology:  extutil.Ptr("Redis"),
		Category:    extutil.Ptr("resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long to run the big key attack cycles"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "keySize",
				Label:        "Key Size (MB)",
				Description:  extutil.Ptr("Size of each key value in megabytes. Ensure keySize × numKeys is less than Redis maxmemory."),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("10"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "numKeys",
				Label:        "Number of Keys",
				Description:  extutil.Ptr("Number of big keys to create per cycle. Total memory used per cycle will be keySize × numKeys."),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("1"),
				Required:     extutil.Ptr(true),
			},
		},
	}
}

func (a *bigKeyAttack) Prepare(ctx context.Context, state *BigKeyState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds
	keySizeMB := int(extutil.ToInt64(request.Config["keySize"]))
	numKeys := int(extutil.ToInt64(request.Config["numKeys"]))

	if keySizeMB < 1 {
		keySizeMB = 1
	}
	if numKeys < 1 {
		numKeys = 1
	}

	executionId := uuid.New().String()[:8]

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.KeyPrefix = fmt.Sprintf("steadybit-bigkey-%s-", executionId)
	state.KeySize = keySizeMB * 1024 * 1024 // Convert MB to bytes
	state.NumKeys = numKeys
	state.CreatedKeys = []string{}
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.ExecutionId = executionId
	state.CycleCount = 0
	state.TotalCreated = 0
	state.TotalDeleted = 0

	return nil, nil
}

func (a *bigKeyAttack) Start(ctx context.Context, state *BigKeyState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Get Redis memory info for better error messages
	var maxMemory, usedMemory string
	memoryInfo, err := clients.GetRedisInfo(ctx, client, "memory")
	if err == nil {
		if val, ok := memoryInfo["maxmemory_human"]; ok && val != "0B" {
			maxMemory = val
		}
		if val, ok := memoryInfo["used_memory_human"]; ok {
			usedMemory = val
		}
	}

	// Test creating one key first to validate memory is available
	keySizeMB := state.KeySize / (1024 * 1024)
	requestedTotalMB := keySizeMB * state.NumKeys

	testKey := fmt.Sprintf("%stest", state.KeyPrefix)
	testValue := generateBigValue(state.KeySize)
	testErr := client.Set(ctx, testKey, testValue, 0).Err()
	if testErr != nil {
		// Clean up test key just in case
		client.Del(ctx, testKey)

		errMsg := fmt.Sprintf("Failed to create big keys. Requested %d keys × %d MB = %d MB total.", state.NumKeys, keySizeMB, requestedTotalMB)
		if maxMemory != "" {
			errMsg += fmt.Sprintf(" Redis maxmemory is %s (currently using %s).", maxMemory, usedMemory)
			errMsg += " Reduce keySize or numKeys to fit within Redis memory limits."
		}
		errMsg += fmt.Sprintf(" Error: %v", testErr)

		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Error),
					Message: errMsg,
				},
			}),
		}, fmt.Errorf("failed to create big keys: %s", errMsg)
	}
	// Clean up test key
	client.Del(ctx, testKey)

	// Create stats tracker for this execution
	stats := &bigKeyAttackStats{
		stopChan: make(chan struct{}),
	}
	runningAttacksMu.Lock()
	runningAttacks[state.ExecutionId] = stats
	runningAttacksMu.Unlock()

	// Start background goroutine for continuous create/delete cycles
	go a.runBigKeyCycles(state, stats)

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Started big key attack: %d keys × %d MB = %d MB per cycle. Redis maxmemory: %s", state.NumKeys, keySizeMB, requestedTotalMB, maxMemory),
			},
		}),
	}, nil
}

func (a *bigKeyAttack) runBigKeyCycles(state *BigKeyState, stats *bigKeyAttackStats) {
	keySizeMB := state.KeySize / (1024 * 1024)
	cycleCount := 0

	for {
		select {
		case <-stats.stopChan:
			log.Info().Str("executionId", state.ExecutionId).Msg("Big key attack stopped")
			return
		default:
			// Create a new client for each cycle
			client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
			if err != nil {
				log.Error().Err(err).Msg("Failed to create Redis client for big key cycle")
				stats.mu.Lock()
				stats.lastError = err.Error()
				stats.mu.Unlock()
				time.Sleep(time.Second)
				continue
			}

			// Generate big value
			bigValue := generateBigValue(state.KeySize)

			// Create keys
			var createdKeys []string
			for i := 0; i < state.NumKeys; i++ {
				select {
				case <-stats.stopChan:
					// Clean up any keys we just created
					for _, key := range createdKeys {
						client.Del(context.Background(), key)
					}
					client.Close()
					return
				default:
				}

				keyName := fmt.Sprintf("%scycle%d-key%d", state.KeyPrefix, cycleCount, i)
				err := client.Set(context.Background(), keyName, bigValue, 0).Err()
				if err != nil {
					log.Warn().Err(err).Str("key", keyName).Int("keySize", keySizeMB).Msg("Failed to create big key")
					stats.mu.Lock()
					stats.lastError = err.Error()
					stats.mu.Unlock()
					continue
				}
				createdKeys = append(createdKeys, keyName)
				stats.mu.Lock()
				stats.totalCreated++
				stats.mu.Unlock()
			}

			// Hold the keys briefly (500ms) to stress memory
			select {
			case <-stats.stopChan:
				// Clean up before exiting
				for _, key := range createdKeys {
					client.Del(context.Background(), key)
					stats.mu.Lock()
					stats.totalDeleted++
					stats.mu.Unlock()
				}
				client.Close()
				return
			case <-time.After(500 * time.Millisecond):
			}

			// Delete the keys
			for _, key := range createdKeys {
				err := client.Del(context.Background(), key).Err()
				if err != nil {
					log.Warn().Err(err).Str("key", key).Msg("Failed to delete big key")
				} else {
					stats.mu.Lock()
					stats.totalDeleted++
					stats.mu.Unlock()
				}
			}

			client.Close()
			cycleCount++
			stats.mu.Lock()
			stats.cycleCount = cycleCount
			stats.mu.Unlock()

			log.Debug().
				Int("cycle", cycleCount).
				Int("created", len(createdKeys)).
				Msg("Completed big key cycle")

			// Small pause between cycles
			select {
			case <-stats.stopChan:
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

func generateBigValue(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, size)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func (a *bigKeyAttack) Status(ctx context.Context, state *BigKeyState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	var memUsed string
	if err == nil {
		defer client.Close()
		memoryInfo, err := clients.GetRedisInfo(ctx, client, "memory")
		if err == nil {
			if used, ok := memoryInfo["used_memory_human"]; ok {
				memUsed = used
			}
		}
	}

	// Get stats from running attack
	var cycleCount, totalCreated, totalDeleted int
	var lastError string
	runningAttacksMu.Lock()
	if stats, ok := runningAttacks[state.ExecutionId]; ok {
		stats.mu.Lock()
		cycleCount = stats.cycleCount
		totalCreated = stats.totalCreated
		totalDeleted = stats.totalDeleted
		lastError = stats.lastError
		stats.mu.Unlock()
	}
	runningAttacksMu.Unlock()

	keySizeMB := state.KeySize / (1024 * 1024)
	statusMsg := fmt.Sprintf("Big key cycles: %d completed, %d keys created, %d keys deleted (%d MB each). Redis memory: %s",
		cycleCount, totalCreated, totalDeleted, keySizeMB, memUsed)

	if lastError != "" {
		statusMsg += fmt.Sprintf(" Last error: %s", lastError)
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: statusMsg,
			},
		}),
	}, nil
}

func (a *bigKeyAttack) Stop(ctx context.Context, state *BigKeyState) (*action_kit_api.StopResult, error) {
	// Get stats and signal the background goroutine to stop
	var cycleCount, totalCreated, totalDeleted int
	runningAttacksMu.Lock()
	if stats, ok := runningAttacks[state.ExecutionId]; ok {
		close(stats.stopChan)
		stats.mu.Lock()
		cycleCount = stats.cycleCount
		totalCreated = stats.totalCreated
		totalDeleted = stats.totalDeleted
		stats.mu.Unlock()
		delete(runningAttacks, state.ExecutionId)
	}
	runningAttacksMu.Unlock()

	// Give the goroutine a moment to clean up
	time.Sleep(200 * time.Millisecond)

	// Clean up any remaining keys (safety net)
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return &action_kit_api.StopResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("Big key attack stopped but failed to connect for cleanup: %v", err),
				},
			}),
		}, nil
	}
	defer client.Close()

	// Scan for any remaining keys with our prefix and delete them
	var cursor uint64 = 0
	cleanedUp := 0
	for {
		var keys []string
		var err error
		keys, cursor, err = client.Scan(ctx, cursor, state.KeyPrefix+"*", 100).Result()
		if err != nil {
			log.Warn().Err(err).Msg("Failed to scan for remaining big keys")
			break
		}

		for _, key := range keys {
			if err := client.Del(ctx, key).Err(); err != nil {
				log.Warn().Err(err).Str("key", key).Msg("Failed to delete remaining big key")
			} else {
				cleanedUp++
			}
		}

		if cursor == 0 {
			break
		}
	}

	keySizeMB := state.KeySize / (1024 * 1024)
	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Big key attack completed: %d cycles, %d keys created, %d keys deleted (%d MB each). Cleaned up %d remaining keys.",
					cycleCount, totalCreated, totalDeleted, keySizeMB, cleanedUp),
			},
		}),
	}, nil
}
