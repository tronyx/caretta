package caretta

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/groundcover-com/caretta/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubProbes struct{ unloaded atomic.Bool }

func (p *stubProbes) UnloadProbes() error {
	p.unloaded.Store(true)
	return nil
}

// blockingMap holds the first iteration open until released, like a slow read of a large map.
type blockingMap struct {
	stubMap
	blocked   atomic.Bool
	iterating chan struct{}
	release   chan struct{}
}

func newBlockingMap() *blockingMap {
	return &blockingMap{
		stubMap:   stubMap{entries: map[ConnectionIdentifier]ConnectionThroughputStats{}},
		iterating: make(chan struct{}, 1),
		release:   make(chan struct{}),
	}
}

func (m *blockingMap) Iterate() IEbpfMapIterator {
	if m.blocked.CompareAndSwap(false, true) {
		m.iterating <- struct{}{}
		<-m.release
	}
	return m.stubMap.Iterate()
}

func newTestCaretta(connections IEbpfMap, probes Probes, maxStale time.Duration) *Caretta {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Caretta{
		tracer:        LinksTracer{connections: connections, resolver: stubResolver{}, ebpfObjects: probes},
		metricsServer: &http.Server{},
		health:        health.NewChecker(maxStale),
		cancelPoll:    cancel,
		pollDone:      make(chan struct{}),
	}
	c.health.MarkReady()
	go c.pollLoop(ctx, 10*time.Millisecond)
	return c
}

func livez(c *Caretta) int {
	rec := httptest.NewRecorder()
	c.health.Livez(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	return rec.Code
}

func TestPollLoopKeepsLivenessFresh(t *testing.T) {
	conn := ConnectionIdentifier{Tuple: ConnectionTuple{SrcIp: 1, DstIp: 2, DstPort: 80}, Role: ClientConnectionRole}
	connections := &stubMap{entries: map[ConnectionIdentifier]ConnectionThroughputStats{conn: {BytesSent: 10, IsActive: 1}}}
	c := newTestCaretta(connections, &stubProbes{}, 100*time.Millisecond)
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, http.StatusOK, livez(c), "successful polls keep heartbeating")
	assert.NotNil(t, collector.latest.Load(), "polls publish their links")
}

func TestFailingPollsTripLiveness(t *testing.T) {
	connections := &stubMap{entries: map[ConnectionIdentifier]ConnectionThroughputStats{
		{Tuple: ConnectionTuple{SrcIp: 1, DstIp: 2, DstPort: 80}, Role: ClientConnectionRole}: {BytesSent: 10, IsActive: 1},
	}, failAfter: 0, iterErr: errors.New("map read failed")}
	c := newTestCaretta(connections, &stubProbes{}, 100*time.Millisecond)
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, http.StatusServiceUnavailable, livez(c), "polls that keep failing don't heartbeat, so the pod is restarted")
}

func TestStopWaitsForAPollBeforeUnloadingProbes(t *testing.T) {
	connections := newBlockingMap()
	probes := &stubProbes{}
	c := newTestCaretta(connections, probes, time.Minute)

	<-connections.iterating // a poll is now reading the map
	stopped := make(chan error)
	go func() { stopped <- c.Stop(context.Background()) }()

	select {
	case <-stopped:
		t.Fatal("Stop returned while a poll was still reading the map")
	case <-time.After(100 * time.Millisecond):
	}
	assert.False(t, probes.unloaded.Load(), "the BPF maps must stay loaded while a poll reads them")

	close(connections.release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop didn't return after the poll finished")
	}
	assert.True(t, probes.unloaded.Load())
}

func TestStopGivesUpOnAStuckPollWithoutUnloading(t *testing.T) {
	connections := newBlockingMap()
	t.Cleanup(func() { close(connections.release) })
	probes := &stubProbes{}
	c := newTestCaretta(connections, probes, time.Minute)
	<-connections.iterating

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Stop(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, probes.unloaded.Load(), "maps are never unloaded under a poll that's still reading them")
}
