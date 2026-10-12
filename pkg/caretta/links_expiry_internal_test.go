package caretta

import (
	"errors"
	"strconv"
	"strings"
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
	// Makes iteration stop after this many entries and report iterErr.
	failAfter int
	iterErr   error
}

type stubIterator struct {
	keys      []ConnectionIdentifier
	entries   map[ConnectionIdentifier]ConnectionThroughputStats
	failAfter int
	iterErr   error
	read      int
}

func (it *stubIterator) Next(key interface{}, val interface{}) bool {
	if len(it.keys) == 0 || (it.iterErr != nil && it.read == it.failAfter) {
		return false
	}
	*key.(*ConnectionIdentifier) = it.keys[0]
	*val.(*ConnectionThroughputStats) = it.entries[it.keys[0]]
	it.keys = it.keys[1:]
	it.read++
	return true
}

func (it *stubIterator) Err() error {
	if it.iterErr != nil && it.read == it.failAfter {
		return it.iterErr
	}
	return nil
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
	return &stubIterator{keys: keys, entries: m.entries, failAfter: m.failAfter, iterErr: m.iterErr}
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

	pastLinks, links, _, _ := tracer.TracesPollingIteration(pastLinks)
	assert.Len(links, 1, "a live link is reported")

	connections.entries[conn] = ConnectionThroughputStats{BytesSent: 150, IsActive: 0}
	clock = clock.Add(time.Minute)
	pastLinks, links, _, _ = tracer.TracesPollingIteration(pastLinks)
	assert.Len(links, 1, "a link is reported in the poll that sees it close")
	assert.Empty(connections.entries, "a closed connection is removed from the map")

	clock = clock.Add(59 * time.Minute)
	pastLinks, links, _, _ = tracer.TracesPollingIteration(pastLinks)
	assert.Equal(uint64(150), sumValues(links), "a closed link keeps its bytes until the TTL passes")

	clock = clock.Add(2 * time.Minute)
	pastLinks, links, _, _ = tracer.TracesPollingIteration(pastLinks)
	assert.Empty(links, "a link idle for longer than the TTL is dropped")
	assert.Empty(pastLinks)
	assert.Empty(tracer.lastSeen)
}

func TestZeroTTLKeepsLinks(t *testing.T) {
	conn := ConnectionIdentifier{Tuple: ConnectionTuple{SrcIp: 1, DstIp: 2, DstPort: 80}, Role: ClientConnectionRole}
	connections := &stubMap{entries: map[ConnectionIdentifier]ConnectionThroughputStats{conn: {BytesSent: 10}}}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tracer := LinksTracer{connections: connections, resolver: stubResolver{}, now: func() time.Time { return clock }}

	pastLinks, _, _, _ := tracer.TracesPollingIteration(map[NetworkLink]uint64{})
	clock = clock.Add(24 * 365 * time.Hour)
	_, links, _, _ := tracer.TracesPollingIteration(pastLinks)
	assert.Len(t, links, 1)
	assert.Nil(t, tracer.lastSeen)
}

func TestFailedMapReadSkipsThePoll(t *testing.T) {
	assert := assert.New(t)
	closed := ConnectionIdentifier{Id: 1, Tuple: ConnectionTuple{SrcIp: 1, DstIp: 2, DstPort: 80}, Role: ClientConnectionRole}
	live := ConnectionIdentifier{Id: 2, Tuple: ConnectionTuple{SrcIp: 1, DstIp: 3, DstPort: 80}, Role: ClientConnectionRole}
	connections := &stubMap{
		entries: map[ConnectionIdentifier]ConnectionThroughputStats{
			closed: {BytesSent: 10, IsActive: 0},
			live:   {BytesSent: 20, IsActive: 1},
		},
		failAfter: 1,
		iterErr:   errors.New("iteration aborted"),
	}
	tracer := LinksTracer{connections: connections, resolver: stubResolver{}}
	pastLinks := map[NetworkLink]uint64{}

	returnedPast, links, tcpConnections, err := tracer.TracesPollingIteration(pastLinks)
	assert.ErrorContains(err, "iteration aborted")
	assert.Nil(links, "no partial links are published")
	assert.Nil(tcpConnections)
	assert.Empty(returnedPast, "nothing moves into past links")
	assert.Len(connections.entries, 2, "nothing is deleted from the map")

	connections.iterErr = nil
	_, links, _, err = tracer.TracesPollingIteration(pastLinks)
	assert.NoError(err)
	assert.Equal(uint64(30), sumValues(links), "the next good poll sees every connection")
	assert.Len(connections.entries, 1, "and then deletes the closed one")
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

func TestCollectorSumsLinksThatShareLabels(t *testing.T) {
	client := k8s.Workload{Name: "client", Namespace: "ns", Kind: "Deployment"}
	serverA := k8s.Workload{Name: "server", Namespace: "ns", Kind: "Deployment", Owner: "a"}
	serverB := k8s.Workload{Name: "server", Namespace: "ns", Kind: "Deployment", Owner: "b"}
	c := &linksCollector{}

	// The server's owner isn't a label, so these two links are one series.
	c.update(map[NetworkLink]uint64{
		{Client: client, Server: serverA, ServerPort: 80, Role: ClientConnectionRole}: 10,
		{Client: client, Server: serverB, ServerPort: 80, Role: ClientConnectionRole}: 20,
	}, nil)

	expected := `
# HELP caretta_links_observed total bytes_sent value of links observed by caretta since its launch
# TYPE caretta_links_observed gauge
caretta_links_observed{client_id="` + strconv.Itoa(int(fnvHash("clientns"))) + `",client_kind="Deployment",client_name="client",client_namespace="ns",client_owner="",link_id="` + strconv.Itoa(int(fnvHash("clientnsserverns")+ClientConnectionRole)) + `",role="1",server_id="` + strconv.Itoa(int(fnvHash("serverns"))) + `",server_kind="Deployment",server_name="server",server_namespace="ns",server_port="80"} 30
`
	assert.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected), "caretta_links_observed"))
}

func TestLoopbackConnectionsAreSkipped(t *testing.T) {
	loopback := uint32(0x0100007f) // 127.0.0.1, as the kernel stores it
	connections := &stubMap{entries: map[ConnectionIdentifier]ConnectionThroughputStats{
		{Id: 1, Tuple: ConnectionTuple{SrcIp: loopback, DstIp: loopback, DstPort: 80}, Role: ClientConnectionRole}: {BytesSent: 10, IsActive: 1},
		{Id: 2, Tuple: ConnectionTuple{SrcIp: 1, DstIp: 2, DstPort: 80}, Role: ClientConnectionRole}:               {BytesSent: 20, IsActive: 1},
	}}
	tracer := LinksTracer{connections: connections, resolver: stubResolver{}}

	_, links, tcpConnections, err := tracer.TracesPollingIteration(map[NetworkLink]uint64{})
	assert.NoError(t, err)
	assert.Equal(t, uint64(20), sumValues(links), "only the non-loopback connection is counted")
	assert.Len(t, tcpConnections, 1)
	assert.Equal(t, 1.0, testutil.ToFloat64(filteredLoopbackConnections))
}

func TestIsAddressLoopback(t *testing.T) {
	assert.True(t, isAddressLoopback(0x0100007f), "127.0.0.1")
	assert.True(t, isAddressLoopback(0x0500007f), "127.0.0.5")
	assert.False(t, isAddressLoopback(0x0100000a), "10.0.0.1")
	assert.False(t, isAddressLoopback(0x7f000001), "1.0.0.127 is not loopback: the bytes are in network order")
}

func sumValues(links map[NetworkLink]uint64) uint64 {
	var total uint64
	for _, v := range links {
		total += v
	}
	return total
}
