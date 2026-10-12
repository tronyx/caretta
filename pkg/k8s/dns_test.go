package k8s

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeDNS struct {
	mu      sync.Mutex
	answers map[string][]string
	calls   atomic.Int32
	block   chan struct{} // when set, lookups wait on it or their context
}

func (f *fakeDNS) lookup(ctx context.Context, ip string) ([]string, error) {
	f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if hosts, ok := f.answers[ip]; ok {
		return hosts, nil
	}
	return nil, errors.New("no such host")
}

func (f *fakeDNS) set(ip string, hosts ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if hosts == nil {
		delete(f.answers, ip)
	} else {
		f.answers[ip] = hosts
	}
}

func newTestDNS(t *testing.T, fake *fakeDNS) (*reverseDNS, *fakeClock) {
	d, err := newReverseDNS(100)
	assert.NoError(t, err)
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	d.lookup = fake.lookup
	d.now = clock.get
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	d.start(stop)
	return d, clock
}

func settled(d *reverseDNS) bool {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	return len(d.pending) == 0 && len(d.queue) == 0
}

func TestReverseDNSResolvesInTheBackground(t *testing.T) {
	assert := assert.New(t)
	fake := &fakeDNS{answers: map[string][]string{"1.2.3.4": {"example.com."}}}
	d, _ := newTestDNS(t, fake)

	assert.Equal("1.2.3.4", d.name("1.2.3.4"), "an unseen IP is returned as-is without waiting")
	assert.Eventually(func() bool { return d.name("1.2.3.4") == "example.com." }, time.Second, time.Millisecond)
	assert.Equal(int32(1), fake.calls.Load())
}

func TestReverseDNSCachesFailures(t *testing.T) {
	assert := assert.New(t)
	fake := &fakeDNS{answers: map[string][]string{}}
	d, clock := newTestDNS(t, fake)

	d.name("5.6.7.8")
	assert.Eventually(func() bool { return settled(d) }, time.Second, time.Millisecond)
	for range 10 {
		assert.Equal("5.6.7.8", d.name("5.6.7.8"))
	}
	assert.True(settled(d))
	assert.Equal(int32(1), fake.calls.Load(), "a failed lookup isn't retried within the miss TTL")

	fake.set("5.6.7.8", "late.example.")
	clock.advance(dnsMissTTL + time.Second)
	d.name("5.6.7.8")
	assert.Eventually(func() bool { return d.name("5.6.7.8") == "late.example." }, time.Second, time.Millisecond)
}

func TestReverseDNSKeepsNameWhileRefreshing(t *testing.T) {
	assert := assert.New(t)
	fake := &fakeDNS{answers: map[string][]string{"1.2.3.4": {"old.example."}}}
	d, clock := newTestDNS(t, fake)

	d.name("1.2.3.4")
	assert.Eventually(func() bool { return d.name("1.2.3.4") == "old.example." }, time.Second, time.Millisecond)

	fake.set("1.2.3.4")
	clock.advance(dnsHitTTL + time.Second)
	assert.Equal("old.example.", d.name("1.2.3.4"), "an expired name is still used while it's refreshed")
	assert.Eventually(func() bool { return settled(d) }, time.Second, time.Millisecond)
	assert.Equal("old.example.", d.name("1.2.3.4"), "a failed refresh keeps the name")
	assert.Equal(int32(2), fake.calls.Load())
}

func TestReverseDNSNeverBlocksOnASlowServer(t *testing.T) {
	assert := assert.New(t)
	fake := &fakeDNS{answers: map[string][]string{}, block: make(chan struct{})}
	defer close(fake.block)
	d, _ := newTestDNS(t, fake)

	start := time.Now()
	for i := range 3 * dnsQueueSize {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		d.name(ip)
		d.name(ip)
	}
	assert.Less(time.Since(start), 100*time.Millisecond, "name() returns at once even with DNS hung and the queue full")
	assert.Eventually(func() bool { return fake.calls.Load() == dnsWorkers }, time.Second, time.Millisecond)
}
