// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_test/e2e"
	actValidate "github.com/steadybit/action-kit/go/action_kit_test/validate"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	discValidate "github.com/steadybit/discovery-kit/go/discovery_kit_test/validate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithMinikube(t *testing.T) {
	// Use Bitnami Redis for e2e. We pass the extension its endpoints via Helm env.
	extFactory := e2e.HelmExtensionFactory{
		Name: "extension-redis",
		Port: 8083,
		ExtraArgs: func(m *e2e.Minikube) []string {
			endpointsJSON := `[{"url":"redis://my-redis-master.default.svc.cluster.local:6379","password":"redis-password","name":"my-redis"},` +
				`{"url":"redis://redis-cluster.default.svc.cluster.local:6379","password":"redis-password","name":"my-redis-cluster"}]`
			return []string{
				"--set", "logging.level=debug",
				"--set-json", "redis.auth.managementEndpoints=" + endpointsJSON,
			}
		},
	}

	e2e.WithMinikube(t,
		e2e.DefaultMinikubeOpts().AfterStart(installRedis),
		&extFactory,
		[]e2e.WithMinikubeTestCase{
			{Name: "validate discovery", Test: validateDiscovery},
			{Name: "validate actions", Test: validateActions},
			{Name: "discover instances", Test: testDiscoverInstances},
			{Name: "discover databases", Test: testDiscoverDatabases},
			{Name: "cache expiration high volume", Test: testCacheExpirationHighVolume},
			{Name: "check latency on authenticated cluster node", Test: testCheckLatencyOnAuthenticatedClusterNode},
		},
	)
}

func validateDiscovery(t *testing.T, _ *e2e.Minikube, e *e2e.Extension) {
	assert.NoError(t, discValidate.ValidateEndpointReferences("/", e.Client))
}

func validateActions(t *testing.T, _ *e2e.Minikube, e *e2e.Extension) {
	assert.NoError(t, actValidate.ValidateEndpointReferences("/", e.Client))
}

func testDiscoverInstances(t *testing.T, _ *e2e.Minikube, e *e2e.Extension) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	target, err := e2e.PollForTarget(ctx, e, "com.steadybit.extension_redis.instance", func(t discovery_kit_api.Target) bool {
		return len(t.Attributes["redis.host"]) > 0
	})
	require.NoError(t, err)
	assert.Equal(t, "com.steadybit.extension_redis.instance", target.TargetType)
	assert.NotEmpty(t, target.Attributes["redis.host"])
	assert.NotEmpty(t, target.Attributes["redis.port"])
	assert.NotEmpty(t, target.Attributes["redis.url"])
}

func testDiscoverDatabases(t *testing.T, _ *e2e.Minikube, e *e2e.Extension) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	target, err := e2e.PollForTarget(ctx, e, "com.steadybit.extension_redis.database", func(t discovery_kit_api.Target) bool {
		return len(t.Attributes["redis.database.index"]) > 0
	})
	require.NoError(t, err)
	assert.Equal(t, "com.steadybit.extension_redis.database", target.TargetType)
	assert.NotEmpty(t, target.Attributes["redis.host"])
	assert.NotEmpty(t, target.Attributes["redis.database.index"])
}

func testCacheExpirationHighVolume(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	// Populate Redis with many keys
	populateKeys(t, m, "highvol:exp:", 2000)
	defer cleanupKeys(m, "highvol:exp:*")

	target := &action_kit_api.Target{
		Attributes: map[string][]string{
			"redis.url":            {"redis://my-redis-master.default.svc.cluster.local:6379"},
			"redis.database.index": {"0"},
		},
	}

	config := map[string]any{
		"duration":      10000,
		"pattern":       "highvol:exp:*",
		"ttl":           300,
		"maxKeys":       0,
		"restoreOnStop": false,
	}

	execution, err := e.RunAction("com.steadybit.extension_redis.database.cache-expiration", target, config, nil)
	require.NoError(t, err)

	err = execution.Wait()
	require.NoError(t, err)

	// Verify TTL is set on keys (spot check a few)
	for _, idx := range []int{0, 500, 999, 1500, 1999} {
		key := fmt.Sprintf("highvol:exp:%04d", idx)
		ttl := getKeyTTL(t, m, key)
		assert.Greater(t, ttl, 0, "Key %s should have a TTL set", key)
	}
}

// testCheckLatencyOnAuthenticatedClusterNode runs an action against a node discovered from a
// password-protected Redis Cluster. The node's target URL is its own address, not the configured
// seed endpoint, so the action must still connect with the seed endpoint's credentials.
func testCheckLatencyOnAuthenticatedClusterNode(t *testing.T, _ *e2e.Minikube, e *e2e.Extension) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	node, err := e2e.PollForTarget(ctx, e, "com.steadybit.extension_redis.instance", func(t discovery_kit_api.Target) bool {
		return len(t.Attributes["redis.cluster.node_id"]) > 0
	})
	require.NoError(t, err)
	require.NotEqual(t, "redis://redis-cluster.default.svc.cluster.local:6379", node.Attributes["redis.url"][0],
		"cluster node targets should carry the node's own address")

	target := &action_kit_api.Target{Attributes: node.Attributes}
	config := map[string]any{
		"duration":     3000,
		"maxLatencyMs": 1000,
	}

	execution, err := e.RunAction("com.steadybit.extension_redis.instance.check-latency", target, config, nil)
	require.NoError(t, err)
	require.NoError(t, execution.Wait())
}

// populateKeys creates count keys with the given prefix using a Redis pipeline
func populateKeys(t *testing.T, m *e2e.Minikube, prefix string, count int) {
	t.Helper()
	// Use a pipeline command to create keys in batches to avoid timeout
	batchSize := 500
	for i := 0; i < count; i += batchSize {
		end := min(i+batchSize, count)
		// Build MSET args: key1 value1 key2 value2 ...
		args := []string{
			"kubectl", "--context", m.Profile, "-n", "default",
			"exec", "my-redis-master-0", "--",
			"redis-cli", "-a", "redis-password", "MSET",
		}
		for j := i; j < end; j++ {
			args = append(args, fmt.Sprintf("%s%04d", prefix, j), fmt.Sprintf("value-%d", j))
		}
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, "Failed to create keys batch %d-%d: %s", i, end, string(out))
	}
}

// countKeys returns the number of keys matching the pattern
func countKeys(t *testing.T, m *e2e.Minikube, pattern string) int {
	t.Helper()
	cmd := exec.Command( //NOSONAR go:S4036
		"kubectl", "--context", m.Profile, "-n", "default",
		"exec", "my-redis-master-0", "--",
		"redis-cli", "-a", "redis-password",
		"EVAL", fmt.Sprintf("local keys = redis.call('KEYS', '%s') return #keys", pattern), "0",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to count keys: %s", string(out))

	var count int
	// Parse the integer from output (ignoring the auth warning line)
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Warning") {
			continue
		}
		_, err := fmt.Sscanf(line, "%d", &count)
		if err == nil {
			return count
		}
	}
	return 0
}

// getKeyTTL returns the TTL in seconds of a key (-1 = no TTL, -2 = key doesn't exist)
func getKeyTTL(t *testing.T, m *e2e.Minikube, key string) int {
	t.Helper()
	cmd := exec.Command( //NOSONAR go:S4036
		"kubectl", "--context", m.Profile, "-n", "default",
		"exec", "my-redis-master-0", "--",
		"redis-cli", "-a", "redis-password",
		"TTL", key,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to get TTL for %s: %s", key, string(out))

	var ttl int
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Warning") {
			continue
		}
		_, err := fmt.Sscanf(line, "%d", &ttl)
		if err == nil {
			return ttl
		}
	}
	return -2
}

// cleanupKeys deletes all keys matching the pattern
func cleanupKeys(m *e2e.Minikube, pattern string) {
	exec.Command( //NOSONAR go:S4036
		"kubectl", "--context", m.Profile, "-n", "default",
		"exec", "my-redis-master-0", "--",
		"redis-cli", "-a", "redis-password",
		"EVAL", fmt.Sprintf("local keys = redis.call('KEYS', '%s') if #keys > 0 then return redis.call('DEL', unpack(keys)) end return 0", pattern), "0",
	).Run()
}

func installRedis(minikube *e2e.Minikube) error {
	if err := helmInstallRedis(minikube); err != nil {
		return err
	}
	return installRedisCluster(minikube)
}

// redisClusterManifest is a three-master, password-protected Redis Cluster built from the official
// redis image (Bitnami no longer publishes a redis-cluster image).
const redisClusterManifest = `
apiVersion: v1
kind: Service
metadata:
  name: redis-cluster
  namespace: default
spec:
  selector:
    app: redis-cluster
  ports:
    - name: redis
      port: 6379
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis-cluster
  namespace: default
spec:
  serviceName: redis-cluster
  replicas: 3
  selector:
    matchLabels:
      app: redis-cluster
  template:
    metadata:
      labels:
        app: redis-cluster
    spec:
      containers:
        - name: redis
          image: redis:7.4
          args: ["--cluster-enabled", "yes", "--requirepass", "redis-password", "--masterauth", "redis-password", "--appendonly", "no", "--save", ""]
          ports:
            - containerPort: 6379
          readinessProbe:
            exec:
              command: ["redis-cli", "-a", "redis-password", "ping"]
`

func installRedisCluster(minikube *e2e.Minikube) error {
	apply := exec.Command("kubectl", "--context", minikube.Profile, "apply", "-f", "-") //NOSONAR go:S4036
	apply.Stdin = strings.NewReader(redisClusterManifest)
	if out, err := apply.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply redis cluster: %s: %s", err, out)
	}

	if out, err := exec.Command("kubectl", "--context", minikube.Profile, "-n", "default", //NOSONAR go:S4036
		"rollout", "status", "statefulset/redis-cluster", "--timeout=5m").CombinedOutput(); err != nil {
		return fmt.Errorf("redis cluster pods not ready: %s: %s", err, out)
	}

	out, err := exec.Command("kubectl", "--context", minikube.Profile, "-n", "default", //NOSONAR go:S4036
		"get", "pods", "-l", "app=redis-cluster", "-o", "jsonpath={.items[*].status.podIP}").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get redis cluster pod IPs: %s: %s", err, out)
	}
	args := []string{"--context", minikube.Profile, "-n", "default", "exec", "redis-cluster-0", "--",
		"redis-cli", "-a", "redis-password", "--cluster", "create"}
	for ip := range strings.FieldsSeq(string(out)) {
		args = append(args, ip+":6379")
	}
	args = append(args, "--cluster-replicas", "0", "--cluster-yes")
	if out, err := exec.Command("kubectl", args...).CombinedOutput(); err != nil { //NOSONAR go:S4036
		return fmt.Errorf("failed to create redis cluster: %s: %s", err, out)
	}

	deadline := time.Now().Add(2 * time.Minute)
	for {
		out, err := exec.Command("kubectl", "--context", minikube.Profile, "-n", "default", //NOSONAR go:S4036
			"exec", "redis-cluster-0", "--", "redis-cli", "-a", "redis-password", "cluster", "info").CombinedOutput()
		if err == nil && strings.Contains(string(out), "cluster_state:ok") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("redis cluster not ready after waiting: %v: %s", err, out)
		}
		time.Sleep(5 * time.Second)
	}
}

func helmInstallRedis(minikube *e2e.Minikube) error {
	if out, err := exec.Command("helm", "repo", "add", "bitnami", "https://charts.bitnami.com/bitnami").CombinedOutput(); err != nil { //NOSONAR go:S4036
		return fmt.Errorf("failed to add repo: %s: %s", err, out)
	}

	// Single replica Redis with password authentication
	args := []string{
		"upgrade", "--install",
		"--kube-context", minikube.Profile,
		"--namespace", "default",
		"--create-namespace",
		"my-redis", "bitnami/redis",
		"--set", "auth.password=redis-password", //NOSONAR go:S2068
		"--set", "architecture=standalone",
		"--set", "master.persistence.enabled=false",
		"--set", "replica.persistence.enabled=false",
		"--wait",
		"--timeout=10m0s",
	}

	if out, err := exec.Command("helm", args...).CombinedOutput(); err != nil { //NOSONAR go:S4036
		return fmt.Errorf("failed to install redis chart: %s: %s", err, string(out))
	}

	// Wait for Redis to be ready by checking the service
	deadline := time.Now().Add(2 * time.Minute)
	for {
		cmd := exec.Command( //NOSONAR go:S4036
			"kubectl", "--context", minikube.Profile, "-n", "default",
			"exec", "my-redis-master-0", "--",
			"redis-cli", "-a", "redis-password", "ping",
		)
		out, err := cmd.CombinedOutput()
		// Check if output contains PONG (ignoring the password warning from redis-cli)
		if err == nil && strings.Contains(string(out), "PONG") {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("redis not ready after waiting: %v: %s", err, string(out))
		}
		time.Sleep(5 * time.Second)
	}

	// Create some test keys for database discovery
	if err := createTestData(minikube); err != nil {
		return fmt.Errorf("failed to create test data: %w", err)
	}

	return nil
}

func createTestData(minikube *e2e.Minikube) error {
	// Create some test keys in db0
	cmd := exec.Command( //NOSONAR go:S4036
		"kubectl", "--context", minikube.Profile, "-n", "default",
		"exec", "my-redis-master-0", "--",
		"redis-cli", "-a", "redis-password",
		"SET", "test:key1", "value1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create test key: %s: %s", err, string(out))
	}

	cmd = exec.Command( //NOSONAR go:S4036
		"kubectl", "--context", minikube.Profile, "-n", "default",
		"exec", "my-redis-master-0", "--",
		"redis-cli", "-a", "redis-password",
		"SET", "test:key2", "value2",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create test key: %s: %s", err, string(out))
	}

	return nil
}
