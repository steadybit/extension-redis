/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
)

type cachePenetrationAttack struct{}

type CachePenetrationState struct {
	RedisURL    string `json:"redisUrl"`
	Password    string `json:"password"`
	DB          int    `json:"db"`
	Concurrency int    `json:"concurrency"`
	KeyPrefix   string `json:"keyPrefix"`
	EndTime     int64  `json:"endTime"`
	AttackKey   string `json:"attackKey"`
}

// Track active cache penetration workers for cleanup
var (
	cachePenetrationCancels = make(map[string]context.CancelFunc)
	cachePenetrationCounts  = make(map[string]*atomic.Int64)
	cachePenetrationMutex   sync.Mutex
)

var _ action_kit_sdk.Action[CachePenetrationState] = (*cachePenetrationAttack)(nil)
var _ action_kit_sdk.ActionWithStop[CachePenetrationState] = (*cachePenetrationAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[CachePenetrationState] = (*cachePenetrationAttack)(nil)

func NewCachePenetrationAttack() action_kit_sdk.Action[CachePenetrationState] {
	return &cachePenetrationAttack{}
}

func (a *cachePenetrationAttack) NewEmptyState() CachePenetrationState {
	return CachePenetrationState{}
}

func (a *cachePenetrationAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.cache-penetration",
		Label:       "Cache Penetration",
		Description: "Continuously sends cache requests for non-existing keys to reduce Redis performance and simulate cache penetration",
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
				Description:  extutil.Ptr("How long to send cache penetration requests"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "concurrency",
				Label:        "Concurrency",
				Description:  extutil.Ptr("Number of concurrent workers sending requests"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("10"),
				Required:     extutil.Ptr(true),
				MinValue:     extutil.Ptr(1),
				MaxValue:     extutil.Ptr(100),
			},
		},
	}
}

func (a *cachePenetrationAttack) Prepare(ctx context.Context, state *CachePenetrationState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000
	concurrency := int(extutil.ToInt64(request.Config["concurrency"]))
	if concurrency < 1 {
		concurrency = 10
	}

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.Concurrency = concurrency
	state.KeyPrefix = fmt.Sprintf("steadybit-penetration-miss-%s-", uuid.New().String()[:8])
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.AttackKey = fmt.Sprintf("%s-%d", state.RedisURL, time.Now().UnixNano())

	return nil, nil
}

func (a *cachePenetrationAttack) Start(ctx context.Context, state *CachePenetrationState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	workerCtx, cancel := context.WithDeadline(context.Background(), time.Unix(state.EndTime, 0))
	counter := &atomic.Int64{}

	cachePenetrationMutex.Lock()
	cachePenetrationCancels[state.AttackKey] = cancel
	cachePenetrationCounts[state.AttackKey] = counter
	cachePenetrationMutex.Unlock()

	for i := 0; i < state.Concurrency; i++ {
		go a.sendRequests(workerCtx, state, i, counter)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Started cache penetration with %d concurrent workers", state.Concurrency),
			},
		}),
	}, nil
}

func (a *cachePenetrationAttack) sendRequests(ctx context.Context, state *CachePenetrationState, workerID int, counter *atomic.Int64) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		log.Error().Err(err).Int("worker", workerID).Msg("Failed to create Redis client for cache penetration worker")
		return
	}
	defer client.Close()

	keyCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		key := fmt.Sprintf("%sw%d-k%d", state.KeyPrefix, workerID, keyCounter)
		_ = client.Get(ctx, key).Err()
		counter.Add(1)
		keyCounter++
	}
}

func (a *cachePenetrationAttack) Status(ctx context.Context, state *CachePenetrationState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	cachePenetrationMutex.Lock()
	counter := cachePenetrationCounts[state.AttackKey]
	cachePenetrationMutex.Unlock()

	var totalRequests int64
	if counter != nil {
		totalRequests = counter.Load()
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Cache penetration: %d requests sent for non-existing keys (%d workers)", totalRequests, state.Concurrency),
			},
		}),
	}, nil
}

func (a *cachePenetrationAttack) Stop(ctx context.Context, state *CachePenetrationState) (*action_kit_api.StopResult, error) {
	cachePenetrationMutex.Lock()
	cancel := cachePenetrationCancels[state.AttackKey]
	counter := cachePenetrationCounts[state.AttackKey]
	delete(cachePenetrationCancels, state.AttackKey)
	delete(cachePenetrationCounts, state.AttackKey)
	cachePenetrationMutex.Unlock()

	if cancel != nil {
		cancel()
	}

	var totalRequests int64
	if counter != nil {
		totalRequests = counter.Load()
	}

	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Cache penetration stopped. Total requests sent: %d", totalRequests),
			},
		}),
	}, nil
}
