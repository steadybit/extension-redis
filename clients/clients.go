/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package clients

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-redis/config"
)

// clientPool stores long-lived Redis clients keyed by (url, password, db).
var clientPool sync.Map

// ClusterNodeInfo represents a node parsed from CLUSTER NODES output.
type ClusterNodeInfo struct {
	ID    string
	Addr  string // host:port
	Role  string // "master" or "slave"
	Flags string
}

// CreateRedisClient creates a new standalone Redis client from an endpoint configuration.
func CreateRedisClient(endpoint *config.RedisEndpoint) (*redis.Client, error) {
	opts, err := parseRedisURL(endpoint)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opts)
	return client, nil
}

// GetRedisClient returns a pooled standalone Redis client for the given (url, password, db) combination.
// The returned client must NOT be closed — it is shared and long-lived.
func GetRedisClient(url string, password string, db int) (*redis.Client, error) {
	key := fmt.Sprintf("%s|%s|%d", url, password, db)
	if v, ok := clientPool.Load(key); ok {
		return v.(*redis.Client), nil
	}

	client, err := CreateRedisClientFromURL(url, password, db)
	if err != nil {
		return nil, err
	}

	actual, loaded := clientPool.LoadOrStore(key, client)
	if loaded {
		_ = client.Close()
		return actual.(*redis.Client), nil
	}
	return client, nil
}

// CloseAllClients closes all pooled Redis clients for graceful shutdown.
func CloseAllClients() {
	clientPool.Range(func(key, value any) bool {
		if client, ok := value.(*redis.Client); ok {
			_ = client.Close()
		}
		clientPool.Delete(key)
		return true
	})
}

// Deprecated: Use GetRedisClient for pooled clients.
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

	if endpoint.Password != "" {
		opts.Password = endpoint.Password
	}
	if endpoint.Username != "" {
		opts.Username = endpoint.Username
	}
	if endpoint.DB > 0 {
		opts.DB = endpoint.DB
	}

	if strings.HasPrefix(endpoint.URL, "rediss://") {
		if opts.TLSConfig == nil {
			opts.TLSConfig = &tls.Config{}
		}
		opts.TLSConfig.InsecureSkipVerify = endpoint.InsecureSkipVerify
		opts.TLSConfig.MinVersion = tls.VersionTLS12
	}

	opts.DialTimeout = 3 * time.Second
	opts.ReadTimeout = 3 * time.Second
	opts.WriteTimeout = 3 * time.Second
	opts.PoolSize = 10
	opts.MinIdleConns = 0

	return opts, nil
}

// ---------------------------------------------------------------------------
// Cluster support
// ---------------------------------------------------------------------------

// DetectClusterMode connects to the endpoint and checks redis_mode.
func DetectClusterMode(ctx context.Context, endpoint *config.RedisEndpoint) (bool, error) {
	if endpoint.ClusterMode == "cluster" {
		return true, nil
	}
	if endpoint.ClusterMode == "standalone" {
		return false, nil
	}

	// Auto-detect
	client, err := CreateRedisClient(endpoint)
	if err != nil {
		return false, err
	}
	defer client.Close()

	info, err := GetRedisInfo(ctx, client, "server")
	if err != nil {
		// If INFO server fails (e.g. miniredis), try CLUSTER INFO as fallback
		clusterInfo, clusterErr := client.ClusterInfo(ctx).Result()
		if clusterErr != nil {
			// Can't determine — assume standalone
			log.Debug().Err(err).Msg("Cannot auto-detect cluster mode, assuming standalone")
			return false, nil
		}
		return strings.Contains(clusterInfo, "cluster_state:ok"), nil
	}
	return info["redis_mode"] == "cluster", nil
}

// GetMasterNodes returns ClusterNodeInfo for each master in the cluster.
// For standalone endpoints it returns a single entry for the configured endpoint.
func GetMasterNodes(ctx context.Context, endpoint *config.RedisEndpoint) ([]ClusterNodeInfo, bool, error) {
	isCluster, err := DetectClusterMode(ctx, endpoint)
	if err != nil {
		return nil, false, err
	}

	if !isCluster {
		opts, err := parseRedisURL(endpoint)
		if err != nil {
			return nil, false, err
		}
		return []ClusterNodeInfo{{Addr: opts.Addr, Role: "master"}}, false, nil
	}

	client, err := CreateRedisClient(endpoint)
	if err != nil {
		return nil, true, err
	}
	defer client.Close()

	nodes, err := ParseClusterNodes(ctx, client)
	if err != nil {
		return nil, true, err
	}

	var masters []ClusterNodeInfo
	for _, n := range nodes {
		if n.Role == "master" {
			masters = append(masters, n)
		}
	}
	return masters, true, nil
}

// ParseClusterNodes runs CLUSTER NODES and parses the output.
func ParseClusterNodes(ctx context.Context, client *redis.Client) ([]ClusterNodeInfo, error) {
	result, err := client.ClusterNodes(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("CLUSTER NODES failed: %w", err)
	}
	return parseClusterNodesOutput(result), nil
}

func parseClusterNodesOutput(raw string) []ClusterNodeInfo {
	var nodes []ClusterNodeInfo
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: <id> <ip:port@cport[,hostname]> <flags> <master|--> <ping> <pong> <epoch> <link> <slot...>
		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}

		id := parts[0]
		addrRaw := parts[1] // e.g. "10.0.0.1:6379@16379" or "10.0.0.1:6379@16379,hostname"
		flags := parts[2]

		// Strip cport and optional hostname
		addr := addrRaw
		if idx := strings.Index(addr, "@"); idx != -1 {
			addr = addr[:idx]
		}

		// Determine role from flags
		role := "slave"
		if strings.Contains(flags, "master") {
			role = "master"
		}

		// Skip nodes in fail/noaddr state
		if strings.Contains(flags, "fail") || strings.Contains(flags, "noaddr") {
			continue
		}

		nodes = append(nodes, ClusterNodeInfo{
			ID:    id,
			Addr:  addr,
			Role:  role,
			Flags: flags,
		})
	}
	return nodes
}

// CreateDirectClient creates a standalone client connected directly to a specific address,
// inheriting credentials and TLS settings from the endpoint.
func CreateDirectClient(endpoint *config.RedisEndpoint, addr string) (*redis.Client, error) {
	opts := &redis.Options{
		Addr:         addr,
		Password:     endpoint.Password,
		Username:     endpoint.Username,
		DB:           0, // Cluster always uses DB 0
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	if strings.HasPrefix(endpoint.URL, "rediss://") {
		opts.TLSConfig = &tls.Config{
			InsecureSkipVerify: endpoint.InsecureSkipVerify,
			MinVersion:         tls.VersionTLS12,
		}
	}

	return redis.NewClient(opts), nil
}

// ForEachMaster executes fn on each master node in a cluster.
// For standalone Redis, fn is called once on the single node.
// Errors from individual nodes are collected and returned as a combined error.
func ForEachMaster(ctx context.Context, endpoint *config.RedisEndpoint, fn func(ctx context.Context, client *redis.Client, addr string) error) error {
	masters, _, err := GetMasterNodes(ctx, endpoint)
	if err != nil {
		return err
	}

	var errs []string
	for _, node := range masters {
		nodeClient, err := CreateDirectClient(endpoint, node.Addr)
		if err != nil {
			errs = append(errs, fmt.Sprintf("node %s: create client: %v", node.Addr, err))
			continue
		}
		if err := fn(ctx, nodeClient, node.Addr); err != nil {
			errs = append(errs, fmt.Sprintf("node %s: %v", node.Addr, err))
		}
		nodeClient.Close()
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors on %d/%d nodes: %s", len(errs), len(masters), strings.Join(errs, "; "))
	}
	return nil
}

// ScanAllKeys scans keys matching pattern across all master nodes in a cluster,
// or on the single node for standalone Redis. Returns deduplicated keys.
func ScanAllKeys(ctx context.Context, endpoint *config.RedisEndpoint, pattern string, maxKeys int) ([]string, error) {
	var mu sync.Mutex
	seen := make(map[string]struct{})
	var allKeys []string

	err := ForEachMaster(ctx, endpoint, func(ctx context.Context, client *redis.Client, addr string) error {
		var cursor uint64
		for {
			keys, nextCursor, err := client.Scan(ctx, cursor, pattern, 100).Result()
			if err != nil {
				return fmt.Errorf("SCAN failed: %w", err)
			}

			mu.Lock()
			for _, k := range keys {
				if _, exists := seen[k]; !exists {
					seen[k] = struct{}{}
					allKeys = append(allKeys, k)
				}
				if maxKeys > 0 && len(allKeys) >= maxKeys {
					mu.Unlock()
					return nil
				}
			}
			mu.Unlock()

			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}
		return nil
	})

	return allKeys, err
}

// ---------------------------------------------------------------------------
// Public helpers (standalone-compatible via UniversalClient)
// ---------------------------------------------------------------------------

// PingRedis checks if a Redis connection is working.
func PingRedis(ctx context.Context, client redis.Cmdable) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := client.Ping(ctx).Result()
	if err != nil {
		log.Warn().Err(err).Msg("Redis ping failed")
		return err
	}
	return nil
}

// GetRedisInfo retrieves Redis INFO command output.
func GetRedisInfo(ctx context.Context, client redis.Cmdable, section string) (map[string]string, error) {
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
	lines := strings.SplitSeq(info, "\n")

	for line := range lines {
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
