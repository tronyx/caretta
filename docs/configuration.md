# Configuration

You set the configuration of Caretta with Helm values, in a values file or with `--set`. This page describes the values of the chart and gives examples. To see all values with their defaults, run this command:

```bash
helm show values oci://ghcr.io/tronyx/charts/caretta --version 0.2.1
```

To use a values file, add `-f values.yaml` to `helm install` or `helm upgrade`. For an upgrade, see also [Your values](upgrading.md#your-values).

## The agent

| Value | Default | Description |
| --- | --- | --- |
| `pollIntervalSeconds` | `5` | How often the agent reads the connections from the kernel and updates its metrics, in seconds. |
| `traverseUpHierarchy` | `true` | Show the top owner of each pod, for example its Deployment. If `false`, Caretta shows the pod name and puts the owner of the pod in the `client_owner` label. |
| `enableDnsResolving` | `true` | Look up DNS names for IP addresses outside the cluster. See [Names for addresses outside the cluster](how-it-works.md#names-for-addresses-outside-the-cluster). |
| `linkTTL` | `1h` | How long a link stays in the metrics after the agent last sees it, as a duration such as `30m` or `6h`. `0` keeps each link until the agent restarts. |
| `maxConnections` | `1000000` | How many connections each agent can track at one time. See [Memory](#memory). |
| `logFormat` | `json` | `json` writes one JSON object for each log line, for log collectors such as Fluent Bit or Promtail. `text` is easier to read in `kubectl logs`. |
| `prometheusPort` | `7117` | The port of the metrics and of the health probes. |
| `prometheusEndpoint` | `/metrics` | The path of the metrics. |
| `tracefsPath` | `/sys/kernel/tracing` | Where tracefs is on the nodes. See [Other tracefs paths](installation.md#other-tracefs-paths). |

A link is the traffic from one workload to another workload on one port. When a link stops, it stays in the metrics for `linkTTL`. A shorter `linkTTL` removes old links sooner. A longer `linkTTL` keeps a link that is used only from time to time, for example by a nightly job.

## Image

| Value | Default | Description |
| --- | --- | --- |
| `image.repository` | `ghcr.io/tronyx/caretta` | The image of the agent. `docker.io/tronyx/caretta` is the same image on Docker Hub. |
| `image.tag` | the version of the chart | The tag of the image. Leave it empty to use the image that the chart was released with. |
| `image.pullPolicy` | `IfNotPresent` | When Kubernetes pulls the image. |
| `imagePullSecrets` | none | Secrets for a private registry, if you copy the image to one. |

## Memory

When the agent starts, the kernel reserves memory for `maxConnections` connections. The agent stores each open connection there, and each connection that closed since the last poll. At the default of 1,000,000 connections, the kernel reserves about 192 MiB. The amount changes in proportion to `maxConnections`. For example, 250,000 connections use about 48 MiB.

This memory counts against the memory limit of the agent. If you change `maxConnections`, change the memory values to match:

1. Set `resources.limits.memory` to the kernel memory plus the memory for Caretta itself. Caretta itself uses about 30 to 60 MiB in a small cluster. It uses more in a cluster with many pods.
2. Set `goMemLimit` lower than the memory limit minus the kernel memory. The Go runtime then frees memory before the pod reaches its limit.

| Value | Default | Description |
| --- | --- | --- |
| `resources` | CPU 10m request and 500m limit, memory 256Mi request and 512Mi limit | The CPU and the memory for each agent. |
| `goMemLimit` | `256MiB` | The memory target for the Go runtime of the agent. An empty value removes it. |

This example is for a node with fewer connections:

```yaml
maxConnections: 250000
goMemLimit: 128MiB
resources:
  requests:
    memory: 128Mi
  limits:
    memory: 256Mi
```

When the table of connections is full, the agent does not track new connections until old connections close. The metric `caretta_ebpf_connections_map_size` shows how many connections the agent tracks. See [Health of Caretta](metrics.md#health-of-caretta).

## Where the agent runs

The agent runs as a DaemonSet, with one pod on each node that it can run on. To see all the traffic, the agent must run on each node.

| Value | Default | Description |
| --- | --- | --- |
| `tolerations` | the control-plane taints | Lets the agent run on control-plane nodes. Add a toleration for each taint on your other nodes. |
| `nodeSelector` | none | Runs the agent only on the nodes with these labels. |
| `affinity` | none | Kubernetes affinity rules for the agent. |
| `priorityClassName` | none | The priority class of the agent. A high priority lets the agent run on a full node. |
| `podAnnotations` | none | Annotations for the agent pods. |

This example lets the agent run on nodes that have a GPU taint, and on the control-plane nodes:

```yaml
tolerations:
  - key: node-role.kubernetes.io/control-plane
    operator: Exists
    effect: NoSchedule
  - key: nvidia.com/gpu
    operator: Exists
    effect: NoSchedule
```

A list in a values file replaces the default list. To keep the control-plane toleration, include it as in the example.

## Security

| Value | Default | Description |
| --- | --- | --- |
| `securityContext` | root, with only the `BPF`, `PERFMON` and `SYS_RESOURCE` capabilities and a read-only root filesystem | The security context of the agent container. See [Pod Security](installation.md#pod-security). |
| `podSecurityContext` | the `RuntimeDefault` seccomp profile | The security context of the agent pod. |
| `rbac.sccEnabled` | `true` | On OpenShift, let the agent use the `privileged` security context constraint. |
| `serviceAccount.create` | `true` | Make a service account for the agent. |
| `serviceAccount.name` | `caretta` | The name of the service account. If `serviceAccount.create` is `false`, set this to an existing service account. |
| `serviceAccount.annotations` | none | Annotations for the service account, for example for workload identity on a cloud provider. |

The agent can only list and watch pods, nodes, services, ReplicaSets, DaemonSets, StatefulSets, Deployments, Jobs and CronJobs. It cannot read Secrets, ConfigMaps or logs, and it cannot change anything in the cluster.

## Network policies

If you set `networkPolicy.enabled` to `true`, the chart adds NetworkPolicies with these rules:

- Only the bundled VictoriaMetrics can get the metrics from the agents.
- Only the bundled Grafana can query VictoriaMetrics.
- The agents and VictoriaMetrics can only connect to DNS and to the Kubernetes API server.
- VictoriaMetrics can also connect to the agents, to get their metrics.

Your network plugin must enforce NetworkPolicies. Most plugins do, for example Calico, Cilium and the kindnet plugin of kind.

| Value | Default | Description |
| --- | --- | --- |
| `networkPolicy.enabled` | `false` | Add the NetworkPolicies. |
| `networkPolicy.metricsFrom` | none | NetworkPolicy peers that can also get the metrics from the agents, for example your own Prometheus. |
| `networkPolicy.queryFrom` | none | NetworkPolicy peers that can also query VictoriaMetrics. |
| `networkPolicy.apiServer.cidrs` | all addresses | The addresses of your Kubernetes API server. |
| `networkPolicy.apiServer.ports` | `443` and `6443` | The ports of your Kubernetes API server. |

The rule for the API server lists two ports. Most network plugins check the address and the port of the API server itself, and not the address of the `kubernetes` Service. That port is often 6443. To find the addresses of your API server, run this command:

```bash
kubectl get endpointslices --namespace default -l kubernetes.io/service-name=kubernetes
```

This example lets a Prometheus in the `monitoring` namespace get the metrics, and allows only one API server address:

```yaml
networkPolicy:
  enabled: true
  metricsFrom:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: monitoring
  apiServer:
    cidrs:
      - 172.18.0.2/32
```

The Grafana chart has its own values for access to Grafana, under `grafana.networkPolicy`.

## VictoriaMetrics

The chart installs [VictoriaMetrics](https://victoriametrics.com) to collect and store the metrics. Its values are under `victoria-metrics-single`. The [VictoriaMetrics chart](https://github.com/VictoriaMetrics/helm-charts/tree/master/charts/victoria-metrics-single) describes all of them.

| Value | Default | Description |
| --- | --- | --- |
| `victoria-metrics-single.enabled` | `true` | Install VictoriaMetrics. Set to `false` if you use your own Prometheus. See [Use your own Prometheus](metrics.md#use-your-own-prometheus). |
| `victoria-metrics-single.server.retentionPeriod` | `1` | How long VictoriaMetrics keeps the metrics. A number without a unit is in months. `14d` is 14 days. |
| `victoria-metrics-single.server.persistentVolume.enabled` | `false` | Keep the metrics on a persistent volume. If `false`, the metrics are lost when VictoriaMetrics restarts. |
| `victoria-metrics-single.server.persistentVolume.size` | `16Gi` | The size of the persistent volume. |
| `victoria-metrics-single.server.resources` | CPU 5m request and 300m limit, memory 96Mi request and 350Mi limit | The CPU and the memory of VictoriaMetrics. |

This example keeps the metrics for 14 days on a persistent volume:

```yaml
victoria-metrics-single:
  server:
    retentionPeriod: 14d
    persistentVolume:
      enabled: true
```

## Grafana

The chart installs [Grafana](https://grafana.com) with the Caretta dashboard. Its values are under `grafana`. The [Grafana chart](https://github.com/grafana-community/helm-charts/tree/main/charts/grafana) describes all of them.

| Value | Default | Description |
| --- | --- | --- |
| `grafana.enabled` | `true` | Install Grafana. Set to `false` if you use your own Grafana. |
| `grafana.adminUser` | `admin` | The name of the admin user. |
| `grafana.admin.existingSecret` | none | A Secret with the login of the admin user, in the keys `admin-user` and `admin-password`. If empty, the chart makes a random password. |
| `grafana.grafana\.ini.auth\.anonymous.enabled` | `false` | Let anyone who can connect to Grafana see the dashboards without a login. |
| `grafana.persistence.enabled` | `false` | Keep the data of Grafana on a persistent volume, for example dashboards that you make. |

The dots in `grafana\.ini` and `auth\.anonymous` are part of the names. With `--set`, write a backslash before each of these dots. In a values file, write them as normal keys:

```yaml
grafana:
  grafana.ini:
    auth.anonymous:
      enabled: true
```

To use your own Grafana, import the dashboard in [chart/dashboard.json](../chart/dashboard.json). Its data source must be a Prometheus data source that has the metrics of Caretta.
