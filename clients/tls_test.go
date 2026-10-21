// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package clients

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/steadybit/extension-redis/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startSelfSignedRedis(t *testing.T) *miniredis.Miniredis {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	server, err := miniredis.RunTLS(&tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	require.NoError(t, err)
	t.Cleanup(server.Close)
	return server
}

func pingSelfSigned(t *testing.T, endpointInsecureSkipVerify bool) error {
	server := startSelfSignedRedis(t)
	client, err := CreateRedisClient(&config.RedisEndpoint{URL: "rediss://" + server.Addr(), InsecureSkipVerify: endpointInsecureSkipVerify})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client.Ping(context.Background()).Err()
}

func TestCreateRedisClient_rejects_self_signed_certificate_by_default(t *testing.T) {
	t.Setenv("STEADYBIT_EXTENSION_INSECURE_SKIP_VERIFY", "")
	assert.ErrorContains(t, pingSelfSigned(t, false), "certificate")
}

func TestCreateRedisClient_accepts_self_signed_certificate_with_endpoint_skip(t *testing.T) {
	t.Setenv("STEADYBIT_EXTENSION_INSECURE_SKIP_VERIFY", "")
	assert.NoError(t, pingSelfSigned(t, true))
}

func TestCreateRedisClient_accepts_self_signed_certificate_with_global_skip(t *testing.T) {
	t.Setenv("STEADYBIT_EXTENSION_INSECURE_SKIP_VERIFY", "true")
	assert.NoError(t, pingSelfSigned(t, false))
}
