// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package clients

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/steadybit/extension-redis/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseClusterNodesOutput_ThreeMasters(t *testing.T) {
	raw := `07c37dfeb235213a872192d90877d0cd55635b91 127.0.0.1:30004@31004 slave e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca 0 1426238317239 4 connected
67ed2db8d677e59ec4a4cefb06858cf2a1a89fa1 127.0.0.1:30002@31002 master - 0 1426238316232 2 connected 5461-10922
292f8b365bb7edb5e285caf0b7e6ddc7265d2f4f 127.0.0.1:30003@31003 master - 0 1426238318243 3 connected 10923-16383
6ec23923021cf3ffec47b5b2738d926ca8ab5e6c 127.0.0.1:30005@31005 slave 67ed2db8d677e59ec4a4cefb06858cf2a1a89fa1 0 1426238316232 5 connected
824fe116063bc5fcf9f4ffd895bc17aee7731ac3 127.0.0.1:30006@31006 slave 292f8b365bb7edb5e285caf0b7e6ddc7265d2f4f 0 1426238317239 6 connected
e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca 127.0.0.1:30001@31001 myself,master - 0 0 1 connected 0-5460`

	nodes := parseClusterNodesOutput(raw)

	require.Len(t, nodes, 6)

	masters := 0
	slaves := 0
	for _, n := range nodes {
		if n.Role == "master" {
			masters++
		} else {
			slaves++
		}
	}
	assert.Equal(t, 3, masters)
	assert.Equal(t, 3, slaves)
}

func TestParseClusterNodesOutput_StripsCportAndHostname(t *testing.T) {
	raw := `abc123 10.0.0.1:6379@16379,my-hostname master - 0 0 1 connected 0-5460`

	nodes := parseClusterNodesOutput(raw)
	require.Len(t, nodes, 1)
	assert.Equal(t, "10.0.0.1:6379", nodes[0].Addr)
	assert.Equal(t, "master", nodes[0].Role)
}

func TestParseClusterNodesOutput_SkipsFailNodes(t *testing.T) {
	raw := `abc123 10.0.0.1:6379@16379 master,fail - 0 0 1 connected 0-5460
def456 10.0.0.2:6379@16379 master - 0 0 2 connected 5461-10922`

	nodes := parseClusterNodesOutput(raw)
	require.Len(t, nodes, 1)
	assert.Equal(t, "10.0.0.2:6379", nodes[0].Addr)
}

func TestParseClusterNodesOutput_SkipsNoaddr(t *testing.T) {
	raw := `abc123 :0@0 master,noaddr - 0 0 1 connected
def456 10.0.0.2:6379@16379 master - 0 0 2 connected 5461-10922`

	nodes := parseClusterNodesOutput(raw)
	require.Len(t, nodes, 1)
}

func TestParseClusterNodesOutput_Empty(t *testing.T) {
	nodes := parseClusterNodesOutput("")
	assert.Empty(t, nodes)
}

func TestDetectClusterMode_ExplicitStandalone(t *testing.T) {
	endpoint := &config.RedisEndpoint{
		URL:         "redis://localhost:6379",
		ClusterMode: "standalone",
	}

	isCluster, err := DetectClusterMode(context.Background(), endpoint)
	require.NoError(t, err)
	assert.False(t, isCluster)
}

func TestDetectClusterMode_ExplicitCluster(t *testing.T) {
	endpoint := &config.RedisEndpoint{
		URL:         "redis://localhost:6379",
		ClusterMode: "cluster",
	}

	isCluster, err := DetectClusterMode(context.Background(), endpoint)
	require.NoError(t, err)
	assert.True(t, isCluster)
}

func TestDetectClusterMode_AutoDetectStandalone(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL: "redis://" + mr.Addr(),
	}

	isCluster, err := DetectClusterMode(context.Background(), endpoint)
	require.NoError(t, err)
	assert.False(t, isCluster) // miniredis is standalone
}

func TestGetMasterNodes_Standalone(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL:         "redis://" + mr.Addr(),
		ClusterMode: "standalone",
	}

	nodes, isCluster, err := GetMasterNodes(context.Background(), endpoint)
	require.NoError(t, err)
	assert.False(t, isCluster)
	require.Len(t, nodes, 1)
	assert.Equal(t, "master", nodes[0].Role)
}

func TestCreateDirectClient(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL:      "redis://localhost:6379",
		Password: "secret",
	}

	client, err := CreateDirectClient(endpoint, mr.Addr())
	require.NoError(t, err)
	defer client.Close()

	// Verify it connects
	err = PingRedis(context.Background(), client)
	require.NoError(t, err)
}

func TestCreateDirectClient_TLS(t *testing.T) {
	endpoint := &config.RedisEndpoint{
		URL:                "rediss://localhost:6379",
		InsecureSkipVerify: true,
	}

	client, err := CreateDirectClient(endpoint, "localhost:6379")
	require.NoError(t, err)
	defer client.Close()

	opts := client.Options()
	require.NotNil(t, opts.TLSConfig)
	assert.True(t, opts.TLSConfig.InsecureSkipVerify)
}

func TestForEachMaster_Standalone(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL:         "redis://" + mr.Addr(),
		ClusterMode: "standalone",
	}

	callCount := 0
	err = ForEachMaster(context.Background(), endpoint, func(ctx context.Context, client *redis.Client, addr string) error {
		callCount++
		return PingRedis(ctx, client)
	})

	require.NoError(t, err)
	assert.Equal(t, 1, callCount)
}

func TestScanAllKeys_Standalone(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("test:1", "v1")
	mr.Set("test:2", "v2")
	mr.Set("other:1", "v3")

	endpoint := &config.RedisEndpoint{
		URL:         "redis://" + mr.Addr(),
		ClusterMode: "standalone",
	}

	keys, err := ScanAllKeys(context.Background(), endpoint, "test:*", 0)
	require.NoError(t, err)
	assert.Len(t, keys, 2)
}

func TestScanAllKeys_WithMaxKeys(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	for i := 0; i < 20; i++ {
		mr.Set(fmt.Sprintf("scan:%d", i), "val")
	}

	endpoint := &config.RedisEndpoint{
		URL:         "redis://" + mr.Addr(),
		ClusterMode: "standalone",
	}

	keys, err := ScanAllKeys(context.Background(), endpoint, "scan:*", 5)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(keys), 5)
}

func TestGetEndpointMaxBackupSizeBytes_Default(t *testing.T) {
	endpoint := &config.RedisEndpoint{URL: "redis://localhost:6379"}
	assert.Equal(t, int64(10*1024*1024), endpoint.GetMaxBackupSizeBytes())
}

func TestGetEndpointMaxBackupSizeBytes_Custom(t *testing.T) {
	endpoint := &config.RedisEndpoint{
		URL:                "redis://localhost:6379",
		MaxBackupSizeBytes: 50 * 1024 * 1024,
	}
	assert.Equal(t, int64(50*1024*1024), endpoint.GetMaxBackupSizeBytes())
}
