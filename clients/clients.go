/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package clients

import (
	"context"
	"crypto/tls"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-redis/config"
	"strings"
	"time"
)

// CreateRedisClient creates a new Redis client from an endpoint configuration
func CreateRedisClient(endpoint *config.RedisEndpoint) (*redis.Client, error) {
	opts, err := parseRedisURL(endpoint)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opts)
	return client, nil
}

// CreateRedisClientFromURL creates a Redis client from a URL string with optional password override
func CreateRedisClientFromURL(url string, password string, db int) (*redis.Client, error) {
	endpoint := config.GetEndpointByURL(url)

	var opts *redis.Options
	var err error

	if endpoint != nil {
		opts, err = parseRedisURL(endpoint)
		if err != nil {
			return nil, err
		}
	} else {
		opts, err = redis.ParseURL(url)
		if err != nil {
			return nil, err
		}
	}

	if password != "" {
		opts.Password = password
	}
	if db >= 0 {
		opts.DB = db
	}

	client := redis.NewClient(opts)
	return client, nil
}

func parseRedisURL(endpoint *config.RedisEndpoint) (*redis.Options, error) {
	opts, err := redis.ParseURL(endpoint.URL)
	if err != nil {
		return nil, err
	}

	// Override with explicit settings
	if endpoint.Password != "" {
		opts.Password = endpoint.Password
	}
	if endpoint.Username != "" {
		opts.Username = endpoint.Username
	}
	if endpoint.DB > 0 {
		opts.DB = endpoint.DB
	}

	// Configure TLS if using rediss://
	if strings.HasPrefix(endpoint.URL, "rediss://") {
		if opts.TLSConfig == nil {
			opts.TLSConfig = &tls.Config{}
		}
		opts.TLSConfig.InsecureSkipVerify = endpoint.InsecureSkipVerify
		opts.TLSConfig.MinVersion = tls.VersionTLS12
	}

	// Set reasonable defaults
	opts.DialTimeout = 5 * time.Second
	opts.ReadTimeout = 3 * time.Second
	opts.WriteTimeout = 3 * time.Second
	opts.PoolSize = 10
	opts.MinIdleConns = 1

	return opts, nil
}

// PingRedis checks if a Redis connection is working
func PingRedis(ctx context.Context, client *redis.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := client.Ping(ctx).Result()
	if err != nil {
		log.Warn().Err(err).Msg("Redis ping failed")
		return err
	}
	return nil
}

// GetRedisInfo retrieves Redis INFO command output
func GetRedisInfo(ctx context.Context, client *redis.Client, section string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var result string
	var err error

	if section != "" {
		result, err = client.Info(ctx, section).Result()
	} else {
		result, err = client.Info(ctx).Result()
	}
	if err != nil {
		return nil, err
	}

	return parseInfoResult(result), nil
}

func parseInfoResult(info string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(info, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	return result
}
