/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
)

type cacheExpirationAttack struct{}

type CacheExpirationState struct {
	RedisURL       string   `json:"redisUrl"`
	Password       string   `json:"password"`
	DB             int      `json:"db"`
	Pattern        string   `json:"pattern"`
	MaxKeys        int      `json:"maxKeys"`
	TTLSeconds     int      `json:"ttlSeconds"`
	AffectedKeys   []string `json:"affectedKeys"`
	EndTime        int64    `json:"endTime"`
}

var _ action_kit_sdk.Action[CacheExpirationState] = (*cacheExpirationAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[CacheExpirationState] = (*cacheExpirationAttack)(nil)

func NewCacheExpirationAttack() action_kit_sdk.Action[CacheExpirationState] {
	return &cacheExpirationAttack{}
}

func (a *cacheExpirationAttack) NewEmptyState() CacheExpirationState {
	return CacheExpirationState{}
}

func (a *cacheExpirationAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.database.cache-expiration",
		Label:       "Force Cache Expiration",
		Description: "Sets TTL on keys matching a pattern to force them to expire",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(redisIcon),
		TargetSelection: extutil.Ptr(action_kit_api.TargetSelection{
			TargetType: TargetTypeDatabase,
			SelectionTemplates: extutil.Ptr([]action_kit_api.TargetSelectionTemplate{
				{
					Label:       "by host and database",
					Description: extutil.Ptr("Find Redis database by host and index"),
					Query:       "redis.host=\"\" AND redis.database.index=\"\"",
				},
			}),
		}),
		Technology:  extutil.Ptr("Redis"),
		Category:    extutil.Ptr("state"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long the attack should last (for status tracking)"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "pattern",
				Label:        "Key Pattern",
				Description:  extutil.Ptr("Pattern to match keys for expiration (e.g., 'session:*', 'cache:*')"),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: extutil.Ptr("steadybit-test:*"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "ttl",
				Label:        "TTL (seconds)",
				Description:  extutil.Ptr("Time-to-live in seconds before keys expire. Set to 1 for immediate expiration."),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("5"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "maxKeys",
				Label:        "Max Keys",
				Description:  extutil.Ptr("Maximum number of keys to affect (0 = unlimited, use with caution)"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("100"),
				Required:     extutil.Ptr(true),
			},
		},
	}
}

func (a *cacheExpirationAttack) Prepare(ctx context.Context, state *CacheExpirationState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	dbIndex := request.Target.Attributes[AttrDatabaseIndex]
	db := 0
	if len(dbIndex) > 0 {
		db, _ = strconv.Atoi(dbIndex[0])
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds
	pattern := extutil.ToString(request.Config["pattern"])
	ttl := int(extutil.ToInt64(request.Config["ttl"]))
	maxKeys := int(extutil.ToInt64(request.Config["maxKeys"]))

	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if ttl < 1 {
		ttl = 1
	}

	state.RedisURL = redisURL[0]
	state.DB = db
	state.Pattern = pattern
	state.TTLSeconds = ttl
	state.MaxKeys = maxKeys
	state.AffectedKeys = []string{}
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()

	return nil, nil
}

func (a *cacheExpirationAttack) Start(ctx context.Context, state *CacheExpirationState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Find keys matching pattern using SCAN (non-blocking)
	var keysToExpire []string
	var cursor uint64 = 0

	for {
		var keys []string
		var err error
		keys, cursor, err = client.Scan(ctx, cursor, state.Pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to scan keys: %w", err)
		}

		keysToExpire = append(keysToExpire, keys...)

		// Check max keys limit
		if state.MaxKeys > 0 && len(keysToExpire) >= state.MaxKeys {
			keysToExpire = keysToExpire[:state.MaxKeys]
			break
		}

		if cursor == 0 {
			break
		}
	}

	if len(keysToExpire) == 0 {
		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("No keys found matching pattern '%s'", state.Pattern),
				},
			}),
		}, nil
	}

	// Set TTL on matching keys
	expireCount := 0
	ttlDuration := time.Duration(state.TTLSeconds) * time.Second

	for _, key := range keysToExpire {
		err := client.Expire(ctx, key, ttlDuration).Err()
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to set TTL on key")
			continue
		}
		state.AffectedKeys = append(state.AffectedKeys, key)
		expireCount++
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Set TTL of %d seconds on %d keys matching pattern '%s'", state.TTLSeconds, expireCount, state.Pattern),
			},
		}),
	}, nil
}

func (a *cacheExpirationAttack) Status(ctx context.Context, state *CacheExpirationState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	// Check how many keys still exist
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	remainingKeys := 0
	if err == nil {
		defer client.Close()
		for _, key := range state.AffectedKeys {
			exists, err := client.Exists(ctx, key).Result()
			if err == nil && exists > 0 {
				remainingKeys++
			}
		}
	}

	expiredCount := len(state.AffectedKeys) - remainingKeys

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Cache expiration: %d/%d keys expired", expiredCount, len(state.AffectedKeys)),
			},
		}),
	}, nil
}
