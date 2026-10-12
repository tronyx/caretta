package k8s

import (
	"context"
	"net"
	"sync"
	"time"

	lrucache "github.com/hashicorp/golang-lru/v2"
)

const (
	dnsWorkers       = 4
	dnsQueueSize     = 1024
	dnsLookupTimeout = 2 * time.Second
	dnsHitTTL        = time.Hour
	dnsMissTTL       = 10 * time.Minute
)

type dnsCacheEntry struct {
	host    string
	expires time.Time
}

// reverseDNS names external IPs without ever making a poll wait on DNS: lookups run on background workers.
type reverseDNS struct {
	lookup func(ctx context.Context, ip string) ([]string, error)
	now    func() time.Time
	cache  *lrucache.Cache[string, dnsCacheEntry]
	queue  chan string

	pendingMu sync.Mutex
	pending   map[string]struct{}
}

func newReverseDNS(size int) (*reverseDNS, error) {
	cache, err := lrucache.New[string, dnsCacheEntry](size)
	if err != nil {
		return nil, err
	}
	return &reverseDNS{
		lookup:  net.DefaultResolver.LookupAddr,
		now:     time.Now,
		cache:   cache,
		queue:   make(chan string, dnsQueueSize),
		pending: make(map[string]struct{}),
	}, nil
}

func (d *reverseDNS) start(stop <-chan struct{}) {
	for range dnsWorkers {
		go func() {
			for {
				select {
				case <-stop:
					return
				case ip := <-d.queue:
					d.resolve(ip)
				}
			}
		}()
	}
}

// name returns the cached name for ip, or ip itself until a lookup finishes. An expired name is still
// returned while it's refreshed, so a link's labels don't flip back to the raw IP.
func (d *reverseDNS) name(ip string) string {
	entry, ok := d.cache.Get(ip)
	if !ok || d.now().After(entry.expires) {
		d.enqueue(ip)
	}
	if ok {
		return entry.host
	}
	return ip
}

func (d *reverseDNS) enqueue(ip string) {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	if _, ok := d.pending[ip]; ok {
		return
	}
	select {
	case d.queue <- ip:
		d.pending[ip] = struct{}{}
	default:
		// Queue full: the IP is asked for again on a later poll.
	}
}

func (d *reverseDNS) resolve(ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), dnsLookupTimeout)
	defer cancel()
	hosts, err := d.lookup(ctx, ip)

	entry := dnsCacheEntry{host: ip, expires: d.now().Add(dnsMissTTL)}
	if err == nil && len(hosts) > 0 {
		entry = dnsCacheEntry{host: hosts[0], expires: d.now().Add(dnsHitTTL)}
	} else if prev, ok := d.cache.Peek(ip); ok {
		// A failed refresh keeps the name we already have rather than reverting to the raw IP.
		entry.host = prev.host
	}
	d.cache.Add(ip, entry)

	d.pendingMu.Lock()
	delete(d.pending, ip)
	d.pendingMu.Unlock()
}
