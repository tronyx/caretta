package caretta

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/groundcover-com/caretta/pkg/health"
	caretta_k8s "github.com/groundcover-com/caretta/pkg/k8s"
	"github.com/groundcover-com/caretta/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	linksMetrics = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "caretta_links_observed",
		Help: "total bytes_sent value of links observed by caretta since its launch",
	}, []string{
		"link_id", "client_id", "client_name", "client_namespace", "client_kind", "client_owner", "server_id", "server_name", "server_namespace", "server_kind", "server_port", "role",
	})
	tcpStateMetrics = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "caretta_tcp_states",
		Help: "state of TCP connections observed by caretta since its launch",
	}, []string{
		"link_id", "client_id", "client_name", "client_namespace", "client_kind", "client_owner", "server_id", "server_name", "server_namespace", "server_kind", "server_port", "role",
	})
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

	caretta.tracer = NewTracer(resolver)
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
			for link, throughput := range links {
				caretta.handleLink(&link, throughput)
			}
			for _, connection := range tcpConnections {
				caretta.handleTcpConnection(&connection)
			}
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

func (caretta *Caretta) handleLink(link *NetworkLink, throughput uint64) {
	linksMetrics.With(prometheus.Labels{
		"link_id":          strconv.Itoa(int(fnvHash(link.Client.Name+link.Client.Namespace+link.Server.Name+link.Server.Namespace) + link.Role)),
		"client_id":        strconv.Itoa(int(fnvHash(link.Client.Name + link.Client.Namespace))),
		"client_name":      link.Client.Name,
		"client_namespace": link.Client.Namespace,
		"client_kind":      link.Client.Kind,
		"client_owner":     link.Client.Owner,
		"server_id":        strconv.Itoa(int(fnvHash(link.Server.Name + link.Server.Namespace))),
		"server_name":      link.Server.Name,
		"server_namespace": link.Server.Namespace,
		"server_kind":      link.Server.Kind,
		"server_port":      strconv.Itoa(int(link.ServerPort)),
		"role":             strconv.Itoa(int(link.Role)),
	}).Set(float64(throughput))
}

func (caretta *Caretta) handleTcpConnection(connection *TcpConnection) {
	tcpStateMetrics.With(prometheus.Labels{
		"link_id":          strconv.Itoa(int(fnvHash(connection.Client.Name+connection.Client.Namespace+connection.Server.Name+connection.Server.Namespace) + connection.Role)),
		"client_id":        strconv.Itoa(int(fnvHash(connection.Client.Name + connection.Client.Namespace))),
		"client_name":      connection.Client.Name,
		"client_namespace": connection.Client.Namespace,
		"client_kind":      connection.Client.Kind,
		"client_owner":     connection.Client.Owner,
		"server_id":        strconv.Itoa(int(fnvHash(connection.Server.Name + connection.Server.Namespace))),
		"server_name":      connection.Server.Name,
		"server_namespace": connection.Server.Namespace,
		"server_kind":      connection.Server.Kind,
		"server_port":      strconv.Itoa(int(connection.ServerPort)),
		"role":             strconv.Itoa(int(connection.Role)),
	}).Set(float64(connection.State))
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
