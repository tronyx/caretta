<div align="center">

<img src="images/logo.svg" width="25%" alt="Caretta logo">

# Caretta

A service dependency map for Kubernetes, shown in Grafana

[![Build](https://img.shields.io/github/actions/workflow/status/tronyx/caretta/pr.yaml?branch=develop&label=build)](https://github.com/tronyx/caretta/actions/workflows/pr.yaml)
[![Release](https://img.shields.io/github/v/release/tronyx/caretta)](https://github.com/tronyx/caretta/releases/latest)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)
[![Slack: groundcover](https://img.shields.io/badge/slack-groundcover-yellowgreen.svg?logo=slack)](http://www.groundcover.com/join-slack)

</div>

> [!NOTE]
> This is a maintained fork of [groundcover-com/caretta](https://github.com/groundcover-com/caretta). [groundcover](https://www.groundcover.com) created Caretta. This fork is not affiliated with groundcover. The last upstream release was v0.0.17, in March 2025. This fork keeps the dependencies and container images up to date, uses stricter security defaults, and fixes reliability problems. The [changelog](CHANGELOG.md) lists each change.

<p align="center">
  <img src="images/caretta.gif" width="90%" alt="The Caretta service map in Grafana">
</p>

## What Caretta does

Caretta draws a map of the network connections between the workloads in your cluster. It shows which workload talks to which, on which port, and how much data they send.

Caretta has three parts, and one Helm chart installs all of them:

1. The Caretta agent runs on each node as a DaemonSet. It uses eBPF (small programs that run inside the Linux kernel) to record each TCP connection on the node. It then uses the Kubernetes API to turn each IP address into a workload name, such as a Deployment or a Service.
2. [VictoriaMetrics](https://victoriametrics.com) collects the metrics from each agent and stores them.
3. [Grafana](https://grafana.com) shows the map and other panels on a dashboard.

Caretta does not change your workloads, and you do not have to add anything to them. To learn how Caretta works, read the [Caretta blog post by groundcover](https://www.groundcover.com/blog/caretta).

## Requirements

| Requirement | Details |
| --- | --- |
| Kubernetes | Linux nodes on amd64 or arm64. Caretta is tested on Kubernetes 1.37. |
| Helm | 3.8 or later, to install charts from an OCI registry. Helm 4 also works. |
| Linux kernel | 5.8 or later, with BTF type information at `/sys/kernel/btf/vmlinux`. Most current distributions have it. For kernels 4.16 to 5.7, see [Older kernels](#older-kernels). |
| tracefs | Mounted at `/sys/kernel/tracing` on each node. For another path, set `tracefsPath`. |
| Pod Security | The namespace of Caretta must allow privileged pods. The agent runs as root with extra Linux capabilities so that it can load eBPF programs. |

Docker Desktop also works, for example with a [kind](https://kind.sigs.k8s.io) cluster on a Mac. Recent releases of Docker Desktop run a Linux kernel with BTF.

## Install

1. Create a namespace for Caretta:

   ```bash
   kubectl create namespace caretta
   ```

2. If your cluster enforces Pod Security Admission, allow privileged pods in the namespace:

   ```bash
   kubectl label namespace caretta pod-security.kubernetes.io/enforce=privileged
   ```

3. Install the chart:

   ```bash
   helm install caretta oci://ghcr.io/tronyx/charts/caretta --version 0.2.0 --namespace caretta
   ```

4. Make sure that all pods are ready. You see one `caretta` pod for each node, one `caretta-vm` pod and one `caretta-grafana` pod.

   ```bash
   kubectl get pods --namespace caretta
   ```

For a newer version, see the [releases](https://github.com/tronyx/caretta/releases). To see all chart values before you install, run `helm show values oci://ghcr.io/tronyx/charts/caretta --version 0.2.0`.

The container images are published to two registries, for amd64 and arm64:

- `ghcr.io/tronyx/caretta`
- `docker.io/tronyx/caretta`

## Open the map in Grafana

1. Forward the Grafana port to your computer:

   ```bash
   kubectl port-forward --namespace caretta svc/caretta-grafana 3000:80
   ```

2. Get the password for the `admin` user. The chart makes a random password when you install it and keeps it when you upgrade.

   ```bash
   kubectl get secret --namespace caretta caretta-grafana -o jsonpath='{.data.admin-password}' | base64 -d; echo
   ```

3. Open <http://localhost:3000> and log in as `admin`.

The Caretta dashboard opens. It has a service map, the most active connections and workloads, and the ports in use. Connections appear on the map within a minute.

To use a password from your own Secret, set `grafana.admin.existingSecret` to the name of the Secret. The Secret must have the keys `admin-user` and `admin-password`. To let anyone who can connect to Grafana see the dashboards without a login, set `grafana.grafana\.ini.auth\.anonymous.enabled` to `true`.

## Configuration

You set the configuration with Helm values, in a file or with `--set`. This table shows the values that you are most likely to change. To see all values, run `helm show values`.

| Value | Default | Description |
| --- | --- | --- |
| `pollIntervalSeconds` | `5` | How often Caretta reads the connections from the kernel and updates its metrics, in seconds. |
| `traverseUpHierarchy` | `true` | Show the top owner of each pod, for example its Deployment. If `false`, Caretta shows the pod name and puts the owner in the `client_owner` label. |
| `enableDnsResolving` | `true` | Look up DNS names for IP addresses outside the cluster. |
| `linkTTL` | `1h` | How long a link stays in the metrics after Caretta last sees it. A link is the traffic from one workload to another on one port. `0` keeps each link until Caretta restarts. |
| `maxConnections` | `1000000` | How many connections each agent can track at one time. See [Memory](#memory). |
| `resources` | 256Mi request, 512Mi limit | CPU and memory for each agent. |
| `goMemLimit` | `256MiB` | The memory target for the Go runtime of the agent. See [Memory](#memory). |
| `logFormat` | `json` | `json` writes one JSON object for each log line, for log collectors. `text` is easier to read in `kubectl logs`. |
| `tolerations` | control-plane taints | Lets the agent run on control-plane nodes. Add a toleration for each taint on your nodes so that Caretta sees all traffic. |
| `networkPolicy.enabled` | `false` | Adds NetworkPolicies. See [Network policies](#network-policies). |
| `victoria-metrics-single.enabled` | `true` | Install VictoriaMetrics. Set to `false` if you use your own Prometheus. |
| `victoria-metrics-single.server.persistentVolume.enabled` | `false` | Keep the metrics on a 16Gi persistent volume. If `false`, the metrics are lost when VictoriaMetrics restarts. |
| `grafana.enabled` | `true` | Install Grafana. Set to `false` if you use your own. |

This example values file keeps the metrics on a persistent volume and polls every 15 seconds:

```yaml
pollIntervalSeconds: 15
victoria-metrics-single:
  server:
    persistentVolume:
      enabled: true
```

Install or upgrade with the file:

```bash
helm upgrade --install caretta oci://ghcr.io/tronyx/charts/caretta --version 0.2.0 --namespace caretta -f values.yaml
```

### Memory

When the agent starts, the kernel reserves memory for `maxConnections` connections. At the default of 1,000,000, the kernel reserves about 192 MiB. The amount changes in proportion to `maxConnections`, so 250,000 uses about 48 MiB. This memory counts against the memory limit of the agent.

If you change `maxConnections`, change the memory values to match:

1. Set `resources.limits.memory` to the kernel memory plus the memory for Caretta itself.
2. Set `goMemLimit` lower than the memory limit minus the kernel memory. Go then frees memory before the pod reaches its limit.

When the table is full, the agent does not track new connections. The metric `caretta_ebpf_connections_map_size` shows how many connections the agent tracks.

### Network policies

If you set `networkPolicy.enabled` to `true`, the chart adds NetworkPolicies with these rules:

- Only the bundled VictoriaMetrics can get the metrics from the agents.
- Only the bundled Grafana can query VictoriaMetrics.
- The agents and VictoriaMetrics can only connect to DNS and the Kubernetes API server.

Your network plugin must enforce NetworkPolicies. These values change the rules:

| Value | Description |
| --- | --- |
| `networkPolicy.metricsFrom` | NetworkPolicy peers that can also get the metrics from the agents, for example your own Prometheus. |
| `networkPolicy.queryFrom` | NetworkPolicy peers that can also query VictoriaMetrics. |
| `networkPolicy.apiServer.cidrs` | The addresses of your API server. The default allows all addresses on ports 443 and 6443. |

The Grafana chart has its own values for access to Grafana, under `grafana.networkPolicy`.

### Older kernels

The default configuration uses the `BPF` and `PERFMON` capabilities, which need kernel 5.8 or later. For kernels 4.16 to 5.7, replace them with `SYS_ADMIN`:

```yaml
securityContext:
  capabilities:
    add:
      - SYS_ADMIN
      - SYS_RESOURCE
```

## Metrics

Each agent publishes its metrics in Prometheus format on port 7117, at `/metrics`. You can use them in any tool that reads Prometheus metrics.

### Links

The main metric is `caretta_links_observed`. It has one series for each link. Its value is the number of bytes that one side of the link sent, in total. Use it as a counter, with `rate()` or `increase()`.

| Label | Description |
| --- | --- |
| `client_name`, `server_name` | The name of the workload. For an address outside the cluster, this is its DNS name or its IP address. |
| `client_namespace`, `server_namespace` | The namespace of the workload. `node` for a node, `external` for an address outside the cluster. |
| `client_kind`, `server_kind` | The kind of the workload, for example `Deployment`, `StatefulSet` or `Service`. `node` or `external` as for the namespace. |
| `client_owner` | The owner of the client pod. Only set when `traverseUpHierarchy` is `false`. |
| `server_port` | The port on the server. |
| `role` | `1` if the client side reported the link, `2` if the server side reported it. With `role="1"`, the value is the bytes from the client to the server. |
| `link_id`, `client_id`, `server_id` | Numbers for the Grafana Node Graph panel. |

The bundled VictoriaMetrics also adds the labels `caretta_pod` and `caretta_node`, which show the agent that reported the link.

This series shows that the `checkout` Deployment sent 2,537 bytes to the `cart` Service on port 8080. The `id` labels are left out:

```text
caretta_links_observed{client_kind="Deployment",client_name="checkout",client_namespace="shop",role="1",server_kind="Service",server_name="cart",server_namespace="shop",server_port="8080"} 2537
```

The metric `caretta_tcp_states` has the same labels. Its value is the state of the connection. `3` is open on the client side, `4` is accepted on the server side, and `5` is closed.

### Example queries

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

### Health of Caretta

Each agent also publishes metrics about itself:

| Metric | Description |
| --- | --- |
| `caretta_poll_duration_seconds` | A histogram of the time that each poll takes. |
| `caretta_ebpf_connections_map_size` | The number of connections that the agent tracks. Compare it with `maxConnections`. |
| `caretta_failed_map_iterations` | The number of polls that stopped because of an error when the agent read the connections from the kernel. |
| `caretta_failed_deletions` | The number of closed connections that the agent did not remove from the kernel because of an error. |
| `caretta_polls_made` | The number of polls. |

These queries show problems:

```promql
histogram_quantile(0.99, sum by (le) (rate(caretta_poll_duration_seconds_bucket[5m])))
max by (caretta_node) (caretta_ebpf_connections_map_size)
increase(caretta_failed_map_iterations[1h]) > 0
```

Each agent also has a liveness probe at `/livez` and a readiness probe at `/readyz`, on port 7117. If the agent cannot poll for 30 seconds or for three poll intervals, whichever is longer, the liveness probe fails and Kubernetes restarts the agent.

### Use your own Prometheus

To use your own Prometheus instead of the bundled VictoriaMetrics:

1. Set `victoria-metrics-single.enabled` and `grafana.enabled` to `false`.
2. Configure Prometheus to scrape port 7117 on the pods that have the label `app=caretta`.
3. If you set `networkPolicy.enabled` to `true`, add your Prometheus to `networkPolicy.metricsFrom`.
4. To use the dashboard, import [chart/dashboard.json](chart/dashboard.json) into your Grafana.

## Upgrade

To upgrade to a new version, run `helm upgrade` with the new version:

```bash
helm upgrade caretta oci://ghcr.io/tronyx/charts/caretta --version 0.2.0 --namespace caretta --reset-then-reuse-values
```

Use `--reset-then-reuse-values`, or give your values again with `-f`. Do not use `--reuse-values`, because it also keeps the defaults of the old chart. The [changelog](CHANGELOG.md) lists the changes in each version and any steps for the upgrade.

### Upgrade from the groundcover chart

The groundcover releases of Caretta used the fixed Grafana password `caretta` and let anyone see the dashboards without a login. This chart makes a random password and requires a login.

> [!IMPORTANT]
> An upgrade keeps the password that is already in the `caretta-grafana` Secret. To get a new random password, follow these steps after you upgrade.

1. Delete the Secret:

   ```bash
   kubectl delete secret --namespace caretta caretta-grafana
   ```

2. Upgrade again, with the same command as before. The chart makes a new password.
3. Restart Grafana so that it uses the new password:

   ```bash
   kubectl rollout restart --namespace caretta deployment/caretta-grafana
   ```

If you enabled persistence for Grafana, Grafana keeps the password in its database. In that case, log in and change the password in your Grafana profile.

## Uninstall

```bash
helm uninstall caretta --namespace caretta
kubectl delete namespace caretta
```

If you enabled a persistent volume, its claim stays after the uninstall. The `kubectl delete namespace` command deletes it.

## Example apps

To see Caretta draw a map, run one of the example apps in the [examples](examples) folder. The top of each file tells you how to install and remove it.

| Example | Description |
| --- | --- |
| [demo-shop.yaml](examples/demo-shop.yaml) | A small app: a load generator, an nginx frontend and API, and Postgres. Install it with `kubectl apply`. |
| [astronomy-shop-values.yaml](examples/astronomy-shop-values.yaml) | Helm values for the [OpenTelemetry Demo](https://opentelemetry.io/docs/demo/), a shop with about 15 services in 10 languages. It uses gRPC, Envoy, Kafka, PostgreSQL, Valkey, feature flags and a load generator. The values make it fit on one node next to Caretta. |

In the OpenTelemetry Demo, you can use the feature flags to make a service fail, and then see the change on the map.

## Get help

- For problems with this fork, its images or its Helm chart, [open an issue](https://github.com/tronyx/caretta/issues).
- For questions about Caretta in general, ask in the [groundcover Slack](http://www.groundcover.com/join-slack).

## Contributing

Pull requests are welcome. Open them against the `develop` branch. Add a line for your change to the `Unreleased` section of the [changelog](CHANGELOG.md).

CI runs these tests on each pull request. You can also run them yourself:

| Tests | Command |
| --- | --- |
| Unit tests | `docker build --target test .` |
| eBPF tests, on Linux as root | `docker build --target integration-test --output /tmp/integration .`, then `sudo /tmp/integration/caretta-integration.test -test.v` |
| Helm chart tests | `docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD/chart:/apps" helmunittest/helm-unittest:4.3.0-1.2.1 .` |
| End-to-end tests on kind | Build the image as `caretta:e2e`, load it with `kind load docker-image caretta:e2e`, then run `IMAGE_REPOSITORY=caretta IMAGE_TAG=e2e scripts/e2e.sh`. |

The end-to-end tests install the chart and the demo app on your current kubectl context.

## Credits and license

[groundcover](https://www.groundcover.com) created Caretta and published it at [groundcover-com/caretta](https://github.com/groundcover-com/caretta). This fork is based on their work. The [changelog](CHANGELOG.md) lists the changes in this fork.

Caretta is licensed under the [Apache License 2.0](LICENSE).
