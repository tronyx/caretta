# Installation

This page tells you what Caretta needs, how to install it, and how to remove it. To install a newer version over an older one, see [Upgrading](upgrading.md).

## Requirements

| Requirement | Details |
| --- | --- |
| Kubernetes | Linux nodes on amd64 or arm64. Caretta is tested on Kubernetes 1.37. |
| Helm | 3.8 or later, to install charts from an OCI registry. Helm 4 also works. |
| Linux kernel | 5.8 or later, with BTF type information. For kernels 4.16 to 5.7, see [Older kernels](#older-kernels). |
| tracefs | Mounted at `/sys/kernel/tracing` on each node. For another path, see [Other tracefs paths](#other-tracefs-paths). |
| Pod Security | The namespace of Caretta must allow privileged pods. See [Pod Security](#pod-security). |

BTF (BPF Type Format) describes the data structures of the kernel. Caretta uses it to run the same eBPF programs on different kernel versions. Most current distributions have it. To make sure that a node has BTF, run this command on the node:

```bash
ls /sys/kernel/btf/vmlinux
```

If the file exists, the node has BTF. The [libbpf documentation](https://github.com/libbpf/libbpf#bpf-co-re-compile-once--run-everywhere) lists the distributions that have it.

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

4. Make sure that all pods are ready:

   ```bash
   kubectl get pods --namespace caretta
   ```

   You see one `caretta` pod for each node, one `caretta-vm` pod and one `caretta-grafana` pod.

For a newer version, see the [releases](https://github.com/tronyx/caretta/releases). To see all chart values before you install, run this command:

```bash
helm show values oci://ghcr.io/tronyx/charts/caretta --version 0.2.0
```

To install with your own values, write them to a file and add `-f values.yaml` to the `helm install` command. [Configuration](configuration.md) describes the values.

## Images

The chart installs the image from the GitHub Container Registry. The same image is also on Docker Hub. Both are for amd64 and arm64.

| Registry | Image |
| --- | --- |
| GitHub Container Registry | `ghcr.io/tronyx/caretta` |
| Docker Hub | `docker.io/tronyx/caretta` |

To use the Docker Hub image, set `image.repository` to `docker.io/tronyx/caretta`. Each release tag, for example `v0.2.0`, is on both registries. No image has the `latest` tag.

Before a release is published, a scanner looks for known vulnerabilities in the image. If the image has a critical vulnerability that has a fix, the release stops. Each week, the scanner also scans the newest release again.

## Pod Security

The Caretta agent needs root and extra Linux capabilities to load eBPF programs into the kernel. Kubernetes Pod Security Admission calls this a privileged pod. The agent is not a fully privileged container: it has only these capabilities, and its root filesystem is read-only.

| Capability | Why the agent needs it |
| --- | --- |
| `BPF` | To load the eBPF programs and to read their data. |
| `PERFMON` | To attach the eBPF programs to the kernel. |
| `SYS_RESOURCE` | To lift the memory limit for eBPF data on kernels before 5.11. |

If the namespace enforces the `baseline` or the `restricted` profile, the agent pods do not start. The DaemonSet then shows an error that names the capabilities. Label the namespace as in step 2 of [Install](#install).

### OpenShift

On OpenShift, the chart adds a rule that lets the service account of Caretta use the `privileged` security context constraint. The chart adds the rule only when the cluster has the OpenShift security API. To remove the rule, set `rbac.sccEnabled` to `false`.

## Older kernels

The default configuration uses the `BPF` and `PERFMON` capabilities, which need kernel 5.8 or later. For kernels 4.16 to 5.7, replace them with `SYS_ADMIN` in your values file:

```yaml
securityContext:
  capabilities:
    add:
      - SYS_ADMIN
      - SYS_RESOURCE
```

`SYS_ADMIN` gives the agent more access than the default capabilities. Use it only on these older kernels.

## Other tracefs paths

Caretta reads tracefs to find the kernel tracepoint that it uses. Most distributions mount tracefs at `/sys/kernel/tracing`. Some older distributions mount it only at `/sys/kernel/debug/tracing`. For those, set `tracefsPath`:

```yaml
tracefsPath: /sys/kernel/debug/tracing
```

The agent mounts this path from the node, and it can only read it.

## Uninstall

1. Remove the chart:

   ```bash
   helm uninstall caretta --namespace caretta
   ```

2. Delete the namespace:

   ```bash
   kubectl delete namespace caretta
   ```

If you enabled a persistent volume for VictoriaMetrics, its claim stays after step 1. Step 2 deletes it. When the agent stops, it removes its eBPF programs from the kernel.
