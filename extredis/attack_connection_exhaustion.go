/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"sync"
	"time"
)

type connectionExhaustionAttack struct{}

type ConnectionExhaustionState struct {
	RedisURL        string `json:"redisUrl"`
	Password        string `json:"password"`
	DB              int    `json:"db"`
	NumConnections  int    `json:"numConnections"`
	EndTime         int64  `json:"endTime"`
	ConnectionCount int    `json:"connectionCount"`
}

// Track active connections for cleanup
var (
	activeConnections      = make(map[string][]*redis.Client)
	activeConnectionsMutex sync.Mutex
)

var _ action_kit_sdk.Action[ConnectionExhaustionState] = (*connectionExhaustionAttack)(nil)
var _ action_kit_sdk.ActionWithStop[ConnectionExhaustionState] = (*connectionExhaustionAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[ConnectionExhaustionState] = (*connectionExhaustionAttack)(nil)

func NewConnectionExhaustionAttack() action_kit_sdk.Action[ConnectionExhaustionState] {
	return &connectionExhaustionAttack{}
}

func (a *connectionExhaustionAttack) NewEmptyState() ConnectionExhaustionState {
	return ConnectionExhaustionState{}
}

func (a *connectionExhaustionAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.connection-exhaustion",
		Label:       "Exhaust Connections",
		Description: "Opens many connections to Redis to test connection limit handling",
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
				Description:  extutil.Ptr("How long to hold connections open"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "numConnections",
				Label:        "Number of Connections",
				Description:  extutil.Ptr("Number of connections to open"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("100"),
				Required:     extutil.Ptr(true),
				MinValue:          extutil.Ptr(1),
				MaxValue:          extutil.Ptr(10000),
			},
		},
	}
}

func (a *connectionExhaustionAttack) Prepare(ctx context.Context, state *ConnectionExhaustionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds
	numConnections := int(extutil.ToInt64(request.Config["numConnections"]))

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.NumConnections = numConnections
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.ConnectionCount = 0

	return nil, nil
}

func (a *connectionExhaustionAttack) Start(ctx context.Context, state *ConnectionExhaustionState) (*action_kit_api.StartResult, error) {
	// Test connection first
	testClient, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	if err := clients.PingRedis(ctx, testClient); err != nil {
		testClient.Close()
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}
	testClient.Close()

	// Generate unique key for this attack instance
	attackKey := fmt.Sprintf("%s-%d", state.RedisURL, time.Now().UnixNano())

	// Open connections
	var connections []*redis.Client
	successCount := 0
	failCount := 0

	for i := 0; i < state.NumConnections; i++ {
		client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
		if err != nil {
			failCount++
			log.Debug().Err(err).Int("index", i).Msg("Failed to create connection")
			continue
		}

		// Verify the connection works
		err = clients.PingRedis(ctx, client)
		if err != nil {
			client.Close()
			failCount++
			log.Debug().Err(err).Int("index", i).Msg("Failed to ping on new connection")
			continue
		}

		connections = append(connections, client)
		successCount++
	}

	// Store connections for cleanup
	activeConnectionsMutex.Lock()
	activeConnections[attackKey] = connections
	activeConnectionsMutex.Unlock()

	state.ConnectionCount = successCount

	messages := []action_kit_api.Message{
		{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Opened %d connections to Redis", successCount),
		},
	}

	if failCount > 0 {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Warn),
			Message: fmt.Sprintf("Failed to open %d connections", failCount),
		})
	}

	// Start background goroutine to keep connections alive
	go a.keepAlive(attackKey, state.EndTime)

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr(messages),
	}, nil
}

func (a *connectionExhaustionAttack) keepAlive(attackKey string, endTime int64) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if time.Now().Unix() >= endTime {
			return
		}

		activeConnectionsMutex.Lock()
		connections := activeConnections[attackKey]
		activeConnectionsMutex.Unlock()

		// Ping all connections to keep them alive
		for _, client := range connections {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = client.Ping(ctx).Err()
			cancel()
		}
	}
}

func (a *connectionExhaustionAttack) Status(ctx context.Context, state *ConnectionExhaustionState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	// Get current connection count from Redis
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	var connectedClients string
	if err == nil {
		clientsInfo, err := clients.GetRedisInfo(ctx, client, "clients")
		if err == nil {
			if cc, ok := clientsInfo["connected_clients"]; ok {
				connectedClients = cc
			}
		}
		client.Close()
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Holding %d connections, Redis total connections: %s", state.ConnectionCount, connectedClients),
			},
		}),
	}, nil
}

func (a *connectionExhaustionAttack) Stop(ctx context.Context, state *ConnectionExhaustionState) (*action_kit_api.StopResult, error) {
	// Find and close all connections for this attack
	attackKey := ""
	activeConnectionsMutex.Lock()
	for key := range activeConnections {
		if len(key) > len(state.RedisURL) && key[:len(state.RedisURL)] == state.RedisURL {
			attackKey = key
			break
		}
	}

	var connections []*redis.Client
	if attackKey != "" {
		connections = activeConnections[attackKey]
		delete(activeConnections, attackKey)
	}
	activeConnectionsMutex.Unlock()

	// Close all connections
	closedCount := 0
	for _, client := range connections {
		if err := client.Close(); err != nil {
			log.Warn().Err(err).Msg("Error closing connection")
		} else {
			closedCount++
		}
	}

	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Closed %d connections", closedCount),
			},
		}),
	}, nil
}

