package caretta

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"time"

	"github.com/groundcover-com/caretta/pkg/health"
	caretta_k8s "github.com/groundcover-com/caretta/pkg/k8s"
	"github.com/groundcover-com/caretta/pkg/metrics"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Caretta struct {
	tracer        LinksTracer
	metricsServer *http.Server
	health        *health.Checker
	config        carettaConfig
	cancelPoll    context.CancelFunc
	pollDone      chan struct{}
}

func NewCaretta() *Caretta {
	return &Caretta{
		config: readConfig(),
	}
}

func (caretta *Caretta) Start() error {
	pollInterval := time.Duration(caretta.config.pollingIntervalSeconds) * time.Second
	caretta.health = health.NewChecker(max(3*pollInterval, 30*time.Second))

	metricsServer, err := metrics.StartMetricsServer(caretta.config.prometheusEndpoint, caretta.config.prometheusPort, caretta.health)
	if err != nil {
		return fmt.Errorf("starting Prometheus server: %w", err)
	}
	caretta.metricsServer = metricsServer

	clientset, err := caretta.getClientSet()
	if err != nil {
		return fmt.Errorf("getting kubernetes clientset: %w", err)
	}
	resolver, err := caretta_k8s.NewK8sIPResolver(clientset, caretta.config.shouldResolveDns, caretta.config.traverseUpHierarchy)
	if err != nil {
		return fmt.Errorf("creating resolver: %w", err)
	}
	// Returns only after the initial cluster snapshot has been listed.
	if err := resolver.StartWatching(); err != nil {
		return fmt.Errorf("watching cluster's state: %w", err)
	}

	caretta.tracer = NewTracer(resolver, caretta.config.linkTTL)
	if err := caretta.tracer.Start(); err != nil {
		return fmt.Errorf("loading probes: %w", err)
	}
	caretta.health.MarkReady()

	pollCtx, cancel := context.WithCancel(context.Background())
	caretta.cancelPoll = cancel
	caretta.pollDone = make(chan struct{})
	go caretta.pollLoop(pollCtx, pollInterval)
	return nil
}

func (caretta *Caretta) pollLoop(ctx context.Context, interval time.Duration) {
	defer close(caretta.pollDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	pastLinks := make(map[NetworkLink]uint64)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var links map[NetworkLink]uint64
			var tcpConnections []TcpConnection
			pastLinks, links, tcpConnections = caretta.tracer.TracesPollingIteration(pastLinks)
			collector.update(links, tcpConnections)
			caretta.health.Heartbeat()
		}
	}
}

func (caretta *Caretta) Stop(ctx context.Context) error {
	log.Print("Stopping Caretta...")
	caretta.cancelPoll()
	select {
	case <-caretta.pollDone: // never close BPF maps while an iteration is reading them
	case <-ctx.Done():
		return fmt.Errorf("poll loop did not stop: %w", ctx.Err())
	}

	var errs []error
	if err := caretta.tracer.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("unloading bpf objects: %w", err))
	}
	if err := caretta.metricsServer.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("shutting Prometheus server down: %w", err))
	}
	return errors.Join(errs...)
}

func (caretta *Caretta) getClientSet() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, nil
}

// simple fnvHash function from string to uint32
func fnvHash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}
