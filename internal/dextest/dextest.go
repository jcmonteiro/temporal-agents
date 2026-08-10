// Package dextest brings up the throwaway OpenID provider the identity suites sign
// in against.
//
// It exists for the same reason pgtest does: the bootstrap is the same everywhere
// and the image is a pinned digest, so "start a provider, point a client at it, stop
// it afterwards" is stated once. A suite says what it needs (a provider with a
// client registered) and nothing about how it is obtained.
//
// It is a normal (non _test) package because Go cannot import another package's test
// files, exactly as pgtest and wftest are.
//
// A suite that cannot reach a Docker (or compatible) daemon fails rather than skips:
// a suite that quietly skips itself reports green while exercising none of the
// protocol it exists for.
package dextest

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Image pins the provider every suite runs, by digest, so the suites test the same
// provider the local stack runs. It is stated once here; the compose file is checked
// against it by the compose suite.
const Image = "ghcr.io/dexidp/dex:v2.44.0@sha256:5d0656fce7d453c0e3b2706abf40c0d0ce5b371fb0b73b3cf714d05f35fa5f86"

// The client the suites sign in as, and where the provider sends the browser back
// to. Nothing ever listens on the redirect address: a test follows the redirects
// itself and stops at this URI, which is what a browser's address bar would show.
const (
	ClientID     = "agent-hub-test"
	ClientSecret = "agent-hub-test-secret"
	RedirectURI  = "http://127.0.0.1:8973/api/v1/auth/callback"
)

// containerPort is where Dex listens inside the container.
const containerPort = "5556"

// startTimeout bounds waiting for the provider to publish its metadata.
const startTimeout = 90 * time.Second

// Provider is a running provider, as a suite needs to describe it to an adapter.
type Provider struct {
	// Issuer is the issuer identifier, which is also where the metadata is
	// discovered from.
	Issuer string
	// ClientID and ClientSecret are the registered confidential client.
	ClientID     string
	ClientSecret string
	// RedirectURI is the callback registered for that client.
	RedirectURI string
}

// container is the throwaway provider a package's suite shares, started on first
// use, and the description of it.
var (
	container     testcontainers.Container
	provider      Provider
	containerOnce sync.Once
)

// Run runs a package's tests and stops the shared container afterwards, so a suite's
// TestMain is one line:
//
//	func TestMain(m *testing.M) { os.Exit(dextest.Run(m)) }
//
// The container is stopped explicitly because os.Exit skips deferred calls.
func Run(m *testing.M) int {
	code := m.Run()
	if container != nil {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("could not terminate the throwaway identity provider: %v", err)
		}
	}
	return code
}

// Start returns the shared provider, starting it on first use. A failure to start is
// fatal rather than a skip.
//
// The host port is chosen before the container starts, because an OpenID provider's
// issuer identifier is part of what a client verifies: the provider has to be told
// the URL it will be reached at, so it cannot be given a port that is only known
// afterwards.
func Start(t *testing.T) Provider {
	t.Helper()
	containerOnce.Do(func() {
		port, err := freePort()
		if err != nil {
			log.Fatalf("could not reserve a port for the throwaway identity provider: %v", err)
		}
		issuer := fmt.Sprintf("http://localhost:%d/dex", port)
		configPath, err := writeConfig(issuer)
		if err != nil {
			log.Fatalf("could not write the throwaway identity provider's configuration: %v", err)
		}
		ctr, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
			Started: true,
			ContainerRequest: testcontainers.ContainerRequest{
				Image: Image,
				Cmd:   []string{"dex", "serve", "/etc/dex/config.yaml"},
				Files: []testcontainers.ContainerFile{{
					HostFilePath:      configPath,
					ContainerFilePath: "/etc/dex/config.yaml",
					FileMode:          0o444,
				}},
				ExposedPorts: []string{containerPort + "/tcp"},
				// The port is published at the one the issuer names, rather than at whatever
				// the daemon would have picked: a client verifies the issuer identifier, so
				// the provider has to be reachable at exactly the URL it was configured with.
				HostConfigModifier: func(config *mobycontainer.HostConfig) {
					published, parseErr := network.ParsePort(containerPort + "/tcp")
					if parseErr != nil {
						log.Fatalf("the provider's container port is not a port: %v", parseErr)
					}
					config.PortBindings = network.PortMap{
						published: []network.PortBinding{
							{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(port)},
						},
					}
				},
				WaitingFor: wait.ForHTTP("/dex/.well-known/openid-configuration").
					WithPort(containerPort + "/tcp").
					WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
					WithStartupTimeout(startTimeout),
			},
		})
		if err != nil {
			log.Fatalf("could not start the throwaway identity provider "+
				"(is a Docker daemon running?): %v", err)
		}
		container = ctr
		provider = Provider{
			Issuer:       issuer,
			ClientID:     ClientID,
			ClientSecret: ClientSecret,
			RedirectURI:  RedirectURI,
		}
	})
	return provider
}

// freePort reserves a port by binding it and letting go again. The window between
// releasing and the container binding is a race in principle; in a test run on one
// machine it is not one worth a more elaborate scheme.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// configTemplate is the provider's whole configuration.
//
// It differs from the local stack's configuration in exactly one way that matters: a
// suite signs in through the mock connector, which returns a static identity without
// a login form, because a test asserting on redirect chains through an HTML form
// would be testing Dex's markup rather than this repository's adapter. Everything a
// client verifies — the issuer, the audience, the signature, the nonce — is
// identical to what a human's sign-in produces.
const configTemplate = `issuer: %s
storage:
  type: memory
web:
  http: 0.0.0.0:%s
oauth2:
  skipApprovalScreen: true
staticClients:
  - id: %s
    secret: %s
    name: Agent Hub (test)
    redirectURIs:
      - %s
connectors:
  - type: mockCallback
    id: mock
    name: Mock
`

// writeConfig renders the configuration into a file the container mounts.
func writeConfig(issuer string) (string, error) {
	dir, err := os.MkdirTemp("", "dextest")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf(configTemplate, issuer, containerPort, ClientID, ClientSecret, RedirectURI)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
