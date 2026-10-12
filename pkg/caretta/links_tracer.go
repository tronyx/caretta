package caretta

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/groundcover-com/caretta/pkg/k8s"
	"github.com/groundcover-com/caretta/pkg/tracing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	pollsMade = promauto.NewCounter(prometheus.CounterOpts{
		Name: "caretta_polls_made",
		Help: "Counter of polls made by caretta",
	})
	failedConnectionDeletion = promauto.NewCounter(prometheus.CounterOpts{
		Name: "caretta_failed_deletions",
		Help: "Counter of failed deletion of closed connection from map",
	})
	filteredLoopbackConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "caretta_current_loopback_connections",
		Help: `Number of loopback connections observed in the last iteration`,
	})
	mapSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "caretta_ebpf_connections_map_size",
		Help: "number of items in the connections map iterated from user space per iteration",
	})
	mapDeletions = promauto.NewCounter(prometheus.CounterOpts{
		Name: "caretta_connection_deletions",
		Help: "total number of deletions from the map done by the userspace",
	})
	failedMapIterations = promauto.NewCounter(prometheus.CounterOpts{
		Name: "caretta_failed_map_iterations",
		Help: "Counter of polls skipped because reading the connections map failed partway",
	})
	pollDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "caretta_poll_duration_seconds",
		Help:    "Time spent on each poll: reading the connections map and resolving its connections into links",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 14),
	})
)

type IPResolver interface {
	ResolveIP(string) k8s.Workload
	StartWatching() error
	StopWatching()
}

type Probes interface {
	UnloadProbes() error
}

type LinksTracer struct {
	ebpfObjects Probes
	connections IEbpfMap
	resolver    IPResolver

	// Links not seen live in the eBPF map for longer than linkTTL are dropped; 0 keeps them forever.
	linkTTL  time.Duration
	lastSeen map[NetworkLink]time.Time
	now      func() time.Time
}

// initializes a LinksTracer object
func NewTracer(resolver *k8s.K8sIPResolver, linkTTL time.Duration) LinksTracer {
	return LinksTracer{resolver: resolver, linkTTL: linkTTL}
}

func NewTracerWithObjs(resolver IPResolver, connections IEbpfMap, probes Probes) LinksTracer {
	return LinksTracer{
		ebpfObjects: probes,
		connections: connections,
		resolver:    resolver,
	}
}

func (tracer *LinksTracer) Start(maxConnections uint32) error {
	objs, connMap, err := tracing.LoadProbes(maxConnections)
	if err != nil {
		return err
	}

	tracer.ebpfObjects = &objs
	tracer.connections = &EbpfMap{innerMap: connMap}
	return nil
}

func (tracer *LinksTracer) Stop() error {
	tracer.resolver.StopWatching()
	return tracer.ebpfObjects.UnloadProbes()
}

// a single polling from the eBPF maps
// iterating the traces from the kernel-space, summing each network link.
// If the map can't be read in full, nothing is deleted from it and an error is returned instead of partial results.
func (tracer *LinksTracer) TracesPollingIteration(pastLinks map[NetworkLink]uint64) (map[NetworkLink]uint64, map[NetworkLink]uint64, []TcpConnection, error) {
	defer prometheus.NewTimer(pollDuration).ObserveDuration()
	// outline of an iteration -
	// filter unwanted connections, sum all connections as links, add past links, and return the new map
	pollsMade.Inc()
	loopbackCounter := 0
	now := time.Now()
	if tracer.now != nil {
		now = tracer.now()
	}
	if tracer.linkTTL > 0 && tracer.lastSeen == nil {
		tracer.lastSeen = make(map[NetworkLink]time.Time)
	}

	currentLinks := make(map[NetworkLink]uint64)
	currentTcpConnections := []TcpConnection{}
	var connectionsToDelete []ConnectionIdentifier

	var conn ConnectionIdentifier
	var throughput ConnectionThroughputStats

	entries := tracer.connections.Iterate()
	// iterate the map from the eBPF program
	itemsCounter := 0
	for entries.Next(&conn, &throughput) {
		itemsCounter += 1
		// filter unnecessary connection

		if throughput.IsActive == 0 {
			connectionsToDelete = append(connectionsToDelete, conn)
		}

		// skip loopback connections
		if conn.Tuple.SrcIp == conn.Tuple.DstIp && isAddressLoopback(conn.Tuple.DstIp) {
			loopbackCounter++
			continue
		}

		// filter unroled connections (probably indicates a bug)
		link, err := tracer.reduceConnectionToLink(conn)
		if conn.Role == UnknownConnectionRole || err != nil {
			continue
		}

		tcpConn, err := tracer.reduceConnectionToTcp(conn, throughput)
		if err != nil {
			continue
		}

		currentLinks[link] += throughput.BytesSent
		currentTcpConnections = append(currentTcpConnections, tcpConn)
		if tracer.linkTTL > 0 {
			tracer.lastSeen[link] = now
		}
	}
	if err := entries.Err(); err != nil {
		failedMapIterations.Inc()
		return pastLinks, nil, nil, fmt.Errorf("reading the connections map: %w", err)
	}

	mapSize.Set(float64(itemsCounter))
	filteredLoopbackConnections.Set(float64(loopbackCounter))

	// add past links
	for pastLink, pastThroughput := range pastLinks {
		currentLinks[pastLink] += pastThroughput
	}

	// delete connections marked to delete
	for _, conn := range connectionsToDelete {
		tracer.deleteAndStoreConnection(&conn, pastLinks)
	}

	tracer.expireIdleLinks(now, pastLinks, currentLinks)

	return pastLinks, currentLinks, currentTcpConnections, nil

}

func (tracer *LinksTracer) expireIdleLinks(now time.Time, pastLinks, currentLinks map[NetworkLink]uint64) {
	if tracer.linkTTL <= 0 {
		return
	}
	// Closed links that were never live here (e.g. loopback) start their TTL now.
	for link := range pastLinks {
		if _, ok := tracer.lastSeen[link]; !ok {
			tracer.lastSeen[link] = now
		}
	}
	for link, seen := range tracer.lastSeen {
		if now.Sub(seen) > tracer.linkTTL {
			delete(tracer.lastSeen, link)
			delete(pastLinks, link)
			delete(currentLinks, link)
		}
	}
}

func (tracer *LinksTracer) deleteAndStoreConnection(conn *ConnectionIdentifier, pastLinks map[NetworkLink]uint64) {
	// newer kernels introduce batch map operation, but it might not be available so we delete item-by-item
	var throughput ConnectionThroughputStats
	err := tracer.connections.Lookup(conn, &throughput)
	if err != nil {
		slog.Warn("Couldn't read a closed connection before deleting it; skipping it", "error", err)
		failedConnectionDeletion.Inc()
		return
	}
	err = tracer.connections.Delete(conn)
	if err != nil {
		slog.Warn("Couldn't delete a closed connection from the map", "error", err)
		failedConnectionDeletion.Inc()
		return
	}
	// if deletion is successful, add it to past links
	link, err := tracer.reduceConnectionToLink(*conn)
	if err != nil {
		slog.Warn("Couldn't turn a deleted connection into a link", "error", err)
		return
	}

	pastLinks[link] += throughput.BytesSent

	mapDeletions.Inc()
}

// reduce a specific connection to a general link
func (tracer *LinksTracer) reduceConnectionToLink(connection ConnectionIdentifier) (NetworkLink, error) {
	var link NetworkLink
	link.Role = connection.Role

	srcWorkload := tracer.resolver.ResolveIP(IP(connection.Tuple.SrcIp).String())
	dstWorkload := tracer.resolver.ResolveIP(IP(connection.Tuple.DstIp).String())

	if connection.Role == ClientConnectionRole {
		// Src is Client, Dst is Server, Port is DstPort
		link.Client = srcWorkload
		link.Server = dstWorkload
		link.ServerPort = connection.Tuple.DstPort
	} else if connection.Role == ServerConnectionRole {
		// Dst is Client, Src is Server, Port is SrcPort
		link.Client = dstWorkload
		link.Server = srcWorkload
		link.ServerPort = connection.Tuple.SrcPort
	} else {
		return NetworkLink{}, errors.New("connection's role is unknown")
	}
	return link, nil
}

// reduce a specific connection to a general tcp connection
func (tracer *LinksTracer) reduceConnectionToTcp(connection ConnectionIdentifier, throughput ConnectionThroughputStats) (TcpConnection, error) {
	var tcpConn TcpConnection
	tcpConn.Role = connection.Role

	srcWorkload := tracer.resolver.ResolveIP(IP(connection.Tuple.SrcIp).String())
	dstWorkload := tracer.resolver.ResolveIP(IP(connection.Tuple.DstIp).String())

	if connection.Role == ClientConnectionRole {
		// Src is Client, Dst is Server, Port is DstPort
		tcpConn.Client = srcWorkload
		tcpConn.Server = dstWorkload
		tcpConn.ServerPort = connection.Tuple.DstPort
		tcpConn.State = TcpConnectionOpenState
	} else if connection.Role == ServerConnectionRole {
		// Dst is Client, Src is Server, Port is SrcPort
		tcpConn.Client = dstWorkload
		tcpConn.Server = srcWorkload
		tcpConn.ServerPort = connection.Tuple.SrcPort
		tcpConn.State = TcpConnectionAcceptState
	} else {
		return TcpConnection{}, errors.New("connection's role is unknown")
	}

	if throughput.IsActive == 0 {
		tcpConn.State = TcpConnectionClosedState
	}

	return tcpConn, nil
}

func isAddressLoopback(ip uint32) bool {
	ipAddr := make(net.IP, 4)
	binary.LittleEndian.PutUint32(ipAddr, ip)
	return ipAddr.IsLoopback()
}
