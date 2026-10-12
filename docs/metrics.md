# Metrics

Each agent publishes its metrics in Prometheus format, on port 7117 at `/metrics`. The bundled VictoriaMetrics collects them every 5 seconds. You can also use them in any other tool that reads Prometheus metrics.

## Links

The main metric is `caretta_links_observed`. It has one series for each link. A link is the traffic from one workload to another workload on one port. The value is the number of bytes that one side of the link sent, in total, since the link started. Use the metric as a counter, with `rate()` or `increase()`.

| Label | Description |
| --- | --- |
| `client_name`, `server_name` | The name of the workload. For an address outside the cluster, this is its DNS name or its IP address. |
| `client_namespace`, `server_namespace` | The namespace of the workload. `node` for a node, `external` for an address outside the cluster. |
| `client_kind`, `server_kind` | The kind of the workload, for example `Deployment`, `StatefulSet`, `Service` or `pod`. `node` or `external` as for the namespace. |
| `client_owner` | The owner of the client pod. This label has a value only when `traverseUpHierarchy` is `false`. |
| `server_port` | The port on the server. |
| `role` | `1` if the client side reported the link, `2` if the server side reported it. |
| `link_id`, `client_id`, `server_id` | Numbers for the Node Graph panel of Grafana. |

The bundled VictoriaMetrics also adds the labels `caretta_pod` and `caretta_node`. They show the agent that reported the link.

### Client side and server side

When both ends of a connection are in the cluster, Caretta usually sees the connection twice. It sees it once on the node of the client, and once on the node of the server. Each side reports the bytes that it sent:

- With `role="1"`, the value is the bytes from the client to the server.
- With `role="2"`, the value is the bytes from the server to the client.

To count each connection once, filter on one role. The examples on this page use `role="1"`. For a connection to an address outside the cluster, only the side in the cluster reports it.

### Example series

This series shows that the `checkout` Deployment sent 2,537 bytes to the `cart` Service on port 8080. The `id` labels are left out:

```text
caretta_links_observed{client_kind="Deployment",client_name="checkout",client_namespace="shop",role="1",server_kind="Service",server_name="cart",server_namespace="shop",server_port="8080"} 2537
```

### Connection states

The metric `caretta_tcp_states` has the same labels as `caretta_links_observed`. It has one series for each connection, and its value is the state of the connection:

| Value | State |
| --- | --- |
| `3` | Open, on the client side. |
| `4` | Accepted, on the server side. |
| `5` | Closed. |

## Example queries

The traffic from `checkout` to `cart` for each port, in bytes per second:

```promql
sum by (server_port) (rate(caretta_links_observed{client_name="checkout", server_name="cart", role="1"}[5m]))
```

The ten busiest links in the cluster:

```promql
topk(10, sum by (client_name, server_name) (rate(caretta_links_observed{role="1"}[5m])))
```

The workloads that connect to `postgres`:

```promql
sum by (client_namespace, client_name) (rate(caretta_links_observed{server_name="postgres", role="1"}[5m]))
```

The traffic to addresses outside the cluster in the last hour, largest first:

```promql
sort_desc(sum by (client_name, server_name) (increase(caretta_links_observed{server_namespace="external", role="1"}[1h])))
```

## Health of Caretta

Each agent also publishes metrics about itself:

| Metric | Type | Description |
| --- | --- | --- |
| `caretta_poll_duration_seconds` | Histogram | The time that each poll takes. A poll reads the connections from the kernel and turns them into links. |
| `caretta_polls_made` | Counter | The number of polls. |
| `caretta_ebpf_connections_map_size` | Gauge | The number of connections that the agent tracks. Compare it with `maxConnections`. |
| `caretta_connection_deletions` | Counter | The number of closed connections that the agent removed from the kernel. |
| `caretta_current_loopback_connections` | Gauge | The number of connections inside one pod or node, which Caretta does not show. |
| `caretta_failed_map_iterations` | Counter | The number of polls that stopped because of an error when the agent read the connections from the kernel. |
| `caretta_failed_deletions` | Counter | The number of closed connections that the agent did not remove from the kernel because of an error. |
| `caretta_watcher_events_count` | Counter | The number of changes to pods, nodes and services that the agent received from the Kubernetes API, by type. |

The agent also publishes the standard metrics of the Go runtime, for example `go_memstats_heap_inuse_bytes`.

These queries show problems:

```promql
histogram_quantile(0.99, sum by (le) (rate(caretta_poll_duration_seconds_bucket[5m])))
max by (caretta_node) (caretta_ebpf_connections_map_size)
increase(caretta_failed_map_iterations[1h]) > 0
```

| Query | What it shows |
| --- | --- |
| The first | How long the slowest polls take. A poll that takes almost as long as `pollIntervalSeconds` delays the next poll. |
| The second | How full the table of connections is on each node. If it comes near `maxConnections`, increase `maxConnections`. |
| The third | The nodes where the agent did not read the connections from the kernel. See [Troubleshooting](troubleshooting.md). |

### Probes

Each agent has two health probes on port 7117:

| Path | Fails when |
| --- | --- |
| `/readyz` | The agent did not finish its start. At start, the agent reads all pods, nodes and services from the Kubernetes API and loads its eBPF programs. |
| `/livez` | The agent did not finish a poll for 30 seconds, or for three poll intervals if that is longer. Kubernetes then restarts the agent. |

The probe at `/livez` does not use the Kubernetes API. If the API server stops for a time, the agents do not restart.

## Use your own Prometheus

To use your own Prometheus instead of the bundled VictoriaMetrics:

1. Set `victoria-metrics-single.enabled` and `grafana.enabled` to `false`.
2. Add a scrape job to Prometheus. This job gets the metrics from the agent pods in the `caretta` namespace, and adds the same labels as the bundled VictoriaMetrics:

   ```yaml
   scrape_configs:
     - job_name: caretta
       scrape_interval: 5s
       kubernetes_sd_configs:
         - role: pod
           namespaces:
             names: [caretta]
       relabel_configs:
         - source_labels: [__meta_kubernetes_pod_label_app]
           regex: caretta
           action: keep
         - action: labelmap
           regex: __meta_kubernetes_pod_label_(.+)
         - source_labels: [__meta_kubernetes_pod_name]
           target_label: caretta_pod
         - source_labels: [__meta_kubernetes_pod_node_name]
           target_label: caretta_node
   ```

3. If you set `networkPolicy.enabled` to `true`, add your Prometheus to `networkPolicy.metricsFrom`. See [Network policies](configuration.md#network-policies).
4. To see the map, import [chart/dashboard.json](../chart/dashboard.json) into your Grafana.

If you use the Prometheus Operator, make a PodMonitor that selects the pods with the label `app: caretta` and the port `prom-metrics`.
