package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/groundcover-com/caretta/pkg/caretta"
)

// Must stay below the pod's terminationGracePeriodSeconds (30s).
const shutdownTimeout = 20 * time.Second

func main() {
	log.Print("Caretta starting...")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	c := caretta.NewCaretta()
	if err := c.Start(); err != nil {
		log.Fatalf("Startup failed: %v", err)
	}

	<-ctx.Done()
	stop() // a second signal now terminates immediately

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := c.Stop(shutdownCtx); err != nil {
		log.Fatalf("Unclean shutdown: %v", err)
	}
}
