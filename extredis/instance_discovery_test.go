// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"testing"

	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstanceDiscovery_Describe(t *testing.T) {
	// Given
	discovery := &redisInstanceDiscovery{}

	// When
	desc := discovery.Describe()

	// Then
	assert.Equal(t, TargetTypeInstance, desc.Id)
	require.NotNil(t, desc.Discover.CallInterval)
}

func TestInstanceDiscovery_DescribeTarget(t *testing.T) {
	// Given
	discovery := &redisInstanceDiscovery{}

	// When
	td := discovery.DescribeTarget()

	// Then
	assert.Equal(t, TargetTypeInstance, td.Id)
	assert.Equal(t, "Redis instance", td.Label.One)
	assert.Equal(t, "Redis instances", td.Label.Other)
	require.NotNil(t, td.Category)
	assert.Equal(t, "data store", *td.Category)
	require.NotNil(t, td.Icon)

	// table columns
	require.GreaterOrEqual(t, len(td.Table.Columns), 4)

	// Verify expected columns exist
	columnAttrs := make([]string, len(td.Table.Columns))
	for i, c := range td.Table.Columns {
		columnAttrs[i] = c.Attribute
	}
	assert.Contains(t, columnAttrs, AttrRedisName)
	assert.Contains(t, columnAttrs, AttrRedisHost)
	assert.Contains(t, columnAttrs, AttrRedisPort)

	// order by
	require.Len(t, td.Table.OrderBy, 1)
	assert.Equal(t, AttrRedisHost, td.Table.OrderBy[0].Attribute)
	assert.Equal(t, discovery_kit_api.OrderByDirection("ASC"), td.Table.OrderBy[0].Direction)
}

func TestInstanceDiscovery_DescribeAttributes(t *testing.T) {
	// Given
	discovery := &redisInstanceDiscovery{}

	// When
	attrs := discovery.DescribeAttributes()

	// Then
	require.NotEmpty(t, attrs)

	// Verify key attributes are present
	attrMap := make(map[string]string)
	for _, a := range attrs {
		attrMap[a.Attribute] = a.Label.One
	}

	assert.Contains(t, attrMap, AttrRedisURL)
	assert.Contains(t, attrMap, AttrRedisHost)
	assert.Contains(t, attrMap, AttrRedisPort)
	assert.Contains(t, attrMap, AttrRedisVersion)
	assert.Contains(t, attrMap, AttrRedisRole)
}
