package collector

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	hsClient "github.com/hibare/headscale-client-go/v1/client"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var (
	lastSeenAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	expiryAt   = time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	createdAt  = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
)

const nodesFixture = `{
  "nodes": [
    {
      "id": "1",
      "name": "laptop",
      "givenName": "Alice Laptop",
      "user": {"id": "1", "name": "alice"},
      "ipAddresses": ["100.64.0.1", "fd7a:115c:a1e0::1"],
      "online": true,
      "lastSeen": "2026-01-02T03:04:05Z",
      "expiry": "2027-01-02T03:04:05Z",
      "createdAt": "2025-01-02T03:04:05Z",
      "registerMethod": "REGISTER_METHOD_AUTH_KEY",
      "tags": ["tag:laptop"],
      "approvedRoutes": ["192.168.1.0/24"],
      "availableRoutes": ["192.168.1.0/24", "10.0.0.0/24"],
      "subnetRoutes": ["192.168.1.0/24"]
    },
    {
      "id": "2",
      "name": "phone",
      "givenName": "",
      "user": {"id": "1", "name": "alice"},
      "ipAddresses": ["100.64.0.2"],
      "online": true,
      "lastSeen": "2026-01-02T03:04:05Z",
      "expiry": "2027-01-02T03:04:05Z",
      "createdAt": "2025-01-02T03:04:05Z",
      "registerMethod": "REGISTER_METHOD_OIDC",
      "tags": [],
      "approvedRoutes": [],
      "availableRoutes": [],
      "subnetRoutes": []
    },
    {
      "id": "3",
      "name": "server",
      "givenName": "",
      "user": {"id": "2", "name": "bob"},
      "ipAddresses": ["100.64.0.3", "fd7a:115c:a1e0::3"],
      "online": false,
      "lastSeen": null,
      "expiry": null,
      "createdAt": "2025-01-02T03:04:05Z",
      "registerMethod": "REGISTER_METHOD_CLI",
      "tags": [],
      "approvedRoutes": ["0.0.0.0/0"],
      "availableRoutes": ["0.0.0.0/0", "192.168.2.0/24"],
      "subnetRoutes": ["0.0.0.0/0", "192.168.2.0/24"]
    },
    {
      "id": "4",
      "name": "",
      "givenName": "Tagged Device",
      "user": {"id": "0", "name": "TaggedDevices"},
      "ipAddresses": ["100.64.0.4"],
      "online": true,
      "lastSeen": null,
      "expiry": null,
      "createdAt": "2025-01-02T03:04:05Z",
      "registerMethod": "REGISTER_METHOD_AUTH_KEY",
      "tags": ["tag:server"],
      "approvedRoutes": [],
      "availableRoutes": [],
      "subnetRoutes": []
    }
  ]
}`

const usersFixture = `{
  "users": [
    {
      "id": "1",
      "name": "alice",
      "createdAt": "2025-01-02T03:04:05Z",
      "displayName": "Alice",
      "email": "alice@example.com",
      "providerId": "1",
      "provider": "OIDC",
      "profilePicUrl": ""
    },
    {
      "id": "2",
      "name": "bob",
      "createdAt": "2025-01-02T03:04:05Z",
      "displayName": "Bob",
      "email": "bob@example.com",
      "providerId": "2",
      "provider": "OIDC",
      "profilePicUrl": ""
    }
  ]
}`

type discardClientLogger struct{}

func (discardClientLogger) Info(context.Context, string, ...any)  {}
func (discardClientLogger) Error(context.Context, string, ...any) {}
func (discardClientLogger) Warn(context.Context, string, ...any)  {}
func (discardClientLogger) Debug(context.Context, string, ...any) {}

func testHandler(nodesStatus, usersStatus int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, nodesStatus, nodesFixture)
	})
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, usersStatus, usersFixture)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func newTestCollector(t *testing.T, handler http.Handler) *Collector {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	apiClient, err := hsClient.NewClient(server.URL, "test-key", hsClient.ClientOptions{
		HTTPClient: server.Client(),
		Logger:     discardClientLogger{},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	discardLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(apiClient, 5*time.Second, discardLogger, BuildInfo{Version: "test", Commit: "abc123"})
}

func metricFamily(name, help, metricType, samples string) string {
	return fmt.Sprintf("# HELP %s %s\n# TYPE %s %s\n%s\n", name, help, name, metricType, strings.TrimRight(samples, "\n"))
}

func TestCollectorCounts(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusOK, http.StatusOK))

	expected := metricFamily("headscale_users", "Total number of registered users.", "gauge", "headscale_users 2") +
		metricFamily("headscale_users_online", "Number of registered users with at least one online node.", "gauge", "headscale_users_online 1") +
		metricFamily("headscale_nodes", "Total number of nodes.", "gauge", "headscale_nodes 4") +
		metricFamily("headscale_nodes_online", "Number of online nodes.", "gauge", "headscale_nodes_online 3") +
		metricFamily("headscale_routes", "Number of route entries by status.", "gauge", `
headscale_routes{status="approved"} 2
headscale_routes{status="available"} 4`) +
		metricFamily("headscale_up", "Whether the last scrape of the Headscale API succeeded (1) or failed (0).", "gauge", "headscale_up 1") +
		metricFamily("headscale_scrape_errors_total", "Total number of failed Headscale API scrapes.", "counter", "headscale_scrape_errors_total 0")

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected),
		"headscale_users",
		"headscale_users_online",
		"headscale_nodes",
		"headscale_nodes_online",
		"headscale_routes",
		"headscale_up",
		"headscale_scrape_errors_total",
	); err != nil {
		t.Fatalf("unexpected metrics:\n%v", err)
	}
}

func TestCollectorUserNodes(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusOK, http.StatusOK))

	expected := metricFamily("headscale_user_nodes", "Number of online and offline nodes per user.", "gauge", `
headscale_user_nodes{status="online",user="alice"} 2
headscale_user_nodes{status="offline",user="alice"} 0
headscale_user_nodes{status="online",user="bob"} 0
headscale_user_nodes{status="offline",user="bob"} 1
headscale_user_nodes{status="online",user="TaggedDevices"} 1
headscale_user_nodes{status="offline",user="TaggedDevices"} 0`)

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "headscale_user_nodes"); err != nil {
		t.Fatalf("unexpected metrics:\n%v", err)
	}
}

func TestCollectorNodeInfo(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusOK, http.StatusOK))

	expected := metricFamily("headscale_node_info", "Static information about a Headscale node. The value is always 1.", "gauge", `
headscale_node_info{exit_node="false",given_name="Alice Laptop",id="1",ipv4="100.64.0.1",ipv6="fd7a:115c:a1e0::1",node="laptop",register_method="auth_key",tags="tag:laptop",user="alice"} 1
headscale_node_info{exit_node="false",given_name="",id="2",ipv4="100.64.0.2",ipv6="",node="phone",register_method="oidc",tags="",user="alice"} 1
headscale_node_info{exit_node="true",given_name="",id="3",ipv4="100.64.0.3",ipv6="fd7a:115c:a1e0::3",node="server",register_method="cli",tags="",user="bob"} 1
headscale_node_info{exit_node="false",given_name="Tagged Device",id="4",ipv4="100.64.0.4",ipv6="",node="Tagged Device",register_method="auth_key",tags="tag:server",user="TaggedDevices"} 1`)

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "headscale_node_info"); err != nil {
		t.Fatalf("unexpected metrics:\n%v", err)
	}
}

func TestCollectorNodeStatus(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusOK, http.StatusOK))

	expected := metricFamily("headscale_node_online", "Whether the node is online (1) or offline (0).", "gauge", `
headscale_node_online{id="1",node="laptop",user="alice"} 1
headscale_node_online{id="2",node="phone",user="alice"} 1
headscale_node_online{id="3",node="server",user="bob"} 0
headscale_node_online{id="4",node="Tagged Device",user="TaggedDevices"} 1`) +
		metricFamily("headscale_node_approved_routes", "Number of routes approved for the node.", "gauge", `
headscale_node_approved_routes{id="1",node="laptop",user="alice"} 1
headscale_node_approved_routes{id="2",node="phone",user="alice"} 0
headscale_node_approved_routes{id="3",node="server",user="bob"} 1
headscale_node_approved_routes{id="4",node="Tagged Device",user="TaggedDevices"} 0`) +
		metricFamily("headscale_node_available_routes", "Number of routes advertised by the node.", "gauge", `
headscale_node_available_routes{id="1",node="laptop",user="alice"} 2
headscale_node_available_routes{id="2",node="phone",user="alice"} 0
headscale_node_available_routes{id="3",node="server",user="bob"} 2
headscale_node_available_routes{id="4",node="Tagged Device",user="TaggedDevices"} 0`) +
		metricFamily("headscale_node_subnet_routes", "Number of subnet routes reported for the node, including exit routes.", "gauge", `
headscale_node_subnet_routes{id="1",node="laptop",user="alice"} 1
headscale_node_subnet_routes{id="2",node="phone",user="alice"} 0
headscale_node_subnet_routes{id="3",node="server",user="bob"} 2
headscale_node_subnet_routes{id="4",node="Tagged Device",user="TaggedDevices"} 0`) +
		metricFamily("headscale_node_is_exit_node", "Whether the node is an exit node (1) or not (0).", "gauge", `
headscale_node_is_exit_node{id="1",node="laptop",user="alice"} 0
headscale_node_is_exit_node{id="2",node="phone",user="alice"} 0
headscale_node_is_exit_node{id="3",node="server",user="bob"} 1
headscale_node_is_exit_node{id="4",node="Tagged Device",user="TaggedDevices"} 0`)

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected),
		"headscale_node_online",
		"headscale_node_approved_routes",
		"headscale_node_available_routes",
		"headscale_node_subnet_routes",
		"headscale_node_is_exit_node",
	); err != nil {
		t.Fatalf("unexpected node status metrics:\n%v", err)
	}
}

func TestCollectorTimestamps(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusOK, http.StatusOK))

	lastSeenSamples := fmt.Sprintf(`
headscale_node_last_seen_timestamp_seconds{id="1",node="laptop",user="alice"} %d
headscale_node_last_seen_timestamp_seconds{id="2",node="phone",user="alice"} %d
headscale_node_last_seen_timestamp_seconds{id="3",node="server",user="bob"} 0
headscale_node_last_seen_timestamp_seconds{id="4",node="Tagged Device",user="TaggedDevices"} 0`,
		lastSeenAt.Unix(), lastSeenAt.Unix(),
	)

	createdSamples := fmt.Sprintf(`
headscale_node_created_timestamp_seconds{id="1",node="laptop",user="alice"} %d
headscale_node_created_timestamp_seconds{id="2",node="phone",user="alice"} %d
headscale_node_created_timestamp_seconds{id="3",node="server",user="bob"} %d
headscale_node_created_timestamp_seconds{id="4",node="Tagged Device",user="TaggedDevices"} %d`,
		createdAt.Unix(), createdAt.Unix(), createdAt.Unix(), createdAt.Unix(),
	)

	expirySamples := fmt.Sprintf(`
headscale_node_expiry_timestamp_seconds{id="1",node="laptop",user="alice"} %d
headscale_node_expiry_timestamp_seconds{id="2",node="phone",user="alice"} %d
headscale_node_expiry_timestamp_seconds{id="3",node="server",user="bob"} 0
headscale_node_expiry_timestamp_seconds{id="4",node="Tagged Device",user="TaggedDevices"} 0`,
		expiryAt.Unix(), expiryAt.Unix(),
	)

	expected := metricFamily("headscale_node_last_seen_timestamp_seconds", "Unix timestamp of the last time the node was seen. 0 means unknown.", "gauge", lastSeenSamples) +
		metricFamily("headscale_node_created_timestamp_seconds", "Unix timestamp of when the node was registered.", "gauge", createdSamples) +
		metricFamily("headscale_node_expiry_timestamp_seconds", "Unix timestamp of when the node key expires. 0 means no expiry.", "gauge", expirySamples)

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected),
		"headscale_node_last_seen_timestamp_seconds",
		"headscale_node_created_timestamp_seconds",
		"headscale_node_expiry_timestamp_seconds",
	); err != nil {
		t.Fatalf("unexpected timestamp metrics:\n%v", err)
	}
}

func TestCollectorRouteInfo(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusOK, http.StatusOK))

	expected := metricFamily("headscale_route_info", "Subnet route information per node. The value is always 1.", "gauge", `
headscale_route_info{approved="true",available="true",exit_route="false",id="1",node="laptop",route="192.168.1.0/24",user="alice"} 1
headscale_route_info{approved="false",available="true",exit_route="false",id="1",node="laptop",route="10.0.0.0/24",user="alice"} 1
headscale_route_info{approved="true",available="true",exit_route="true",id="3",node="server",route="0.0.0.0/0",user="bob"} 1
headscale_route_info{approved="false",available="true",exit_route="false",id="3",node="server",route="192.168.2.0/24",user="bob"} 1`)

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "headscale_route_info"); err != nil {
		t.Fatalf("unexpected metrics:\n%v", err)
	}
}

func TestCollectorNodesAPIError(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusInternalServerError, http.StatusOK))

	expected := metricFamily("headscale_up", "Whether the last scrape of the Headscale API succeeded (1) or failed (0).", "gauge", "headscale_up 0") +
		metricFamily("headscale_scrape_errors_total", "Total number of failed Headscale API scrapes.", "counter", "headscale_scrape_errors_total 1")

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected),
		"headscale_up",
		"headscale_scrape_errors_total",
	); err != nil {
		t.Fatalf("unexpected metrics:\n%v", err)
	}
}

func TestCollectorUsersAPIError(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusOK, http.StatusInternalServerError))

	expected := metricFamily("headscale_up", "Whether the last scrape of the Headscale API succeeded (1) or failed (0).", "gauge", "headscale_up 0") +
		metricFamily("headscale_scrape_errors_total", "Total number of failed Headscale API scrapes.", "counter", "headscale_scrape_errors_total 1")

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected),
		"headscale_up",
		"headscale_scrape_errors_total",
	); err != nil {
		t.Fatalf("unexpected metrics:\n%v", err)
	}
}

func TestCollectorBuildInfo(t *testing.T) {
	t.Parallel()

	collector := newTestCollector(t, testHandler(http.StatusOK, http.StatusOK))

	samples := fmt.Sprintf(`headscale_exporter_build_info{commit="abc123",go_version=%q,version="test"} 1`, runtime.Version())
	expected := metricFamily("headscale_exporter_build_info", "Build information about the headscale-exporter. The value is always 1.", "gauge", samples)

	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "headscale_exporter_build_info"); err != nil {
		t.Fatalf("unexpected metrics:\n%v", err)
	}
}
