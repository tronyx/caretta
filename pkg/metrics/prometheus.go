package metrics

import (
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func StartMetricsServer(endpoint string, port string) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.Handle(endpoint, promhttp.Handler())

	listener, err := net.Listen("tcp", port)
	if err != nil {
		return nil, err
	}

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Prometheus server on port %v failed: %v", port, err)
		}
	}()
	return server, nil
}
