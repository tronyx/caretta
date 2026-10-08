package caretta

import (
	"strconv"
	"strings"
	"sync/atomic"

	caretta_k8s "github.com/groundcover-com/caretta/pkg/k8s"
	"github.com/prometheus/client_golang/prometheus"
)

var linkLabelNames = []string{
	"link_id", "client_id", "client_name", "client_namespace", "client_kind", "client_owner", "server_id", "server_name", "server_namespace", "server_kind", "server_port", "role",
}

var (
	linksDesc = prometheus.NewDesc(
		"caretta_links_observed",
		"total bytes_sent value of links observed by caretta since its launch",
		linkLabelNames, nil,
	)
	tcpStatesDesc = prometheus.NewDesc(
		"caretta_tcp_states",
		"state of TCP connections observed by caretta since its launch",
		linkLabelNames, nil,
	)
)

type labeledValue struct {
	labels []string
	value  float64
}

type linksSnapshot struct {
	links     map[string]labeledValue
	tcpStates map[string]labeledValue
}

// linksCollector serves only the latest poll's results, so series for links and connections that are gone disappear.
type linksCollector struct {
	latest atomic.Pointer[linksSnapshot]
}

var collector = &linksCollector{}

func init() {
	prometheus.MustRegister(collector)
}

func (c *linksCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- linksDesc
	ch <- tcpStatesDesc
}

func (c *linksCollector) Collect(ch chan<- prometheus.Metric) {
	snapshot := c.latest.Load()
	if snapshot == nil {
		return
	}
	for _, point := range snapshot.links {
		ch <- prometheus.MustNewConstMetric(linksDesc, prometheus.GaugeValue, point.value, point.labels...)
	}
	for _, point := range snapshot.tcpStates {
		ch <- prometheus.MustNewConstMetric(tcpStatesDesc, prometheus.GaugeValue, point.value, point.labels...)
	}
}

func (c *linksCollector) update(links map[NetworkLink]uint64, tcpConnections []TcpConnection) {
	linkPoints := make(map[string]labeledValue, len(links))
	for link, throughput := range links {
		addPoint(linkPoints, linkLabelValues(link.Client, link.Server, link.ServerPort, link.Role), float64(throughput))
	}
	tcpPoints := make(map[string]labeledValue, len(tcpConnections))
	for _, connection := range tcpConnections {
		addPoint(tcpPoints, linkLabelValues(connection.Client, connection.Server, connection.ServerPort, connection.Role), float64(connection.State))
	}
	c.latest.Store(&linksSnapshot{links: linkPoints, tcpStates: tcpPoints})
}

// Distinct links can share a label set (the server's owner isn't a label), and a duplicate would fail the whole scrape.
func addPoint(points map[string]labeledValue, labels []string, value float64) {
	points[strings.Join(labels, "\xff")] = labeledValue{labels: labels, value: value}
}

func linkLabelValues(client, server caretta_k8s.Workload, serverPort uint16, role uint32) []string {
	return []string{
		strconv.Itoa(int(fnvHash(client.Name+client.Namespace+server.Name+server.Namespace) + role)),
		strconv.Itoa(int(fnvHash(client.Name + client.Namespace))),
		client.Name,
		client.Namespace,
		client.Kind,
		client.Owner,
		strconv.Itoa(int(fnvHash(server.Name + server.Namespace))),
		server.Name,
		server.Namespace,
		server.Kind,
		strconv.Itoa(int(serverPort)),
		strconv.Itoa(int(role)),
	}
}
