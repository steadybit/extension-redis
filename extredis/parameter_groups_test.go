// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extredis

import (
	"testing"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/stretchr/testify/assert"
)

func TestParameterGroups(t *testing.T) {
	tests := []struct {
		name   string
		desc   action_kit_api.ActionDescription
		layout []string
	}{
		{"memory", NewMemoryCheck().Describe(), []string{"duration", "maxMemoryPercent", "maxMemoryBytes", "failEarly"}},
		{"latency", NewLatencyCheck().Describe(), []string{"duration", "maxLatencyMs", "failEarly"}},
		{"connections", NewConnectionCountCheck().Describe(), []string{"duration", "maxConnectionsPct", "maxConnections", "failEarly"}},
		{"replication", NewReplicationLagCheck().Describe(), []string{"duration", "maxLagSeconds", "requireLinkUp", "failEarly"}},
		// Restore on Stop stays out of sight: turning it off loses the expired keys for good.
		{"cacheExpiration", NewCacheExpirationAttack().Describe(), []string{"duration", "pattern", "ttl", "maxKeys", "restoreOnStop@" + action_kit_api.ParameterGroupAdditionalSettings}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.layout, layout(t, tt.desc))
			assert.Nil(t, tt.desc.ParameterGroups)
		})
	}
}

func TestAbsoluteThresholdsAreShownOnEveryPlatform(t *testing.T) {
	for _, desc := range []action_kit_api.ActionDescription{NewMemoryCheck().Describe(), NewConnectionCountCheck().Describe()} {
		for _, p := range desc.Parameters {
			if p.Name == "maxMemoryBytes" || p.Name == "maxConnections" {
				assert.Nil(t, p.Advanced, "%s: %s", desc.Id, p.Name)
			}
		}
	}
}

// layout lists the parameters in the order platforms show them, as "name" or "name@group". None of these actions sets
// an order, so the list order decides and a group is shown where its first parameter is.
func layout(t *testing.T, desc action_kit_api.ActionDescription) []string {
	t.Helper()
	var names []string
	for _, p := range desc.Parameters {
		assert.Nil(t, p.Order, "%s: parameter %s has an order, so the list order no longer decides", desc.Id, p.Name)
		if p.Group != nil {
			names = append(names, p.Name+"@"+*p.Group)
		} else {
			names = append(names, p.Name)
		}
		// No advanced parameter is left to the platform's fallback group, and the grouped ones that were advanced stay
		// advanced for platforms without groups.
		if p.Advanced != nil && *p.Advanced {
			assert.NotNil(t, p.Group, "%s: advanced parameter %s has no group", desc.Id, p.Name)
		}
		if p.Name == "failEarly" {
			assert.Nil(t, p.Advanced, "%s: failEarly is shown on every platform", desc.Id)
		}
	}
	return names
}

func definedGroups(desc action_kit_api.ActionDescription) []action_kit_api.ParameterGroup {
	if desc.ParameterGroups == nil {
		return nil
	}
	return *desc.ParameterGroups
}
