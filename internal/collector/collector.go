// Package collector implements a Prometheus collector for the Headscale API.
package collector

import (
	"context"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/hibare/headscale-client-go/v1/client"
	"github.com/hibare/headscale-client-go/v1/nodes"
	"github.com/hibare/headscale-client-go/v1/users"
	"github.com/prometheus/client_golang/prometheus"
)

const defaultTimeout = 10 * time.Second

// BuildInfo describes the exporter build.
type BuildInfo struct {
	Version string
	Commit  string
}

// Collector gathers Headscale node, user, and route metrics.
type Collector struct {
	client    client.ClientInterface
	timeout   time.Duration
	logger    *slog.Logger
	buildInfo BuildInfo

	up             *prometheus.Desc
	scrapeDuration *prometheus.Desc
	scrapeErrors   prometheus.Counter

	users       *prometheus.Desc
	usersOnline *prometheus.Desc
	userNodes   *prometheus.Desc
	nodes       *prometheus.Desc
	nodesOnline *prometheus.Desc

	nodeInfo            *prometheus.Desc
	nodeOnline          *prometheus.Desc
	nodeLastSeen        *prometheus.Desc
	nodeCreated         *prometheus.Desc
	nodeExpiry          *prometheus.Desc
	nodeApprovedRoutes  *prometheus.Desc
	nodeAvailableRoutes *prometheus.Desc
	nodeSubnetRoutes    *prometheus.Desc
	nodeIsExitNode      *prometheus.Desc

	routes    *prometheus.Desc
	routeInfo *prometheus.Desc

	exporterBuildInfo *prometheus.Desc
}

// New creates a Collector.
func New(apiClient client.ClientInterface, timeout time.Duration, logger *slog.Logger, buildInfo BuildInfo) *Collector {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	if buildInfo.Version == "" {
		buildInfo.Version = "dev"
	}
	if buildInfo.Commit == "" {
		buildInfo.Commit = "none"
	}

	return &Collector{
		client:    apiClient,
		timeout:   timeout,
		logger:    logger,
		buildInfo: buildInfo,

		up: prometheus.NewDesc(
			"headscale_up",
			"Whether the last scrape of the Headscale API succeeded (1) or failed (0).",
			nil, nil,
		),
		scrapeDuration: prometheus.NewDesc(
			"headscale_scrape_duration_seconds",
			"Duration of the last Headscale API scrape in seconds.",
			nil, nil,
		),
		scrapeErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "headscale_scrape_errors_total",
			Help: "Total number of failed Headscale API scrapes.",
		}),

		users: prometheus.NewDesc(
			"headscale_users",
			"Total number of registered users.",
			nil, nil,
		),
		usersOnline: prometheus.NewDesc(
			"headscale_users_online",
			"Number of registered users with at least one online node.",
			nil, nil,
		),
		userNodes: prometheus.NewDesc(
			"headscale_user_nodes",
			"Number of online and offline nodes per user.",
			[]string{"user", "status"}, nil,
		),
		nodes: prometheus.NewDesc(
			"headscale_nodes",
			"Total number of nodes.",
			nil, nil,
		),
		nodesOnline: prometheus.NewDesc(
			"headscale_nodes_online",
			"Number of online nodes.",
			nil, nil,
		),

		nodeInfo: prometheus.NewDesc(
			"headscale_node_info",
			"Static information about a Headscale node. The value is always 1.",
			[]string{"id", "node", "given_name", "user", "ipv4", "ipv6", "register_method", "tags", "exit_node"}, nil,
		),
		nodeOnline: prometheus.NewDesc(
			"headscale_node_online",
			"Whether the node is online (1) or offline (0).",
			[]string{"id", "node", "user"}, nil,
		),
		nodeLastSeen: prometheus.NewDesc(
			"headscale_node_last_seen_timestamp_seconds",
			"Unix timestamp of the last time the node was seen. 0 means unknown.",
			[]string{"id", "node", "user"}, nil,
		),
		nodeCreated: prometheus.NewDesc(
			"headscale_node_created_timestamp_seconds",
			"Unix timestamp of when the node was registered.",
			[]string{"id", "node", "user"}, nil,
		),
		nodeExpiry: prometheus.NewDesc(
			"headscale_node_expiry_timestamp_seconds",
			"Unix timestamp of when the node key expires. 0 means no expiry.",
			[]string{"id", "node", "user"}, nil,
		),
		nodeApprovedRoutes: prometheus.NewDesc(
			"headscale_node_approved_routes",
			"Number of routes approved for the node.",
			[]string{"id", "node", "user"}, nil,
		),
		nodeAvailableRoutes: prometheus.NewDesc(
			"headscale_node_available_routes",
			"Number of routes advertised by the node.",
			[]string{"id", "node", "user"}, nil,
		),
		nodeSubnetRoutes: prometheus.NewDesc(
			"headscale_node_subnet_routes",
			"Number of subnet routes reported for the node, including exit routes.",
			[]string{"id", "node", "user"}, nil,
		),
		nodeIsExitNode: prometheus.NewDesc(
			"headscale_node_is_exit_node",
			"Whether the node is an exit node (1) or not (0).",
			[]string{"id", "node", "user"}, nil,
		),

		routes: prometheus.NewDesc(
			"headscale_routes",
			"Number of route entries by status.",
			[]string{"status"}, nil,
		),
		routeInfo: prometheus.NewDesc(
			"headscale_route_info",
			"Subnet route information per node. The value is always 1.",
			[]string{"id", "node", "user", "route", "approved", "available", "exit_route"}, nil,
		),

		exporterBuildInfo: prometheus.NewDesc(
			"headscale_exporter_build_info",
			"Build information about the headscale-exporter. The value is always 1.",
			[]string{"version", "commit", "go_version"}, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.up
	ch <- c.scrapeDuration
	ch <- c.scrapeErrors.Desc()
	ch <- c.users
	ch <- c.usersOnline
	ch <- c.userNodes
	ch <- c.nodes
	ch <- c.nodesOnline
	ch <- c.nodeInfo
	ch <- c.nodeOnline
	ch <- c.nodeLastSeen
	ch <- c.nodeCreated
	ch <- c.nodeExpiry
	ch <- c.nodeApprovedRoutes
	ch <- c.nodeAvailableRoutes
	ch <- c.nodeSubnetRoutes
	ch <- c.nodeIsExitNode
	ch <- c.routes
	ch <- c.routeInfo
	ch <- c.exporterBuildInfo
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	started := time.Now()
	up := 0.0

	defer func() {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, up)
		ch <- prometheus.MustNewConstMetric(c.scrapeDuration, prometheus.GaugeValue, time.Since(started).Seconds())
		ch <- c.scrapeErrors
		ch <- prometheus.MustNewConstMetric(c.exporterBuildInfo, prometheus.GaugeValue, 1, c.buildInfo.Version, c.buildInfo.Commit, runtime.Version())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	nodeResponse, err := c.client.Nodes().List(ctx, nodes.NodeListFilter{})
	if err != nil {
		c.scrapeErrors.Inc()
		c.logger.Error("failed to list Headscale nodes", "error", err)
		return
	}

	userResponse, err := c.client.Users().List(ctx, users.UserListFilter{})
	if err != nil {
		c.scrapeErrors.Inc()
		c.logger.Error("failed to list Headscale users", "error", err)
		return
	}

	up = 1
	c.collect(ch, nodeResponse.Nodes, userResponse.Users)
}

type nodeCounts struct {
	online  int
	offline int
}

func (c *Collector) collect(ch chan<- prometheus.Metric, nodeList []nodes.Node, userList []users.User) {
	registeredUsers := make(map[string]struct{}, len(userList))
	userNodeCounts := make(map[string]*nodeCounts, len(userList)+1)

	for _, user := range userList {
		name := strings.TrimSpace(user.Name)
		if name == "" {
			continue
		}
		registeredUsers[name] = struct{}{}
		if _, ok := userNodeCounts[name]; !ok {
			userNodeCounts[name] = &nodeCounts{}
		}
	}

	onlineUsers := make(map[string]struct{})
	nodesOnline := 0
	approvedRoutes := 0
	availableRoutes := 0

	for i := range nodeList {
		node := &nodeList[i]

		id := node.ID
		nodeName := displayName(node)
		userName := strings.TrimSpace(node.User.Name)
		if userName == "" {
			userName = "unknown"
		}

		if node.Online {
			nodesOnline++
		}
		if _, ok := registeredUsers[userName]; ok && node.Online {
			onlineUsers[userName] = struct{}{}
		}

		counts, ok := userNodeCounts[userName]
		if !ok {
			counts = &nodeCounts{}
			userNodeCounts[userName] = counts
		}
		if node.Online {
			counts.online++
		} else {
			counts.offline++
		}

		ipv4, ipv6 := splitIPAddresses(node.IPAddresses)
		isExitNode := node.IsExitNode()

		approvedSet := stringSet(node.ApprovedRoutes)
		availableSet := stringSet(node.AvailableRoutes)

		ch <- gauge(c.nodeInfo, 1,
			id,
			nodeName,
			strings.TrimSpace(node.GivenName),
			userName,
			ipv4,
			ipv6,
			normalizeRegisterMethod(node.RegisterMethod),
			strings.Join(node.Tags, ","),
			boolLabel(isExitNode),
		)
		ch <- gauge(c.nodeOnline, boolValue(node.Online), id, nodeName, userName)
		ch <- gauge(c.nodeLastSeen, unixOrZero(node.LastSeen), id, nodeName, userName)
		ch <- gauge(c.nodeCreated, unixOrZero(node.CreatedAt), id, nodeName, userName)
		ch <- gauge(c.nodeExpiry, unixOrZero(node.Expiry), id, nodeName, userName)
		ch <- gauge(c.nodeApprovedRoutes, float64(len(approvedSet)), id, nodeName, userName)
		ch <- gauge(c.nodeAvailableRoutes, float64(len(availableSet)), id, nodeName, userName)
		ch <- gauge(c.nodeSubnetRoutes, float64(len(node.SubnetRoutes)), id, nodeName, userName)
		ch <- gauge(c.nodeIsExitNode, boolValue(isExitNode), id, nodeName, userName)

		for route := range union(approvedSet, availableSet) {
			isApproved := approvedSet[route]
			isAvailable := availableSet[route]
			if isApproved {
				approvedRoutes++
			}
			if isAvailable {
				availableRoutes++
			}
			ch <- gauge(c.routeInfo, 1,
				id,
				nodeName,
				userName,
				route,
				boolLabel(isApproved),
				boolLabel(isAvailable),
				boolLabel(isExitRoute(route)),
			)
		}
	}

	ch <- gauge(c.users, float64(len(registeredUsers)))
	ch <- gauge(c.usersOnline, float64(len(onlineUsers)))
	ch <- gauge(c.nodes, float64(len(nodeList)))
	ch <- gauge(c.nodesOnline, float64(nodesOnline))
	ch <- gauge(c.routes, float64(approvedRoutes), "approved")
	ch <- gauge(c.routes, float64(availableRoutes), "available")

	for user, counts := range userNodeCounts {
		ch <- gauge(c.userNodes, float64(counts.online), user, "online")
		ch <- gauge(c.userNodes, float64(counts.offline), user, "offline")
	}
}

func gauge(desc *prometheus.Desc, value float64, labelValues ...string) prometheus.Metric {
	return prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labelValues...)
}

func displayName(node *nodes.Node) string {
	if name := strings.TrimSpace(node.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(node.GivenName); name != "" {
		return name
	}
	return node.ID
}

func splitIPAddresses(addresses []string) (ipv4, ipv6 string) {
	var v4, v6 []string
	for _, raw := range addresses {
		address := strings.TrimSpace(raw)
		if address == "" {
			continue
		}
		ip := net.ParseIP(address)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, address)
		} else {
			v6 = append(v6, address)
		}
	}
	return strings.Join(v4, ","), strings.Join(v6, ",")
}

func normalizeRegisterMethod(method string) string {
	normalized := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(method)), "register_method_")
	if normalized == "" {
		return "unspecified"
	}
	return normalized
}

func unixOrZero(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}

func isExitRoute(route string) bool {
	return route == nodes.ExitRouteIPv4 || route == nodes.ExitRouteIPv6
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = true
	}
	return set
}

func union(first, second map[string]bool) map[string]bool {
	result := make(map[string]bool, len(first)+len(second))
	for value := range first {
		result[value] = true
	}
	for value := range second {
		result[value] = true
	}
	return result
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
