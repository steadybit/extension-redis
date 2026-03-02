/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"os"
	"testing"

	"github.com/steadybit/extension-redis/clients"
)

func TestMain(m *testing.M) {
	code := m.Run()
	clients.CloseAllClients()
	os.Exit(code)
}
