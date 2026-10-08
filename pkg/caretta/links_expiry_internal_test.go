package caretta

import (
	"errors"
	"testing"
	"time"

	"github.com/groundcover-com/caretta/pkg/k8s"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

type stubResolver struct{}

func (stubResolver) ResolveIP(ip string) k8s.Workload {
	return k8s.Workload{Name: ip, Namespace: "ns", Kind: "Deployment"}
}
func (stubResolver) StartWatching() error { return nil }
func (stubResolver) StopWatching()        {}

type stubMap struct {
	entries map[ConnectionIdentifier]ConnectionThroughputStats
}

type stubIterator struct {
	keys    []ConnectionIdentifier
	entries map[ConnectionIdentifier]ConnectionThroughputStats
}

func (it *stubIterator) Next(key interface{}, val interface{}) bool {
	if len(it.keys) == 0 {
		return false
	}
	*key.(*ConnectionIdentifier) = it.keys[0]
	*val.(*ConnectionThroughputStats) = it.entries[it.keys[0]]
	it.keys = it.keys[1:]
	return true
}

func (m *stubMap) Lookup(key interface{}, val interface{}) error {
	stats, ok := m.entries[*key.(*ConnectionIdentifier)]
	if !ok {
		return errors.New("not found")
	}
	*val.(*ConnectionThroughputStats) = stats
	return nil
}

func (m *stubMap) Iterate() IEbpfMapIterator {
	keys := make([]ConnectionIdentifier, 0, len(m.entries))
	for key := range m.entries {
		keys = append(keys, key)
	}
	return &stubIterator{keys: keys, entries: m.entries}
}

func (m *stubMap) Delete(key interface{}) error {
	delete(m.entries, *key.(*ConnectionIdentifier))
	return nil
}

func TestIdleLinksExpireAfterTTL(t *testing.T) {
	assert := assert.New(t)
	conn := ConnectionIdentifier{
		Id:    1,
		Tuple: ConnectionTuple{SrcIp: 1, DstIp: 2, SrcPort: 40000, DstPort: 80},
		Role:  ClientConnectionRole,
	}
	connections := &stubMap{entries: map[ConnectionIdentifier]ConnectionThroughputStats{
		conn: {BytesSent: 100, IsActive: 1},
	}}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tracer := LinksTracer{
		connections: connections,
		resolver:    stubResolver{},
		linkTTL:     time.Hour,
		now:         func() time.Time { return clock },
	}
	pastLinks := map[NetworkLink]uint64{}

	pastLinks, links, _ := tracer.TracesPollingIteration(pastLinks)
	assert.Len(links, 1, "a live link is reported")

	connections.entries[conn] = ConnectionThroughputStats{BytesSent: 150, IsActive: 0}
	clock = clock.Add(time.Minute)
	pastLinks, links, _ = tracer.TracesPollingIteration(pastLinks)
	assert.Len(links, 1, "a link is reported in the poll that sees it close")
	assert.Empty(connections.entries, "a closed connection is removed from the map")

	clock = clock.Add(59 * time.Minute)
	pastLinks, links, _ = tracer.TracesPollingIteration(pastLinks)
	assert.Equal(uint64(150), sumValues(links), "a closed link keeps its bytes until the TTL passes")

	clock = clock.Add(2 * time.Minute)
	pastLinks, links, _ = tracer.TracesPollingIteration(pastLinks)
	assert.Empty(links, "a link idle for longer than the TTL is dropped")
	assert.Empty(pastLinks)
	assert.Empty(tracer.lastSeen)
}

func TestZeroTTLKeepsLinks(t *testing.T) {
	conn := ConnectionIdentifier{Tuple: ConnectionTuple{SrcIp: 1, DstIp: 2, DstPort: 80}, Role: ClientConnectionRole}
	connections := &stubMap{entries: map[ConnectionIdentifier]ConnectionThroughputStats{conn: {BytesSent: 10}}}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tracer := LinksTracer{connections: connections, resolver: stubResolver{}, now: func() time.Time { return clock }}

	pastLinks, _, _ := tracer.TracesPollingIteration(map[NetworkLink]uint64{})
	clock = clock.Add(24 * 365 * time.Hour)
	_, links, _ := tracer.TracesPollingIteration(pastLinks)
	assert.Len(t, links, 1)
	assert.Nil(t, tracer.lastSeen)
}

func TestCollectorServesOnlyTheLatestPoll(t *testing.T) {
	assert := assert.New(t)
	c := &linksCollector{}
	assert.Equal(0, testutil.CollectAndCount(c), "nothing is served before the first poll")

	client := k8s.Workload{Name: "client", Namespace: "ns", Kind: "Deployment"}
	server := k8s.Workload{Name: "server", Namespace: "ns", Kind: "Deployment"}
	link := NetworkLink{Client: client, Server: server, ServerPort: 80, Role: ClientConnectionRole}
	connections := []TcpConnection{
		{Client: client, Server: server, ServerPort: 80, Role: ClientConnectionRole, State: TcpConnectionOpenState},
		{Client: client, Server: server, ServerPort: 80, Role: ClientConnectionRole, State: TcpConnectionClosedState},
	}

	c.update(map[NetworkLink]uint64{link: 42}, connections)
	assert.Equal(1, testutil.CollectAndCount(c, "caretta_links_observed"))
	assert.Equal(1, testutil.CollectAndCount(c, "caretta_tcp_states"), "connections with the same labels are served once")

	c.update(map[NetworkLink]uint64{}, nil)
	assert.Equal(0, testutil.CollectAndCount(c), "series from earlier polls are gone")
}

func sumValues(links map[NetworkLink]uint64) uint64 {
	var total uint64
	for _, v := range links {
		total += v
	}
	return total
}
