/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
)

type bgsaveAttack struct{}

type BgsaveState struct {
	RedisURL       string `json:"redisUrl"`
	Password       string `json:"password"`
	DB             int    `json:"db"`
	LastSaveBefore int64  `json:"lastSaveBefore"`
	IsElastiCache  bool   `json:"isElastiCache"`
}

// isElastiCacheHost checks if the host appears to be an AWS ElastiCache instance
func isElastiCacheHost(host string) bool {
	host = strings.ToLower(host)
	return strings.Contains(host, ".cache.amazonaws.com") ||
		strings.Contains(host, ".elasticache.") ||
		strings.Contains(host, "elasticache")
}

var _ action_kit_sdk.Action[BgsaveState] = (*bgsaveAttack)(nil)

func NewBgsaveAttack() action_kit_sdk.Action[BgsaveState] {
	return &bgsaveAttack{}
}

func (a *bgsaveAttack) NewEmptyState() BgsaveState {
	return BgsaveState{}
}

func (a *bgsaveAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.trigger-bgsave",
		Label:       "Trigger Background Save",
		Description: "Triggers a Redis BGSAVE command to initiate a background save (RDB snapshot). This can cause performance degradation during the save process, especially on large datasets.",
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
		Category:    extutil.Ptr("state"),
		TimeControl: action_kit_api.TimeControlInstantaneous,
		Kind:        action_kit_api.Attack,
		Parameters:  []action_kit_api.ActionParameter{},
	}
}

func (a *bgsaveAttack) Prepare(ctx context.Context, state *BgsaveState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	// Check if this is an ElastiCache instance based on hostname
	redisHost := request.Target.Attributes[AttrRedisHost]
	if len(redisHost) > 0 {
		state.IsElastiCache = isElastiCacheHost(redisHost[0])
	}

	state.RedisURL = redisURL[0]
	state.DB = 0

	return nil, nil
}

func (a *bgsaveAttack) Start(ctx context.Context, state *BgsaveState) (*action_kit_api.StartResult, error) {
	client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}

	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	lastSaveTimestamp, err := client.LastSave(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get last save time: %w", err)
	}
	state.LastSaveBefore = lastSaveTimestamp
	lastSaveTime := time.Unix(lastSaveTimestamp, 0)

	if inProgress, err := isBgsaveInProgress(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to get persistence info: %w", err)
	} else if inProgress {
		return bgsaveAlreadyInProgressResult(), nil
	}

	result, err := client.BgSave(ctx).Result()
	if err != nil {
		return handleBgsaveError(err, state.IsElastiCache)
	}

	usedMemory := getInfoField(ctx, client, "memory", "used_memory_human")
	keyCount := getFirstKeyspaceEntry(ctx, client)

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Triggered background save (BGSAVE). Response: %s. Redis memory: %s, Keyspace: %s. Last save was at: %s", result, usedMemory, keyCount, lastSaveTime.Format(time.RFC3339)),
			},
		}),
	}, nil
}

func isBgsaveInProgress(ctx context.Context, client *redis.Client) (bool, error) {
	info, err := clients.GetRedisInfo(ctx, client, "persistence")
	if err != nil {
		return false, err
	}
	val, ok := info["rdb_bgsave_in_progress"]
	return ok && val == "1", nil
}

func bgsaveAlreadyInProgressResult() *action_kit_api.StartResult {
	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Warn),
				Message: "A background save is already in progress",
			},
		}),
	}
}

func handleBgsaveError(err error, isElastiCache bool) (*action_kit_api.StartResult, error) {
	if err.Error() == "ERR Background save already in progress" {
		return bgsaveAlreadyInProgressResult(), nil
	}

	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "unknown command") || strings.Contains(errStr, "command not allowed") {
		return nil, fmt.Errorf("%s Original error: %w", bgsaveDisabledMessage(isElastiCache), err)
	}

	return nil, fmt.Errorf("failed to trigger BGSAVE: %w", err)
}

func bgsaveDisabledMessage(isElastiCache bool) string {
	if isElastiCache {
		return "BGSAVE command is disabled on this Redis instance." +
			" AWS ElastiCache disables administrative commands like BGSAVE, BGREWRITEAOF, SAVE, CONFIG, and DEBUG." +
			" Backups on ElastiCache are managed through AWS snapshots instead." +
			" Use 'aws elasticache create-snapshot' or the AWS Console to trigger backups."
	}
	return "BGSAVE command is disabled on this Redis instance." +
		" This is common on managed Redis services (AWS ElastiCache, Azure Cache, GCP Memorystore)" +
		" where the provider manages persistence. Check your provider's documentation for backup options."
}

func getInfoField(ctx context.Context, client *redis.Client, section, field string) string {
	info, err := clients.GetRedisInfo(ctx, client, section)
	if err != nil {
		return "unknown"
	}
	if val, ok := info[field]; ok {
		return val
	}
	return "unknown"
}

func getFirstKeyspaceEntry(ctx context.Context, client *redis.Client) string {
	info, err := clients.GetRedisInfo(ctx, client, "keyspace")
	if err != nil {
		return "unknown"
	}
	for key, val := range info {
		if strings.HasPrefix(key, "db") {
			return val
		}
	}
	return "unknown"
}
