// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"testing"

	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseDiscovery_Describe(t *testing.T) {
	// Given
	discovery := &redisDatabaseDiscovery{}

	// When
	desc := discovery.Describe()

	// Then
	assert.Equal(t, TargetTypeDatabase, desc.Id)
	require.NotNil(t, desc.Discover.CallInterval)
}

func TestDatabaseDiscovery_DescribeTarget(t *testing.T) {
	// Given
	discovery := &redisDatabaseDiscovery{}

	// When
	td := discovery.DescribeTarget()

	// Then
	assert.Equal(t, TargetTypeDatabase, td.Id)
	assert.Equal(t, "Redis database", td.Label.One)
	assert.Equal(t, "Redis databases", td.Label.Other)
	require.NotNil(t, td.Category)
	assert.Equal(t, "data store", *td.Category)
	require.NotNil(t, td.Icon)

	// table columns
	require.GreaterOrEqual(t, len(td.Table.Columns), 2)

	// Verify expected columns exist
	columnAttrs := make([]string, len(td.Table.Columns))
	for i, c := range td.Table.Columns {
		columnAttrs[i] = c.Attribute
	}
	assert.Contains(t, columnAttrs, AttrRedisHost)
	assert.Contains(t, columnAttrs, AttrDatabaseIndex)
	assert.Contains(t, columnAttrs, AttrDatabaseKeys)

	// order by
	require.Len(t, td.Table.OrderBy, 2)
	assert.Equal(t, AttrRedisHost, td.Table.OrderBy[0].Attribute)
	assert.Equal(t, discovery_kit_api.OrderByDirection("ASC"), td.Table.OrderBy[0].Direction)
}

func TestDatabaseDiscovery_DescribeAttributes(t *testing.T) {
	// Given
	discovery := &redisDatabaseDiscovery{}

	// When
	attrs := discovery.DescribeAttributes()

	// Then
	require.NotEmpty(t, attrs)

	// Verify key attributes are present
	attrMap := make(map[string]string)
	for _, a := range attrs {
		attrMap[a.Attribute] = a.Label.One
	}

	assert.Contains(t, attrMap, AttrDatabaseIndex)
	assert.Contains(t, attrMap, AttrDatabaseKeys)
	assert.Contains(t, attrMap, AttrDatabaseName)
}
