/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"github.com/steadybit/extension-redis/config"
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

// createSingleConnectionClient creates a Redis client with PoolSize=1 to ensure exactly one connection
func createSingleConnectionClient(url string, db int) (*redis.Client, error) {
	// Get endpoint config to retrieve password and other settings
	endpoint := config.GetEndpointByURL(url)

	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	// Apply endpoint config if found
	if endpoint != nil {
		if endpoint.Password != "" {
			opts.Password = endpoint.Password
		}
		if endpoint.Username != "" {
			opts.Username = endpoint.Username
		}
	}

	if db >= 0 {
		opts.DB = db
	}

	// Configure TLS if using rediss://
	if strings.HasPrefix(url, "rediss://") {
		if opts.TLSConfig == nil {
			opts.TLSConfig = &tls.Config{}
		}
		opts.TLSConfig.MinVersion = tls.VersionTLS12
		if endpoint != nil {
			opts.TLSConfig.InsecureSkipVerify = endpoint.InsecureSkipVerify
		}
	}

	// Single connection - no pooling
	opts.PoolSize = 1
	opts.MinIdleConns = 1
	opts.MaxIdleConns = 1
	opts.DialTimeout = 5 * time.Second
	opts.ReadTimeout = 3 * time.Second
	opts.WriteTimeout = 3 * time.Second

	client := redis.NewClient(opts)
	return client, nil
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
				MinValue:     extutil.Ptr(1),
				MaxValue:     extutil.Ptr(10000),
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

	// Open connections - using single-connection clients to control exact count
	var connections []*redis.Client
	successCount := 0
	failCount := 0
	var lastErr error

	for i := 0; i < state.NumConnections; i++ {
		// Use single-connection client (PoolSize=1) to ensure exact connection count
		client, err := createSingleConnectionClient(state.RedisURL, state.DB)
		if err != nil {
			failCount++
			lastErr = err
			log.Debug().Err(err).Int("index", i).Msg("Failed to create connection")
			continue
		}

		// Verify the connection works (this also establishes the connection)
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = client.Ping(pingCtx).Err()
		cancel()
		if err != nil {
			client.Close()
			failCount++
			lastErr = err
			log.Debug().Err(err).Int("index", i).Msg("Failed to ping on new connection")
			// Stop trying if we're hitting connection limits
			if failCount > 5 && successCount > 0 {
				log.Info().Int("successCount", successCount).Int("failCount", failCount).Msg("Stopping connection attempts after repeated failures")
				break
			}
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

	// If no connections were opened, return an error
	if successCount == 0 {
		errMsg := fmt.Sprintf("Failed to open any connections to Redis. Attempted %d connections.", state.NumConnections)
		if lastErr != nil {
			errMsg += fmt.Sprintf(" Last error: %v", lastErr)
		}
		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Error),
					Message: errMsg,
				},
			}),
		}, fmt.Errorf("failed to open any connections: %v", lastErr)
	}

	messages := []action_kit_api.Message{
		{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Opened %d connections to Redis (each with PoolSize=1)", successCount),
		},
	}

	if failCount > 0 {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Warn),
			Message: fmt.Sprintf("Failed to open %d connections (connection limit may be reached)", failCount),
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
	connectedClients := "unknown (connection exhausted)"
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err == nil {
		defer client.Close()
		clientsInfo, err := clients.GetRedisInfo(ctx, client, "clients")
		if err == nil {
			if cc, ok := clientsInfo["connected_clients"]; ok {
				connectedClients = cc
			}
		} else {
			log.Debug().Err(err).Msg("Failed to get Redis clients info during connection exhaustion")
		}
	} else {
		log.Debug().Err(err).Msg("Failed to create client for status check during connection exhaustion")
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
