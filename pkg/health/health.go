package health

import (
	"net/http"
	"sync/atomic"
	"time"
)

type Checker struct {
	ready    atomic.Bool
	lastPoll atomic.Int64
	maxStale time.Duration
}

func NewChecker(maxStale time.Duration) *Checker {
	return &Checker{maxStale: maxStale}
}

func (c *Checker) MarkReady() {
	c.Heartbeat()
	c.ready.Store(true)
}

func (c *Checker) Heartbeat() {
	c.lastPoll.Store(time.Now().UnixNano())
}

// Livez only reflects in-process state, never the API server, so a control-plane outage cannot restart every agent at once.
func (c *Checker) Livez(w http.ResponseWriter, _ *http.Request) {
	if c.ready.Load() {
		if age := time.Since(time.Unix(0, c.lastPoll.Load())); age > c.maxStale {
			http.Error(w, "poll loop stalled for "+age.Round(time.Second).String(), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (c *Checker) Readyz(w http.ResponseWriter, _ *http.Request) {
	if !c.ready.Load() {
		http.Error(w, "cluster snapshot loading or probes not attached", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
