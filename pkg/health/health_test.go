package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func probe(handler http.HandlerFunc) int {
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code
}

func TestNotReadyUntilMarkedReady(t *testing.T) {
	c := NewChecker(time.Minute)
	assert.Equal(t, http.StatusServiceUnavailable, probe(c.Readyz))
	assert.Equal(t, http.StatusOK, probe(c.Livez), "startup must not count as a stalled poll loop")

	c.MarkReady()
	assert.Equal(t, http.StatusOK, probe(c.Readyz))
	assert.Equal(t, http.StatusOK, probe(c.Livez))
}

func TestLivezFailsOnlyWhenPollsStall(t *testing.T) {
	c := NewChecker(time.Minute)
	c.MarkReady()

	c.lastPoll.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	assert.Equal(t, http.StatusServiceUnavailable, probe(c.Livez))
	assert.Equal(t, http.StatusOK, probe(c.Readyz), "a stalled loop restarts the pod; it doesn't also pull it from service")

	c.Heartbeat()
	assert.Equal(t, http.StatusOK, probe(c.Livez))
}
