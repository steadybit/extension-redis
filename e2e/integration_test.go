// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

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
			endpointsJSON := `[{"url":"redis://my-redis-master.default.svc.cluster.local:6379","password":"redis-password","name":"my-redis"}]`
			return []string{
				"--set", "logging.level=debug",
				"--set-json", "redis.auth.managementEndpoints=" + endpointsJSON,
			}
		},
	}

	e2e.WithMinikube(t,
		e2e.DefaultMinikubeOpts().AfterStart(helmInstallRedis),
		&extFactory,
		[]e2e.WithMinikubeTestCase{
			{Name: "validate discovery", Test: validateDiscovery},
			{Name: "validate actions", Test: validateActions},
			{Name: "discover instances", Test: testDiscoverInstances},
			{Name: "discover databases", Test: testDiscoverDatabases},
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

func helmInstallRedis(minikube *e2e.Minikube) error {
	if out, err := exec.Command("helm", "repo", "add", "bitnami", "https://charts.bitnami.com/bitnami").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add repo: %s: %s", err, out)
	}

	// Single replica Redis with password authentication
	args := []string{
		"upgrade", "--install",
		"--kube-context", minikube.Profile,
		"--namespace", "default",
		"--create-namespace",
		"my-redis", "bitnami/redis",
		"--set", "auth.password=redis-password",
		"--set", "architecture=standalone",
		"--set", "master.persistence.enabled=false",
		"--set", "replica.persistence.enabled=false",
		"--wait",
		"--timeout=10m0s",
	}

	if out, err := exec.Command("helm", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install redis chart: %s: %s", err, string(out))
	}

	// Wait for Redis to be ready by checking the service
	deadline := time.Now().Add(2 * time.Minute)
	for {
		cmd := exec.Command(
			"kubectl", "--context", minikube.Profile, "-n", "default",
			"exec", "my-redis-master-0", "--",
			"redis-cli", "-a", "redis-password", "ping",
		)
		out, err := cmd.CombinedOutput()
		if err == nil && string(out) == "PONG\n" {
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
	cmd := exec.Command(
		"kubectl", "--context", minikube.Profile, "-n", "default",
		"exec", "my-redis-master-0", "--",
		"redis-cli", "-a", "redis-password",
		"SET", "test:key1", "value1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create test key: %s: %s", err, string(out))
	}

	cmd = exec.Command(
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
