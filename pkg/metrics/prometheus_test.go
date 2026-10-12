package metrics

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/groundcover-com/caretta/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func get(t *testing.T, url string) (int, string) {
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func TestServesMetricsAndProbesOnly(t *testing.T) {
	addr := freePort(t)
	hc := health.NewChecker(time.Minute)
	server, err := StartMetricsServer("/metrics", addr, hc)
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	code, body := get(t, "http://"+addr+"/metrics")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "go_goroutines")

	code, _ = get(t, "http://"+addr+"/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, code)
	hc.MarkReady()
	code, _ = get(t, "http://"+addr+"/readyz")
	assert.Equal(t, http.StatusOK, code)
	code, _ = get(t, "http://"+addr+"/livez")
	assert.Equal(t, http.StatusOK, code)

	// The server has its own mux, so handlers registered on http.DefaultServeMux (such as pprof's) aren't exposed.
	code, _ = get(t, "http://"+addr+"/debug/pprof/")
	assert.Equal(t, http.StatusNotFound, code)
}

func TestBusyPortFailsAtStartup(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	_, err = StartMetricsServer("/metrics", l.Addr().String(), health.NewChecker(time.Minute))
	assert.Error(t, err, "a port that's taken must fail Start, not a background goroutine")
}
