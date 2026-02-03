/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"math/rand"
	"sync"
	"time"
)

type memoryFillAttack struct{}

type MemoryFillState struct {
	RedisURL    string   `json:"redisUrl"`
	Password    string   `json:"password"`
	DB          int      `json:"db"`
	KeyPrefix   string   `json:"keyPrefix"`
	ValueSize   int      `json:"valueSize"`
	FillRate    int      `json:"fillRate"` // MB per second
	MaxMemory   int      `json:"maxMemory"`
	EndTime     int64    `json:"endTime"`
	CreatedKeys []string `json:"createdKeys"`
}

var _ action_kit_sdk.Action[MemoryFillState] = (*memoryFillAttack)(nil)
var _ action_kit_sdk.ActionWithStop[MemoryFillState] = (*memoryFillAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[MemoryFillState] = (*memoryFillAttack)(nil)

func NewMemoryFillAttack() action_kit_sdk.Action[MemoryFillState] {
	return &memoryFillAttack{}
}

func (a *memoryFillAttack) NewEmptyState() MemoryFillState {
	return MemoryFillState{}
}

func (a *memoryFillAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.memory-fill",
		Label:       "Fill Memory",
		Description: "Fills Redis memory with random data to simulate memory pressure",
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
				Description:  extutil.Ptr("How long to fill memory"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "valueSize",
				Label:        "Value Size",
				Description:  extutil.Ptr("Size of each value in bytes"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("10240"),
				Required:     extutil.Ptr(true),
				Advanced:     extutil.Ptr(true),
			},
			{
				Name:         "fillRate",
				Label:        "Fill Rate",
				Description:  extutil.Ptr("Rate to fill memory in MB per second"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("10"),
				Required:     extutil.Ptr(true),
				Advanced:     extutil.Ptr(true),
			},
			{
				Name:         "maxMemory",
				Label:        "Max Memory (MB)",
				Description:  extutil.Ptr("Maximum memory to fill in MB (0 = unlimited)"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("100"),
				Required:     extutil.Ptr(true),
			},
		},
	}
}

func (a *memoryFillAttack) Prepare(ctx context.Context, state *MemoryFillState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds
	valueSize := extutil.ToInt64(request.Config["valueSize"])
	fillRate := extutil.ToInt64(request.Config["fillRate"])
	maxMemory := extutil.ToInt64(request.Config["maxMemory"])

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.KeyPrefix = fmt.Sprintf("steadybit-memfill-%s-", uuid.New().String()[:8])
	state.ValueSize = int(valueSize)
	state.FillRate = int(fillRate)
	state.MaxMemory = int(maxMemory)
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.CreatedKeys = []string{}

	return nil, nil
}

func (a *memoryFillAttack) Start(ctx context.Context, state *MemoryFillState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Start filling memory in background
	go a.fillMemory(state)

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Started filling Redis memory with prefix %s", state.KeyPrefix),
			},
		}),
	}, nil
}

func (a *memoryFillAttack) fillMemory(state *MemoryFillState) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create Redis client for memory fill")
		return
	}
	defer client.Close()

	ctx := context.Background()

	// Calculate delay between writes to achieve fill rate
	bytesPerSecond := state.FillRate * 1024 * 1024
	writesPerSecond := bytesPerSecond / state.ValueSize
	if writesPerSecond < 1 {
		writesPerSecond = 1
	}
	delay := time.Duration(1000/writesPerSecond) * time.Millisecond

	// Generate random value template
	valueTemplate := generateRandomValue(state.ValueSize)

	var mu sync.Mutex
	totalBytes := 0
	maxBytes := state.MaxMemory * 1024 * 1024

	keyCounter := 0
	for time.Now().Unix() < state.EndTime {
		if maxBytes > 0 && totalBytes >= maxBytes {
			log.Info().Int("totalBytes", totalBytes).Msg("Max memory limit reached")
			break
		}

		keyName := fmt.Sprintf("%s%d", state.KeyPrefix, keyCounter)

		err := client.Set(ctx, keyName, valueTemplate, 0).Err()
		if err != nil {
			log.Warn().Err(err).Str("key", keyName).Msg("Failed to set key")
			// Continue trying, but slow down
			time.Sleep(100 * time.Millisecond)
			continue
		}

		mu.Lock()
		state.CreatedKeys = append(state.CreatedKeys, keyName)
		totalBytes += state.ValueSize
		mu.Unlock()

		keyCounter++
		time.Sleep(delay)
	}

	log.Info().
		Int("keysCreated", keyCounter).
		Int("totalBytes", totalBytes).
		Msg("Memory fill completed")
}

func generateRandomValue(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, size)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func (a *memoryFillAttack) Status(ctx context.Context, state *MemoryFillState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: completed,
		}, nil
	}
	defer client.Close()

	// Get current memory usage
	memoryInfo, err := clients.GetRedisInfo(ctx, client, "memory")
	var memUsed string
	if err == nil {
		if used, ok := memoryInfo["used_memory_human"]; ok {
			memUsed = used
		}
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Keys created: %d, Memory used: %s", len(state.CreatedKeys), memUsed),
			},
		}),
	}, nil
}

func (a *memoryFillAttack) Stop(ctx context.Context, state *MemoryFillState) (*action_kit_api.StopResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client for cleanup: %w", err)
	}
	defer client.Close()

	// Delete all created keys
	deletedCount := 0
	for _, key := range state.CreatedKeys {
		err := client.Del(ctx, key).Err()
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to delete key during cleanup")
		} else {
			deletedCount++
		}
	}

	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Cleaned up %d keys (prefix: %s)", deletedCount, state.KeyPrefix),
			},
		}),
	}, nil
}
