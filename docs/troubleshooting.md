# Troubleshooting

This page tells you how to find the cause of a problem, and what to do. Most problems show in the status of the pods or in the log of the agent.

## Where to look

These commands show the status, the events and the log of the agents. The examples use the namespace `caretta`.

```bash
kubectl get pods --namespace caretta
kubectl describe daemonset caretta --namespace caretta
kubectl logs --namespace caretta daemonset/caretta
```

The agent writes one JSON object for each log line. The field `msg` has the message, and the field `error` has the cause of an error. For a log that is easier to read, set `logFormat` to `text`.

For an agent that restarted, add `--previous` to `kubectl logs` to see the log from before the restart.

## The agent pods do not start

### The DaemonSet has no pods

If `kubectl describe daemonset caretta` shows an event with `violates PodSecurity`, the namespace does not allow privileged pods. Label the namespace as in [Pod Security](installation.md#pod-security).

### The agent stops with "Startup failed"

The field `error` tells you which step failed:

| The error starts with | Cause | What to do |
| --- | --- | --- |
| `loading probes: error removing memory lock` | The kernel did not let the agent lift the memory limit for eBPF data. | Make sure that the agent has the `SYS_RESOURCE` capability. |
| `loading probes: error loading BPF spec` | The image is damaged or incomplete. | Install the image again from a release. |
| `loading probes: error loading BPF objects from go-side` | The kernel did not load the eBPF programs. The log also has the message `The kernel's eBPF verifier rejected caretta's programs` with the details in `verifier_log`. | Make sure that the kernel is 5.8 or later and has BTF. See [Requirements](installation.md#requirements). If it does, [open an issue](https://github.com/tronyx/caretta/issues) with the `verifier_log` and the output of `uname -r` on the node. |
| `loading probes: error attaching kprobe` | The kernel did not let the agent attach to `tcp_data_queue`. | Make sure that the agent has the `PERFMON` capability, or `SYS_ADMIN` on kernels before 5.8. |
| `loading probes: error attaching tracepoint` | The agent did not find the tracepoint `sock/inet_sock_set_state`. | Make sure that tracefs is mounted on the node at `tracefsPath`. See [Other tracefs paths](installation.md#other-tracefs-paths). |
| `watching cluster's state` | The agent did not read the pods, nodes or services from the Kubernetes API. | Make sure that the service account of the agent has its ClusterRole. If `networkPolicy.enabled` is `true`, make sure that `networkPolicy.apiServer` allows your API server. |
| `starting Prometheus server` | Another process on the node uses the metrics port. | Set `prometheusPort` to another port. |

## The agent is not ready

The readiness probe fails with `cluster snapshot loading or probes not attached` until the agent finishes its start. In a large cluster, the start can take some minutes. The startup probe waits 5 minutes at most.

If the agent is still not ready after 5 minutes, read its log. A slow or unreachable Kubernetes API is the usual cause.

## The agent restarts

1. Get the reason for the last restart:

   ```bash
   kubectl get pods --namespace caretta -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.containerStatuses[0].lastState.terminated.reason}{"\n"}{end}'
   ```

2. Find the reason in this table:

   | Reason | Cause | What to do |
   | --- | --- | --- |
   | `OOMKilled` | The agent used more memory than its limit. | Increase `resources.limits.memory`, or decrease `maxConnections`. See [Memory](configuration.md#memory). |
   | `Error`, after the event `Liveness probe failed: HTTP probe failed with statuscode: 503` | The agent did not finish a poll for too long. | Read the log from before the restart, with `--previous`. A node that is very busy, or a full table of connections, can slow the polls. |
   | `Error` | The agent stopped because of an error. | Read the log from before the restart, with `--previous`. |

## The map is empty

1. Wait one minute after the agents are ready. The first links appear after one poll and one collection by VictoriaMetrics.
2. Make sure that the time range of the dashboard includes the last few minutes.
3. Make sure that VictoriaMetrics collects the metrics. In the Explore view of Grafana, run this query:

   ```promql
   up{job="caretta"}
   ```

   Each agent must show the value `1`. If the query has no result, VictoriaMetrics does not reach the agents. If `networkPolicy.enabled` is `true`, make sure that your network plugin enforces NetworkPolicies, and that you did not remove the rule for VictoriaMetrics.

4. Make sure that the agents publish links. Run this query:

   ```promql
   count(caretta_links_observed)
   ```

   If the result is `0`, the agents see no traffic. Run a workload that sends traffic, for example one of the [example apps](../README.md#example-apps).

## The map shows IP addresses instead of names

| The address | Cause | What to do |
| --- | --- | --- |
| Is outside the cluster | The address has no DNS name, or `enableDnsResolving` is `false`. A new address also shows as an IP address until the agent gets its name, usually at the next poll. | Nothing, if the address has no DNS name. |
| Is in the cluster | The address belongs to a pod or a node that the agent does not know, for example a pod that stopped more than 2 minutes ago. | If the address stays, [open an issue](https://github.com/tronyx/caretta/issues). |

## The map shows a pod name instead of its Deployment

The agent did not find the owner of the pod. The log has the message `Couldn't find a pod's owner; using the closest one found`. The field `error` tells you why:

| The error starts with | Cause |
| --- | --- |
| `Unsupported kind for lookup` | The owner is a kind that the agent does not know, for example a custom resource. The name is the closest owner that the agent knows. |
| `missing` | The agent did not have the owner yet, usually when the owner and the pod were new. The agent tries again at the next change to the pod. |

If `traverseUpHierarchy` is `false`, the map always shows pod names. This is correct.

## Grafana

| Problem | What to do |
| --- | --- |
| You do not know the password. | Get it from the Secret, as in [Open the map in Grafana](../README.md#open-the-map-in-grafana). |
| The password is `caretta`. | You upgraded from the groundcover chart. See [The Grafana password](upgrading.md#the-grafana-password). |
| The dashboard is not there. | Make sure that the ConfigMap `caretta-grafana-dashboards` exists in the namespace of Caretta. |

## Messages in the log

The agent logs a warning or an error for each problem. This table lists each message:

| Message | Level | Meaning |
| --- | --- | --- |
| `Startup failed` | Error | The agent did not start. See [The agent stops with "Startup failed"](#the-agent-stops-with-startup-failed). |
| `The kernel's eBPF verifier rejected caretta's programs` | Error | The kernel did not load the eBPF programs. The field `verifier_log` tells you why. |
| `Skipping poll` | Warning | The agent did not read the full table of connections. It tries again at the next poll. If this message repeats, the liveness probe restarts the agent. |
| `Couldn't read a closed connection before deleting it; skipping it` | Warning | The agent did not remove one closed connection from the kernel. The metric `caretta_failed_deletions` counts these. |
| `Couldn't delete a closed connection from the map` | Warning | As above. |
| `Couldn't turn a deleted connection into a link` | Warning | The agent removed a closed connection, but did not add its bytes to a link. |
| `Couldn't find a pod's owner; using the closest one found` | Warning | See [The map shows a pod name instead of its Deployment](#the-map-shows-a-pod-name-instead-of-its-deployment). |
| `Metrics server failed` | Error | The metrics server stopped, and the agent stops. Kubernetes restarts it. |
| `Unclean shutdown` | Error | The agent did not stop in 20 seconds, or did not remove its eBPF programs. The kernel removes them when the process stops. |

If a problem is not on this page, [open an issue](https://github.com/tronyx/caretta/issues). Include the version of Caretta, the log of the agent, and the output of `uname -r` on the node.
