//go:build integration

// Loads the real eBPF programs into the running kernel, so it needs root:
// go test -c -tags integration ./pkg/caretta && sudo ./caretta.test -test.v
package caretta

import (
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	clientSends = 1000
	serverSends = 500
)

func startTracer(t *testing.T) *LinksTracer {
	if os.Geteuid() != 0 {
		t.Fatal("loading eBPF programs needs root")
	}
	tracer := LinksTracer{resolver: stubResolver{}}
	require.NoError(t, tracer.Start(4096))
	t.Cleanup(func() { assert.NoError(t, tracer.Stop()) })
	return &tracer
}

// Loopback connections are filtered out by design, so traffic goes to this machine's own non-loopback address.
func hostIP(t *testing.T) string {
	addrs, err := net.InterfaceAddrs()
	require.NoError(t, err)
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	t.Fatal("no non-loopback IPv4 address")
	return ""
}

type tcpPair struct {
	client, server         net.Conn
	clientPort, serverPort uint16
}

// connect opens a TCP connection to ip and sends clientSends bytes one way and serverSends back.
func connect(t *testing.T, ip string) tcpPair {
	listener, err := net.Listen("tcp4", net.JoinHostPort(ip, "0"))
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
		close(accepted)
	}()

	client, err := net.Dial("tcp4", listener.Addr().String())
	require.NoError(t, err)
	server, ok := <-accepted
	require.True(t, ok, "accepting the connection")
	t.Cleanup(func() { client.Close(); server.Close() })

	_, err = client.Write(make([]byte, clientSends))
	require.NoError(t, err)
	_, err = io.ReadFull(server, make([]byte, clientSends))
	require.NoError(t, err)
	_, err = server.Write(make([]byte, serverSends))
	require.NoError(t, err)
	_, err = io.ReadFull(client, make([]byte, serverSends))
	require.NoError(t, err)

	return tcpPair{
		client:     client,
		server:     server,
		clientPort: uint16(client.LocalAddr().(*net.TCPAddr).Port),
		serverPort: uint16(server.LocalAddr().(*net.TCPAddr).Port),
	}
}

// sides returns the kernel's entries for the pair's client and server sockets.
func sides(t *testing.T, tracer *LinksTracer, pair tcpPair) (client, server []ConnectionThroughputStats, clientIDs, serverIDs []ConnectionIdentifier) {
	var conn ConnectionIdentifier
	var stats ConnectionThroughputStats
	entries := tracer.connections.Iterate()
	for entries.Next(&conn, &stats) {
		switch {
		case conn.Tuple.SrcPort == pair.clientPort && conn.Tuple.DstPort == pair.serverPort:
			client, clientIDs = append(client, stats), append(clientIDs, conn)
		case conn.Tuple.SrcPort == pair.serverPort && conn.Tuple.DstPort == pair.clientPort:
			server, serverIDs = append(server, stats), append(serverIDs, conn)
		}
	}
	require.NoError(t, entries.Err(), "reading the map fails if the Go structs don't match the C ones")
	return client, server, clientIDs, serverIDs
}

func TestKernelRecordsConnections(t *testing.T) {
	tracer := startTracer(t)
	assert.Equal(t, uint32(4096), tracer.connections.(*EbpfMap).innerMap.MaxEntries(), "maxConnections sizes the map")
	ip := hostIP(t)
	pair := connect(t, ip)

	client, server, clientIDs, serverIDs := sides(t, tracer, pair)
	require.Len(t, client, 1, "one entry for the client socket")
	require.Len(t, server, 1, "one entry for the server socket")
	assert.Equal(t, uint32(ClientConnectionRole), clientIDs[0].Role)
	assert.Equal(t, uint32(ServerConnectionRole), serverIDs[0].Role)
	assert.NotZero(t, clientIDs[0].Pid, "the client's process is recorded when it connects")
	assert.Equal(t, ip, IP(clientIDs[0].Tuple.SrcIp).String())
	assert.Equal(t, ip, IP(clientIDs[0].Tuple.DstIp).String())
	assert.Equal(t, uint64(1), client[0].IsActive)
	assert.Equal(t, uint64(1), server[0].IsActive)
	assert.Equal(t, uint64(clientSends), client[0].BytesSent)

	pair.client.Close()
	pair.server.Close()
	require.Eventually(t, func() bool {
		client, server, _, _ = sides(t, tracer, pair)
		return len(client) == 1 && len(server) == 1 && client[0].IsActive == 0 && server[0].IsActive == 0
	}, 5*time.Second, 50*time.Millisecond, "both sockets are marked closed")
	assert.Equal(t, uint64(clientSends), client[0].BytesSent)
	assert.Equal(t, uint64(serverSends), server[0].BytesSent)
	// The kernel's bytes_received also counts the peer's FIN, which takes one sequence number.
	assert.InDelta(t, serverSends, client[0].BytesReceived, 1)
	assert.InDelta(t, clientSends, server[0].BytesReceived, 1)
}

func TestPollTurnsKernelConnectionsIntoLinks(t *testing.T) {
	tracer := startTracer(t)
	ip := hostIP(t)
	pair := connect(t, ip)
	workload := stubResolver{}.ResolveIP(ip)
	clientLink := NetworkLink{Client: workload, Server: workload, ServerPort: pair.serverPort, Role: ClientConnectionRole}
	serverLink := NetworkLink{Client: workload, Server: workload, ServerPort: pair.serverPort, Role: ServerConnectionRole}

	pastLinks, links, tcpConnections, err := tracer.TracesPollingIteration(map[NetworkLink]uint64{})
	require.NoError(t, err)
	assert.Equal(t, uint64(clientSends), links[clientLink], "the client's bytes, under the server's port")
	assert.Contains(t, links, serverLink)
	assert.Contains(t, tcpConnections, TcpConnection{Client: workload, Server: workload, ServerPort: pair.serverPort, Role: ClientConnectionRole, State: TcpConnectionOpenState})

	pair.client.Close()
	pair.server.Close()
	require.Eventually(t, func() bool {
		pastLinks, _, _, err = tracer.TracesPollingIteration(pastLinks)
		return err == nil && pastLinks[clientLink] == clientSends && pastLinks[serverLink] == serverSends
	}, 5*time.Second, 50*time.Millisecond, "closed connections move into past links")

	client, server, _, _ := sides(t, tracer, pair)
	assert.Empty(t, client, "and are deleted from the kernel's map")
	assert.Empty(t, server)
	_, links, _, err = tracer.TracesPollingIteration(pastLinks)
	require.NoError(t, err)
	assert.Equal(t, uint64(clientSends), links[clientLink], "their bytes are still reported")
	assert.Equal(t, uint64(serverSends), links[serverLink])
}
