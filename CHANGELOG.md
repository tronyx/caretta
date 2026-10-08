# 📜 Changelog

The notable changes to this fork of Caretta, newest first.

Releases are tagged `vX.Y.Z`; each one publishes the images `ghcr.io/tronyx/caretta` and `docker.io/tronyx/caretta` and the Helm chart `oci://ghcr.io/tronyx/charts/caretta` with that version. Changes waiting on `develop` are listed under **Unreleased**. For changes before the fork, see [groundcover-com/caretta](https://github.com/groundcover-com/caretta/commits/main), whose last release was v0.0.17 in March 2025.

## Unreleased

### Changed

- Every container image is pinned to an explicit, current version, and none uses `latest`. Grafana goes from 9.3.1 to 13.2.3 (distroless) and VictoriaMetrics from v1.85.3 to v1.153.0, both now pulled from their upstream registries instead of groundcover's Quay mirrors, which stopped being updated in 2023. Grafana's helper images (k8s-sidecar, busybox, curl, bats and the image renderer, which defaulted to `latest`) are pinned too. The Grafana subchart moves to the grafana-community repository, where it has lived since January 2026.
- The caretta image is built from `golang:1.27.2-trixie` instead of a 2022 builder image that shipped Go 1.18 or older, and runs on `gcr.io/distroless/static-debian13` (pinned by digest) instead of Alpine 3.17, which is end-of-life. It starts with `ENTRYPOINT ["/caretta"]`, so stop signals reach caretta itself instead of a shell.
- All Go dependencies are current, among them cilium/ebpf v0.22.0 and the Kubernetes client v0.37.1. Go now sizes its thread pool to the pod's CPU limit, instead of the node's core count.
- Caretta no longer runs as a privileged container. It runs as root, which loading eBPF probes requires, but with every capability dropped except `BPF`, `PERFMON` and `SYS_RESOURCE`, a read-only root filesystem, no privilege escalation and the `RuntimeDefault` seccomp profile. It no longer mounts the host's `/proc` and debugfs, only tracefs, read-only, at a path set by the new `tracefsPath` value. This needs kernel 5.8 or newer; the README says what to change for older kernels.
- VictoriaMetrics runs as an unprivileged user (65534) with no privilege escalation, every capability dropped, a read-only root filesystem and the `RuntimeDefault` seccomp profile. It ran as root before.
- Caretta has `/livez` and `/readyz` endpoints, and the chart sets startup, readiness and liveness probes. Readiness waits until the cluster's state is loaded and the eBPF probes are attached; liveness only checks that caretta is still polling, never the Kubernetes API, so an API server outage can't restart caretta on every node at once.
- Caretta is ready in seconds: it waited a fixed 10 seconds before attaching its probes, and now attaches them as soon as it has loaded the cluster's state.
- Caretta follows the cluster through client-go informers instead of nine hand-written watches, and caches only the fields it uses from each object, so each node holds far less of the cluster in memory. The flood of `couldn't retrieve owner` warnings at startup is gone, since every cache is loaded before the first pod is resolved. A deleted pod's IP is forgotten 2 minutes after the pod is gone, and a deleted node's or Service's at once, so a reused IP never keeps an old name.
- A link leaves the metrics once it hasn't been seen for an hour, set by the new `linkTTL` value (`0` keeps links for as long as caretta runs, as before). A link that comes back after that starts again from zero, which `rate()` and `increase()`, and so the bundled dashboard, handle as a counter reset.

### Fixed

- Every graceful stop exited with code 1, so each rolling update, drain or eviction looked like a crash: the metrics server's normal shutdown was reported as a fatal error. Caretta now exits with code 0, and a port that's already taken is reported at startup instead of later.
- Shutdown closed the eBPF maps while a poll could still be reading them, and had no time limit. Caretta now stops polling and waits for the poll to finish first, and finishes within 20 seconds, inside the pod's 30-second grace period.
- When re-opening a dropped watch failed, the resolver crashed on its next event, which could take caretta down on every node at once during an API server blip.
- A new Service that reused a deleted Service's cluster IP was never mapped, and stayed under the old Service's name until caretta restarted.
- `POLL_INTERVAL=0` made caretta crash at startup; a value of 0 or less is now ignored.
- Caretta's memory, and the number of series in VictoriaMetrics, grew for as long as it ran: `caretta_links_observed` and `caretta_tcp_states` kept every label set they had ever seen, so each pod that came and went and each closed connection stayed in the metrics, until a busy cluster's caretta pods ran out of memory. The metrics now show only the latest poll: a closed connection leaves `caretta_tcp_states` at the next poll, and an idle link leaves `caretta_links_observed` after `linkTTL`. The metric names and labels are unchanged.

### Removed

- The Go profiler (`/debug/pprof`) is no longer served on the metrics port, where anyone who could reach the pod could pull heap dumps or run CPU profiles.
- The `caretta_watcher_resets_count` metric, which informers have no equivalent for. `caretta_watcher_events_count` is still there, for pod, node and Service events.
- Support for `batch/v1beta1` CronJobs, which Kubernetes removed in 1.25.

### Project

- Releases publish the image to both GHCR and Docker Hub, and the Helm chart to GHCR as an OCI artifact: `helm install caretta oci://ghcr.io/tronyx/charts/caretta --version X.Y.Z`. Every push to `develop` publishes test images tagged `develop-<short sha>`.
- The image builds both architectures natively instead of emulating arm64 under QEMU, and the GitHub Actions are current.
- eBPF code is generated with `go tool bpf2go` and libbpf 1.8.0 headers. The maps use BTF definitions, since libbpf 1.x no longer has the old `bpf_map_def`.
- The workflow that mirrored Grafana and VictoriaMetrics images to groundcover's Quay is kept as `subcharts.yaml.OLD`, no longer run.
- `examples/demo-shop.yaml` deploys a small app (a load generator, nginx frontend and API, and Postgres) that gives caretta a service map to show.
