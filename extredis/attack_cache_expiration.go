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

type KeyBackup struct {
	Value      string `json:"value"`
	TTLSeconds int64  `json:"ttlSeconds"` // -1 means no TTL (persistent), -2 means key didn't exist
}

type CacheExpirationState struct {
	RedisURL         string               `json:"redisUrl"`
	Password         string               `json:"password"`
	DB               int                  `json:"db"`
	Pattern          string               `json:"pattern"`
	MaxKeys          int                  `json:"maxKeys"`
	TTLSeconds       int                  `json:"ttlSeconds"`
	AffectedKeys     []string             `json:"affectedKeys"`
	BackupData       map[string]KeyBackup `json:"backupData"`
	RestoreOnStop    bool                 `json:"restoreOnStop"`
	EndTime          int64                `json:"endTime"`
	SkippedNonString int                  `json:"skippedNonString"`
}

var _ action_kit_sdk.Action[CacheExpirationState] = (*cacheExpirationAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[CacheExpirationState] = (*cacheExpirationAttack)(nil)
var _ action_kit_sdk.ActionWithStop[CacheExpirationState] = (*cacheExpirationAttack)(nil)

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
		Description: "Sets TTL on string keys matching a pattern to force them to expire. Non-string keys are skipped. Optionally restores keys when attack stops.",
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
				Description:  extutil.Ptr("Pattern to match keys for expiration (e.g., 'session:*', 'cache:*'). Only string keys will be affected."),
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
			{
				Name:         "restoreOnStop",
				Label:        "Restore on Stop",
				Description:  extutil.Ptr("Restore expired keys with their original values and TTLs when attack stops"),
				Type:         action_kit_api.ActionParameterTypeBoolean,
				DefaultValue: extutil.Ptr("false"),
				Required:     extutil.Ptr(false),
				Advanced:     extutil.Ptr(true),
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
	restoreOnStop := extutil.ToBool(request.Config["restoreOnStop"])

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
	state.BackupData = make(map[string]KeyBackup)
	state.RestoreOnStop = restoreOnStop
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.SkippedNonString = 0

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
	var candidateKeys []string
	var cursor uint64 = 0

	for {
		var keys []string
		var err error
		keys, cursor, err = client.Scan(ctx, cursor, state.Pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to scan keys: %w", err)
		}

		candidateKeys = append(candidateKeys, keys...)

		if cursor == 0 {
			break
		}
	}

	if len(candidateKeys) == 0 {
		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("No keys found matching pattern '%s'", state.Pattern),
				},
			}),
		}, nil
	}

	// Filter to string keys only and apply max limit
	var stringKeys []string
	skippedNonString := 0

	for _, key := range candidateKeys {
		// Check key type
		keyType, err := client.Type(ctx, key).Result()
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to get key type")
			continue
		}

		if keyType != "string" {
			skippedNonString++
			continue
		}

		stringKeys = append(stringKeys, key)

		// Check max keys limit
		if state.MaxKeys > 0 && len(stringKeys) >= state.MaxKeys {
			break
		}
	}

	state.SkippedNonString = skippedNonString

	if len(stringKeys) == 0 {
		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("No string keys found matching pattern '%s' (skipped %d non-string keys)", state.Pattern, skippedNonString),
				},
			}),
		}, nil
	}

	// Backup values and TTLs if restore is enabled, then set new TTL
	expireCount := 0
	ttlDuration := time.Duration(state.TTLSeconds) * time.Second

	for _, key := range stringKeys {
		// Backup value and TTL if restore is enabled
		if state.RestoreOnStop {
			// Get current value
			value, err := client.Get(ctx, key).Result()
			if err != nil {
				log.Warn().Err(err).Str("key", key).Msg("Failed to get key value for backup")
				continue
			}

			// Get current TTL (-1 = no expiry, -2 = key doesn't exist)
			ttl, err := client.TTL(ctx, key).Result()
			var ttlSeconds int64 = -1
			if err == nil {
				ttlSeconds = int64(ttl.Seconds())
			}

			state.BackupData[key] = KeyBackup{
				Value:      value,
				TTLSeconds: ttlSeconds,
			}
		}

		// Set new short TTL
		err := client.Expire(ctx, key, ttlDuration).Err()
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to set TTL on key")
			continue
		}
		state.AffectedKeys = append(state.AffectedKeys, key)
		expireCount++
	}

	msg := fmt.Sprintf("Set TTL of %d seconds on %d string keys matching pattern '%s'", state.TTLSeconds, expireCount, state.Pattern)
	if skippedNonString > 0 {
		msg += fmt.Sprintf(" (skipped %d non-string keys)", skippedNonString)
	}
	if state.RestoreOnStop {
		msg += ". Keys will be restored on stop."
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: msg,
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

func (a *cacheExpirationAttack) Stop(ctx context.Context, state *CacheExpirationState) (*action_kit_api.StopResult, error) {
	if !state.RestoreOnStop || len(state.BackupData) == 0 {
		return &action_kit_api.StopResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Cache expiration attack completed. %d keys were affected.", len(state.AffectedKeys)),
				},
			}),
		}, nil
	}

	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return &action_kit_api.StopResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("Failed to connect to Redis for restore: %v", err),
				},
			}),
		}, nil
	}
	defer client.Close()

	// Restore backed up keys
	restoredCount := 0
	alreadyExisted := 0

	for key, backup := range state.BackupData {
		// Check if key still exists
		exists, err := client.Exists(ctx, key).Result()
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to check key existence")
			continue
		}

		if exists > 0 {
			// Key still exists, just restore the original TTL
			alreadyExisted++
			if backup.TTLSeconds == -1 {
				// Original key had no expiry, remove TTL
				err = client.Persist(ctx, key).Err()
				if err != nil {
					log.Warn().Err(err).Str("key", key).Msg("Failed to restore TTL")
				} else {
					log.Info().Str("key", key).Msg("Restored key TTL to persistent (no expiry)")
					restoredCount++
				}
			} else if backup.TTLSeconds > 0 {
				// Restore original TTL
				err = client.Expire(ctx, key, time.Duration(backup.TTLSeconds)*time.Second).Err()
				if err != nil {
					log.Warn().Err(err).Str("key", key).Msg("Failed to restore TTL")
				} else {
					log.Info().Str("key", key).Int64("ttl", backup.TTLSeconds).Msg("Restored key TTL")
					restoredCount++
				}
			} else {
				restoredCount++
			}
		} else {
			// Key expired, recreate it with original value and TTL
			if backup.TTLSeconds == -1 {
				// No expiry
				err = client.Set(ctx, key, backup.Value, 0).Err()
				if err != nil {
					log.Warn().Err(err).Str("key", key).Msg("Failed to restore key")
				} else {
					log.Info().Str("key", key).Msg("Recreated expired key (no expiry)")
					restoredCount++
				}
			} else if backup.TTLSeconds > 0 {
				// With original TTL
				err = client.Set(ctx, key, backup.Value, time.Duration(backup.TTLSeconds)*time.Second).Err()
				if err != nil {
					log.Warn().Err(err).Str("key", key).Msg("Failed to restore key")
				} else {
					log.Info().Str("key", key).Int64("ttl", backup.TTLSeconds).Msg("Recreated expired key with TTL")
					restoredCount++
				}
			} else {
				// TTL was already expired or key didn't exist, recreate without TTL
				err = client.Set(ctx, key, backup.Value, 0).Err()
				if err != nil {
					log.Warn().Err(err).Str("key", key).Msg("Failed to restore key")
				} else {
					log.Info().Str("key", key).Msg("Recreated expired key (no expiry)")
					restoredCount++
				}
			}
		}
	}

	expiredAndRestored := restoredCount - alreadyExisted

	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Restored %d of %d keys (%d were recreated after expiration, %d had TTL restored)", restoredCount, len(state.BackupData), expiredAndRestored, alreadyExisted),
			},
		}),
	}, nil
}
