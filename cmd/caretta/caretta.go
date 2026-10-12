package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/groundcover-com/caretta/pkg/caretta"
	"k8s.io/klog/v2"
)

// Must stay below the pod's terminationGracePeriodSeconds (30s).
const shutdownTimeout = 20 * time.Second

func main() {
	var handler slog.Handler = slog.NewJSONHandler(os.Stderr, nil)
	if os.Getenv("LOG_FORMAT") == "text" {
		handler = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(handler))
	klog.SetSlogLogger(slog.Default())

	slog.Info("Caretta starting")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	c := caretta.NewCaretta()
	if err := c.Start(); err != nil {
		slog.Error("Startup failed", "error", err)
		os.Exit(1)
	}

	<-ctx.Done()
	stop() // a second signal now terminates immediately

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := c.Stop(shutdownCtx); err != nil {
		slog.Error("Unclean shutdown", "error", err)
		os.Exit(1)
	}
}
