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

Caretta does not change your workloads, and you do not have to add anything to them. [How it works](docs/how-it-works.md) has the details. groundcover also wrote a [blog post about Caretta](https://www.groundcover.com/blog/caretta).

## Requirements

You need these to run Caretta:

- Kubernetes with Linux nodes on amd64 or arm64.
- Linux kernel 5.8 or later on each node, with BTF type information. Most current distributions have it.
- Helm 3.8 or later.
- A namespace that allows privileged pods. The agent needs root and extra Linux capabilities to load its eBPF programs.

[Installation](docs/installation.md#requirements) has the full list, and the steps for older kernels and OpenShift. Docker Desktop also works, for example with a [kind](https://kind.sigs.k8s.io) cluster on a Mac.

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
   helm install caretta oci://ghcr.io/tronyx/charts/caretta --version 0.2.1 --namespace caretta
   ```

4. Make sure that all pods are ready. You see one `caretta` pod for each node, one `caretta-vm` pod and one `caretta-grafana` pod.

   ```bash
   kubectl get pods --namespace caretta
   ```

For a newer version, see the [releases](https://github.com/tronyx/caretta/releases). [Installation](docs/installation.md) also describes the images, Pod Security and how to remove Caretta.

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

To use your own password, or to let people see the dashboards without a login, see [Grafana](docs/configuration.md#grafana).

## Configuration

You set the configuration with Helm values, in a values file or with `--set`. This table shows the values that you are most likely to change. [Configuration](docs/configuration.md) describes all of them.

| Value | Default | Description |
| --- | --- | --- |
| `pollIntervalSeconds` | `5` | How often the agent reads the connections from the kernel and updates its metrics, in seconds. |
| `traverseUpHierarchy` | `true` | Show the top owner of each pod, for example its Deployment. If `false`, Caretta shows the pod name. |
| `linkTTL` | `1h` | How long a link stays in the metrics after the agent last sees it. `0` keeps each link until the agent restarts. |
| `maxConnections` | `1000000` | How many connections each agent can track at one time. It changes the memory that the agent needs. See [Memory](docs/configuration.md#memory). |
| `networkPolicy.enabled` | `false` | Add NetworkPolicies that allow only the traffic that Caretta needs. See [Network policies](docs/configuration.md#network-policies). |
| `victoria-metrics-single.server.persistentVolume.enabled` | `false` | Keep the metrics on a persistent volume. If `false`, the metrics are lost when VictoriaMetrics restarts. |

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
helm upgrade --install caretta oci://ghcr.io/tronyx/charts/caretta --version 0.2.1 --namespace caretta -f values.yaml
```

## Metrics

Each agent publishes its metrics in Prometheus format, on port 7117 at `/metrics`. The main metric is `caretta_links_observed`. It has one series for each link, which is the traffic from one workload to another workload on one port. Its labels name the client, the server and the port.

This query shows the ten busiest links in the cluster, in bytes per second:

```promql
topk(10, sum by (client_name, server_name) (rate(caretta_links_observed{role="1"}[5m])))
```

[Metrics](docs/metrics.md) describes all the metrics and labels, has more example queries, and tells you how to use your own Prometheus.

## Upgrade

To upgrade to a new version, run this command with the new version:

```bash
helm upgrade caretta oci://ghcr.io/tronyx/charts/caretta --version 0.2.1 --namespace caretta --reset-then-reuse-values
```

Do not use `--reuse-values`. [Upgrading](docs/upgrading.md) tells you why, and how to move from the groundcover chart.

## Uninstall

```bash
helm uninstall caretta --namespace caretta
kubectl delete namespace caretta
```

## Documentation

| Page | Contents |
| --- | --- |
| [Installation](docs/installation.md) | The requirements, how to install and remove Caretta, the images, Pod Security, OpenShift and older kernels |
| [Upgrading](docs/upgrading.md) | How to upgrade to a new version, how to move from the groundcover chart, and notes for each version |
| [Configuration](docs/configuration.md) | All the values of the chart: the agent, memory, where the agent runs, security, network policies, VictoriaMetrics and Grafana |
| [Metrics](docs/metrics.md) | The metrics and their labels, example queries, the health of Caretta, the probes, and how to use your own Prometheus |
| [How it works](docs/how-it-works.md) | How Caretta records connections and gives them names, links that stop, and what Caretta cannot see |
| [Troubleshooting](docs/troubleshooting.md) | Problems and their causes, and what each message in the log means |
| [How it is tested](docs/testing.md) | The tests, how to run them, and the checks that GitHub runs on each change |

The same pages are in the [wiki](https://github.com/tronyx/caretta/wiki), for the newest release.

## Example apps

To see Caretta draw a map, run one of the example apps in the [examples](examples) folder. The top of each file tells you how to install and remove it.

| Example | Description |
| --- | --- |
| [demo-shop.yaml](examples/demo-shop.yaml) | A small app: a load generator, an nginx frontend and API, and Postgres. Install it with `kubectl apply`. |
| [astronomy-shop-values.yaml](examples/astronomy-shop-values.yaml) | Helm values for the [OpenTelemetry Demo](https://opentelemetry.io/docs/demo/), a shop with about 15 services in 10 languages. It uses gRPC, Envoy, Kafka, PostgreSQL, Valkey, feature flags and a load generator. The values make it fit on one node next to Caretta. |

In the OpenTelemetry Demo, you can use the feature flags to make a service fail, and then see the change on the map.

## Get help

- For problems with this fork, its images or its Helm chart, read [Troubleshooting](docs/troubleshooting.md), or [open an issue](https://github.com/tronyx/caretta/issues).
- For questions about Caretta in general, ask in the [groundcover Slack](http://www.groundcover.com/join-slack).

## Contributing

Pull requests are welcome. Open them against the `develop` branch. Add a line for your change to the `Unreleased` section of the [changelog](CHANGELOG.md). To change the documentation, edit the pages in [docs](docs). The wiki is a copy of them, and changes in the wiki are lost.

GitHub runs the tests on each pull request. [How it is tested](docs/testing.md) tells you how to run them yourself.

## Credits and license

[groundcover](https://www.groundcover.com) created Caretta and published it at [groundcover-com/caretta](https://github.com/groundcover-com/caretta). This fork is based on their work. The [changelog](CHANGELOG.md) lists the changes in this fork.

Caretta is licensed under the [Apache License 2.0](LICENSE).
