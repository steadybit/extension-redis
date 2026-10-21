// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steadybit/action-kit/go/action_kit_test/e2e"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tlsRedisHost          = "redis-tls.default.svc.cluster.local"
	tlsRedisURL           = "rediss://" + tlsRedisHost + ":6379"
	selfSignedCaConfigMap = "redis-self-signed-ca"
)

// TestWithMinikubeSelfSignedCertificate runs the extension against a Redis serving TLS with a self-signed
// certificate: it must refuse the connection by default, and accept it once the operator either skips
// verification or mounts the CA. Each configuration gets its own cluster, as the e2e harness cannot
// reconfigure a Deployment in place (it waits on the terminating pod of the previous revision).
func TestWithMinikubeSelfSignedCertificate(t *testing.T) {
	certDir := generateSelfSignedCert(t)
	endpointsJSON := `[{"url":"` + tlsRedisURL + `","name":"redis-tls"}]`

	scenarios := []struct {
		name string
		args []string
		test func(t *testing.T, m *e2e.Minikube, e *e2e.Extension)
	}{
		{
			name: "rejects self-signed certificate by default",
			test: testCertificateRejected,
		},
		{
			name: "accepts self-signed certificate with insecureSkipVerify",
			args: []string{"--set", "redis.insecureSkipVerify=true"},
			test: testTLSInstanceDiscovered,
		},
		{
			name: "accepts self-signed certificate with mounted CA",
			args: []string{
				"--set", "extraEnv[0].name=SSL_CERT_DIR",
				"--set", "extraEnv[0].value=/etc/ssl/extra-certs:/etc/ssl/certs",
				"--set", "extraVolumeMounts[0].name=extra-certs",
				"--set", "extraVolumeMounts[0].mountPath=/etc/ssl/extra-certs",
				"--set", "extraVolumeMounts[0].readOnly=true",
				"--set", "extraVolumes[0].name=extra-certs",
				"--set", "extraVolumes[0].configMap.name=" + selfSignedCaConfigMap,
			},
			test: testTLSInstanceDiscovered,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			extFactory := e2e.HelmExtensionFactory{
				Name: "extension-redis",
				Port: 8083,
				ExtraArgs: func(m *e2e.Minikube) []string {
					return append([]string{
						"--set", "logging.level=debug",
						"--set-json", "redis.auth.managementEndpoints=" + endpointsJSON,
					}, scenario.args...)
				},
			}
			e2e.WithMinikube(t, e2e.DefaultMinikubeOpts().AfterStart(func(m *e2e.Minikube) error {
				return installTLSRedis(m, certDir)
			}), &extFactory, []e2e.WithMinikubeTestCase{
				{Name: scenario.name, Test: scenario.test},
			})
		})
	}
}

// testCertificateRejected asserts nothing is discovered and that this is because the extension rejected the
// certificate, not because Redis was unreachable: discovery logs the per-endpoint error and returns no targets.
func testCertificateRejected(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	var logs string
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		targets, err := e.DiscoverTargets("com.steadybit.extension_redis.instance")
		if err == nil {
			require.Empty(t, targets, "no instance must be discovered from a server with an untrusted certificate")
		}
		out, err := exec.Command("kubectl", "--context", m.Profile, "-n", e.Pod.GetNamespace(), "logs", e.Pod.GetName()).CombinedOutput() //NOSONAR go:S4036
		require.NoError(t, err, string(out))
		logs = string(out)
		if strings.Contains(logs, "certificate signed by unknown authority") {
			return
		}
	}
	assert.Fail(t, "the extension did not reject the self-signed certificate", "extension logs:\n%s", logs)
}

func testTLSInstanceDiscovered(t *testing.T, _ *e2e.Minikube, e *e2e.Extension) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	target, err := e2e.PollForTarget(ctx, e, "com.steadybit.extension_redis.instance", func(t discovery_kit_api.Target) bool {
		return e2e.HasAttribute(t, "redis.host", tlsRedisHost)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{tlsRedisURL}, target.Attributes["redis.url"])
}

const tlsRedisManifest = `
apiVersion: v1
kind: Service
metadata:
  name: redis-tls
  namespace: default
spec:
  selector:
    app: redis-tls
  ports:
    - name: redis
      port: 6379
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis-tls
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis-tls
  template:
    metadata:
      labels:
        app: redis-tls
    spec:
      containers:
        - name: redis
          image: redis:7.4
          args: ["--port", "0", "--tls-port", "6379", "--tls-cert-file", "/tls/tls.crt", "--tls-key-file", "/tls/tls.key", "--tls-auth-clients", "no", "--save", ""]
          ports:
            - containerPort: 6379
          readinessProbe:
            tcpSocket:
              port: 6379
          volumeMounts:
            - name: tls
              mountPath: /tls
              readOnly: true
      volumes:
        - name: tls
          secret:
            secretName: redis-tls
`

// installTLSRedis deploys a Redis that only serves TLS with the self-signed certificate, and publishes the
// certificate as a ConfigMap the extension can mount as CA.
func installTLSRedis(m *e2e.Minikube, certDir string) error {
	kubectl := func(stdin string, args ...string) error {
		cmd := exec.Command("kubectl", append([]string{"--context", m.Profile, "-n", "default"}, args...)...) //NOSONAR go:S4036
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("kubectl %s: %s: %s", strings.Join(args, " "), err, out)
		}
		return nil
	}

	if err := kubectl("", "create", "secret", "tls", "redis-tls", "--cert", filepath.Join(certDir, "tls.crt"), "--key", filepath.Join(certDir, "tls.key")); err != nil {
		return err
	}
	if err := m.CreateConfigMap("default", selfSignedCaConfigMap, filepath.Join(certDir, "self-signed-ca.crt")); err != nil {
		return err
	}
	if err := kubectl(tlsRedisManifest, "apply", "-f", "-"); err != nil {
		return err
	}
	return kubectl("", "rollout", "status", "deployment/redis-tls", "--timeout=5m")
}

// generateSelfSignedCert writes a self-signed certificate valid for the in-cluster Redis service, its key, and
// a copy of the certificate to mount as CA, and returns the directory.
func generateSelfSignedCert(t *testing.T) string {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: tlsRedisHost},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{tlsRedisHost, "redis-tls.default.svc", "redis-tls"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDer, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.crt"), certPem, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "self-signed-ca.crt"), certPem, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0o600))
	return dir
}
