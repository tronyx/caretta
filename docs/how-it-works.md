# How it works

This page describes how Caretta records connections, how it gives them names, and what it cannot see. You do not need this information to use Caretta. It helps you to read the map and to find the cause of a problem.

## The parts

Caretta has three parts, and one Helm chart installs all of them:

| Part | Runs as | What it does |
| --- | --- | --- |
| The agent | A DaemonSet, with one pod on each node | Records the TCP connections on its node and publishes them as metrics. |
| [VictoriaMetrics](https://victoriametrics.com) | A StatefulSet with one pod | Collects the metrics from each agent every 5 seconds and stores them. |
| [Grafana](https://grafana.com) | A Deployment with one pod | Shows the Caretta dashboard, with data from VictoriaMetrics. |

## Recording connections

The agent uses eBPF to record connections. eBPF lets a program run inside the Linux kernel, at points that the kernel makes available. The kernel checks each eBPF program before it runs the program, so that the program cannot damage the kernel.

The agent attaches two eBPF programs:

| Program | Attached to | What it records |
| --- | --- | --- |
| A kprobe | The kernel function `tcp_data_queue`, which runs when a TCP socket receives data | The addresses, the ports and the byte counts of the connection. |
| A tracepoint | `sock/inet_sock_set_state`, which runs when a TCP socket changes state | A new connection, and its role: the client starts a connection, and the server accepts it. Also when a connection closes. |

The programs write each connection into a table in the kernel, called a map. The table has room for `maxConnections` connections. The kernel reserves the memory for the table when the agent starts.

The programs do not read the data in the connections. They read only the addresses, the ports, the role, the state, and the counts of bytes that the kernel keeps for each socket.

## The poll

Every `pollIntervalSeconds`, the agent reads the table of connections. This is a poll. In each poll, the agent does these steps:

1. It reads each connection from the table.
2. It gives a name to each address, as described in the next section.
3. It adds together the bytes of all the connections between the same two workloads on the same port. The result is a link.
4. It removes the closed connections from the table and keeps their bytes in the link.
5. It publishes the links as metrics.

If the agent cannot read the full table, it does not publish a part of it. It skips the poll and tries again at the next poll. The metric `caretta_failed_map_iterations` counts these polls.

## Names for addresses in the cluster

The agent watches the Kubernetes API for pods, nodes and services. It keeps a list of the IP addresses of each one. For each address in a connection, it looks in this list:

| The address belongs to | The name is |
| --- | --- |
| A pod | The top owner of the pod, for example its Deployment. If `traverseUpHierarchy` is `false`, the name of the pod. |
| A node | The name of the node, in the namespace `node`. |
| A Service | The name of the Service. |
| Nothing in the cluster | See the next section. |

To find the top owner of a pod, the agent follows the owners of the pod. For example, a pod belongs to a ReplicaSet, which belongs to a Deployment. The agent knows ReplicaSets, DaemonSets, StatefulSets, Deployments, Jobs and CronJobs. For an owner of another kind, for example a custom resource, the name is the closest owner that the agent knows.

Some addresses can belong to more than one object. The agent uses these rules:

- A pod with `hostNetwork: true` uses the address of its node. The agent shows the node.
- A Service never replaces a pod or a node with the same address.
- When a pod stops, its address keeps the name of the pod for 2 minutes. Connections that close after the pod stops still get the correct name. A new pod with the same address replaces the old name at once.

## Names for addresses outside the cluster

If `enableDnsResolving` is `true`, the agent looks up a DNS name for each address that does not belong to the cluster. The namespace of the address is `external`. The agent uses these rules:

- 4 workers look up the names in the background, so that a slow DNS server does not delay a poll.
- A new address shows as its IP address until its name arrives, usually at the next poll.
- Each lookup can take 2 seconds at most.
- The agent keeps a name for 1 hour. It keeps a failed lookup for 10 minutes, and then tries again.

If `enableDnsResolving` is `false`, the name is the IP address.

## Links that stop

When a link stops, it stays in the metrics for `linkTTL` (1 hour by default). The map then still shows a connection that happened a short time ago. After `linkTTL`, the agent removes the link from its metrics. If the link starts again later, its count of bytes starts again from zero.

## When the agent stops

When Kubernetes stops the agent, the agent waits for the current poll to finish. It then removes its eBPF programs from the kernel and stops its metrics server. It has 20 seconds for these steps. Kubernetes gives the pod 30 seconds before it stops the pod by force.

## Limitations

Caretta does not see all traffic. It does not show:

- IPv6 connections. The agent records only IPv4 addresses.
- UDP traffic, for example most DNS queries. The agent records only TCP connections.
- Connections inside one pod or one node over the loopback address, for example `127.0.0.1`. The metric `caretta_current_loopback_connections` counts them.
- Traffic on nodes where the agent does not run. See [Where the agent runs](configuration.md#where-the-agent-runs).

Also, the map can show the same traffic with different names:

- On the client side, a connection to a Service shows the Service as the server. On the server side, the same connection shows the pod that the Service sent it to.
- Traffic through a proxy shows as two links: from the client to the proxy, and from the proxy to the server. A service mesh sidecar and an ingress controller are examples of proxies.
