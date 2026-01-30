/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package main

import (
	"context"
	_ "net/http/pprof"
	"os/signal"
	"syscall"

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
	"github.com/steadybit/extension-redis/config"
	"github.com/steadybit/extension-redis/extredis"
	_ "go.uber.org/automaxprocs"
)

func main() {
	extlogging.InitZeroLog()
	extbuild.PrintBuildInformation()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	defer cancel()

	config.ParseConfiguration()
	config.ValidateConfiguration()

	exthealth.SetReady(false)
	exthealth.StartProbes(8084)

	exthttp.RegisterHttpHandler("/", exthttp.GetterAsHandler(getExtensionList))

	discovery_kit_sdk.Register(extredis.NewRedisInstanceDiscovery(ctx))
	discovery_kit_sdk.Register(extredis.NewRedisDatabaseDiscovery(ctx))

	action_kit_sdk.RegisterAction(extredis.NewMemoryFillAttack())
	action_kit_sdk.RegisterAction(extredis.NewKeyDeleteAttack())
	action_kit_sdk.RegisterAction(extredis.NewConnectionExhaustionAttack())
	action_kit_sdk.RegisterAction(extredis.NewClientPauseAttack())
	action_kit_sdk.RegisterAction(extredis.NewMaxmemoryLimitAttack())
	action_kit_sdk.RegisterAction(extredis.NewCacheExpirationAttack())
	action_kit_sdk.RegisterAction(extredis.NewBigKeyAttack())
	action_kit_sdk.RegisterAction(extredis.NewBgsaveAttack())
	action_kit_sdk.RegisterAction(extredis.NewMemoryCheck())
	action_kit_sdk.RegisterAction(extredis.NewLatencyCheck())
	action_kit_sdk.RegisterAction(extredis.NewConnectionCountCheck())
	action_kit_sdk.RegisterAction(extredis.NewReplicationLagCheck())
	action_kit_sdk.RegisterAction(extredis.NewCacheHitRateCheck())
	action_kit_sdk.RegisterAction(extredis.NewBlockedClientsCheck())

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
