/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/KimMachineGun/automemlimit"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/discovery-kit/go/discovery_kit_sdk"
	"github.com/steadybit/event-kit/go/event_kit_api"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/exthealth"
	"github.com/steadybit/extension-kit/exthttp"
	"github.com/steadybit/extension-kit/extlogging"
	"github.com/steadybit/extension-redis/clients"
	"github.com/steadybit/extension-redis/config"
	"github.com/steadybit/extension-redis/extredis"
	_ "go.uber.org/automaxprocs"
)

var startedAt = time.Now().Format(time.RFC3339)

func main() {
	extlogging.InitZeroLog()
	extbuild.PrintBuildInformation()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	defer cancel()
	defer clients.CloseAllClients()

	config.ParseConfiguration()
	config.ValidateConfiguration()

	exthealth.SetReady(false)
	exthealth.StartProbes(8084)

	discovery_kit_sdk.Register(extredis.NewRedisInstanceDiscovery(ctx))
	discovery_kit_sdk.Register(extredis.NewRedisDatabaseDiscovery(ctx))

	action_kit_sdk.RegisterAction(extredis.NewMemoryFillAttack())
	action_kit_sdk.RegisterAction(extredis.NewKeyDeleteAttack())
	action_kit_sdk.RegisterAction(extredis.NewConnectionExhaustionAttack())
	action_kit_sdk.RegisterAction(extredis.NewClientPauseAttack())
	action_kit_sdk.RegisterAction(extredis.NewMaxmemoryLimitAttack())
	action_kit_sdk.RegisterAction(extredis.NewCacheExpirationAttack())
	action_kit_sdk.RegisterAction(extredis.NewBigKeyAttack())
	action_kit_sdk.RegisterAction(extredis.NewSentinelStopAttack())
	action_kit_sdk.RegisterAction(extredis.NewMemoryCheck())
	action_kit_sdk.RegisterAction(extredis.NewLatencyCheck())
	action_kit_sdk.RegisterAction(extredis.NewConnectionCountCheck())
	action_kit_sdk.RegisterAction(extredis.NewReplicationLagCheck())
	action_kit_sdk.RegisterAction(extredis.NewCacheHitRateCheck())
	action_kit_sdk.RegisterAction(extredis.NewBlockedClientsCheck())

	exthttp.RegisterHttpHandler("/", exthttp.IfNoneMatchHandler(func() string { return startedAt }, exthttp.GetterAsHandler(getExtensionList)))

	action_kit_sdk.RegisterCoverageEndpoints()

	exthealth.SetReady(true)
	exthttp.Listen(exthttp.ListenOpts{Port: 8083})
}

type ExtensionListResponse struct {
	action_kit_api.ActionList       `json:",inline"`
	discovery_kit_api.DiscoveryList `json:",inline"`
	event_kit_api.EventListenerList `json:",inline"`
}

func getExtensionList() ExtensionListResponse {
	return ExtensionListResponse{
		ActionList:    action_kit_sdk.GetActionList(),
		DiscoveryList: discovery_kit_sdk.GetDiscoveryList(),
	}
}
