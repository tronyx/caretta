# 📜 Changelog

The notable changes to this fork of Caretta, newest first.

Releases are tagged `vX.Y.Z`; each one publishes the images `ghcr.io/tronyx/caretta` and `docker.io/tronyx/caretta` and the Helm chart `oci://ghcr.io/tronyx/charts/caretta` with that version. Changes waiting on `develop` are listed under **Unreleased**. For changes before the fork, see [groundcover-com/caretta](https://github.com/groundcover-com/caretta/commits/main), whose last release was v0.0.17 in March 2025.

## Unreleased

### Project

- `examples/astronomy-shop-values.yaml` deploys the OpenTelemetry Demo (the "Astronomy Shop") with Helm: a production-like shop of about 15 services in 10 languages, with gRPC, an Envoy edge proxy, Kafka, PostgreSQL, a Valkey cache, feature flags and a load generator, trimmed to fit on one node next to Caretta. The README now lists both example apps.
- The Markdown files pass markdownlint, with the rules in `.markdownlint.json`. The README's broken "made by groundcover" link is fixed.
- CI lints the Markdown files on every pull request, so these problems can't come back.
- The README no longer says Docker for Mac is unsupported: Caretta runs on Docker Desktop, whose recent releases have a Linux kernel with BTF, and it's tested there on a kind cluster.

## [v0.2.0](https://github.com/tronyx/caretta/releases/tag/v0.2.0) · 2026-10-12

Bug fixes found by a new test suite, which now checks every change on real amd64 and arm64 kernels and on a kind cluster before anything is published.

**Upgrading:** if you set `serviceAccount.create: false`, also set `serviceAccount.name` to the account caretta should use. Until now the chart created its own account regardless; without a name, caretta now runs as the namespace's `default` account, which can't read the cluster.

### Fixed

- When two links ended up with the same labels, `caretta_links_observed` showed only one of them, picked at random on each poll, so its value could jump between scrapes and look like counter resets. Their byte counts are now added together.
- A `PROMETHEUS_PORT` of `0`, a negative number or one above 65535 is ignored, like other invalid settings, instead of starting the metrics server on a random port that every probe then fails to reach.
- The chart's `serviceAccount` values now take effect. `serviceAccount.annotations` are added to the service account, for example for cloud workload identity such as EKS IAM roles. `serviceAccount.create: false` uses an existing account, named by `serviceAccount.name`, instead of creating one anyway. By default the account is still called `caretta`, so existing installs don't change.

### Project

- Unit tests now cover the health checks, the metrics server, configuration parsing, the poll loop and shutdown order (the eBPF maps are never unloaded while a poll is reading them), loopback filtering, and the resolver's handling of deleted pods, nodes and services. Coverage goes from 50% to 77%. A bug in the resolver's test harness, which never applied modified workload resources, is fixed.
- CI loads the real eBPF programs into the kernels of an amd64 and an arm64 runner and checks what they record for real TCP connections: both sides of a connection with the right roles, ports, process and byte counts, closed connections marked as such, and a poll turning them into links and deleting them from the kernel's map. It also catches Go and C struct layouts that don't match. Until now, nothing tested the eBPF code before release.
- The Helm chart has tests (`chart/tests`, run with helm-unittest in CI). They check caretta's security context and capabilities, the read-only tracefs mount, the configuration it's given, the probes, RBAC, the service account, every NetworkPolicy case (including that one never lets in traffic from anywhere), pinned images with no `latest`, and Grafana's generated password and disabled anonymous access.
- An end-to-end test runs on a kind cluster in CI, with the image built in the same run. It installs the chart with the NetworkPolicies on, starts the demo app, and checks that caretta maps each of its connections, that VictoriaMetrics scrapes it, that Grafana requires a login and can query VictoriaMetrics, and that caretta shuts down without errors. Images are only published once it passes. The same test runs locally with `scripts/e2e.sh`, and the README explains how to run every kind of test.

## [v0.1.0](https://github.com/tronyx/caretta/releases/tag/v0.1.0) · 2026-10-12

The fork's first release.

### Changed

- Every container image is pinned to an explicit, current version, and none uses `latest`. Grafana goes from 9.3.1 to 13.2.3 (distroless) and VictoriaMetrics from v1.85.3 to v1.153.0, both now pulled from their upstream registries instead of groundcover's Quay mirrors, which stopped being updated in 2023. Grafana's helper images (k8s-sidecar, busybox, curl, bats and the image renderer, which defaulted to `latest`) are pinned too. The Grafana subchart moves to the grafana-community repository, where it has lived since January 2026.
- The caretta image is built from `golang:1.27.2-trixie` instead of a 2022 builder image that shipped Go 1.18 or older, and runs on `gcr.io/distroless/static-debian13` (pinned by digest) instead of Alpine 3.17, which is end-of-life. It starts with `ENTRYPOINT ["/caretta"]`, so stop signals reach caretta itself instead of a shell.
- All Go dependencies are current, among them cilium/ebpf v0.22.0 and the Kubernetes client v0.37.1. Go now sizes its thread pool to the pod's CPU limit, instead of the node's core count.
- golang.org/x/net is at v0.60.0, which fixes five HTTP/2 vulnerabilities Trivy found in v0.59.0, the worst a high-severity denial of service (CVE-2026-78669).
- Caretta no longer runs as a privileged container. It runs as root, which loading eBPF probes requires, but with every capability dropped except `BPF`, `PERFMON` and `SYS_RESOURCE`, a read-only root filesystem, no privilege escalation and the `RuntimeDefault` seccomp profile. It no longer mounts the host's `/proc` and debugfs, only tracefs, read-only, at a path set by the new `tracefsPath` value. This needs kernel 5.8 or newer; the README says what to change for older kernels.
- VictoriaMetrics runs as an unprivileged user (65534) with no privilege escalation, every capability dropped, a read-only root filesystem and the `RuntimeDefault` seccomp profile. It ran as root before.
- Caretta's ClusterRole grants only what it reads: list and watch on pods, nodes, Services, ReplicaSets, DaemonSets, StatefulSets, Deployments, Jobs and CronJobs. It could read about 40 kinds of objects across the cluster before, among them every ConfigMap (where credentials often end up), pod logs, events and ServiceAccounts, which anyone who took over a caretta pod could have read too.
- The bundled Grafana no longer has the fixed admin password `caretta`, which anyone who knew Caretta could look up: a random one is generated into the `caretta-grafana` Secret at install time and kept across upgrades, and `grafana.admin.existingSecret` can name a Secret of your own instead. Anonymous access is off, so the dashboards need a login; it can be turned back on in `grafana.ini`. An upgrade keeps the password already in the Secret, so an existing install keeps `caretta` until it's rotated; the README says how, and to upgrade with `--reset-then-reuse-values`, since `--reuse-values` would also keep the old password setting and anonymous access.
- Caretta has `/livez` and `/readyz` endpoints, and the chart sets startup, readiness and liveness probes. Readiness waits until the cluster's state is loaded and the eBPF probes are attached; liveness only checks that caretta is still polling, never the Kubernetes API, so an API server outage can't restart caretta on every node at once.
- Caretta is ready in seconds: it waited a fixed 10 seconds before attaching its probes, and now attaches them as soon as it has loaded the cluster's state.
- Caretta follows the cluster through client-go informers instead of nine hand-written watches, and caches only the fields it uses from each object, so each node holds far less of the cluster in memory. The flood of `couldn't retrieve owner` warnings at startup is gone, since every cache is loaded before the first pod is resolved. A deleted pod's IP is forgotten 2 minutes after the pod is gone, and a deleted node's or Service's at once, so a reused IP never keeps an old name.
- A link leaves the metrics once it hasn't been seen for an hour, set by the new `linkTTL` value (`0` keeps links for as long as caretta runs, as before). A link that comes back after that starts again from zero, which `rate()` and `increase()`, and so the bundled dashboard, handle as a counter reset.
- Caretta logs one JSON object per line, with a level and the details in separate fields (`{"time":…,"level":"WARN","msg":"Skipping poll","error":…}`), so log collectors such as Fluent Bit or Promtail can filter and parse it; the Kubernetes client's own messages come out the same way. The new `logFormat` value switches to plain `key=value` text. Warnings and errors are marked as such, where every line used to be the same plain text.
- The new `caretta_poll_duration_seconds` histogram shows how long each poll takes, so a poll that's slowing down, for example as a node's connections grow, shows up before it trips the liveness probe.
- The new `networkPolicy.enabled` value adds NetworkPolicies: only the bundled VictoriaMetrics can scrape caretta, only the bundled Grafana can query VictoriaMetrics, and both can only reach DNS and the Kubernetes API server. Other scrapers or dashboards can be let in with `networkPolicy.metricsFrom` and `networkPolicy.queryFrom`. Off by default, since it needs a network plugin that enforces NetworkPolicy.
- How many connections caretta can track per node is the new `maxConnections` value, 1,000,000 by default as before. The kernel reserves memory for all of them when caretta starts, about 192 MiB at the default, which is most of caretta's memory, so lowering it saves memory on every node.
- Caretta's resources match what it uses: a 256Mi memory request (it was 50Mi, below the eBPF maps alone, which made caretta an early eviction target), a 512Mi limit, and a 500m CPU limit instead of 150m, so a poll of a busy node isn't throttled. Go's memory limit is set to 256MiB by the new `goMemLimit` value, so Go frees memory before the pod reaches its limit.

### Fixed

- Every graceful stop exited with code 1, so each rolling update, drain or eviction looked like a crash: the metrics server's normal shutdown was reported as a fatal error. Caretta now exits with code 0, and a port that's already taken is reported at startup instead of later.
- Shutdown closed the eBPF maps while a poll could still be reading them, and had no time limit. Caretta now stops polling and waits for the poll to finish first, and finishes within 20 seconds, inside the pod's 30-second grace period.
- When re-opening a dropped watch failed, the resolver crashed on its next event, which could take caretta down on every node at once during an API server blip.
- A new Service that reused a deleted Service's cluster IP was never mapped, and stayed under the old Service's name until caretta restarted.
- `POLL_INTERVAL=0` made caretta crash at startup; a value of 0 or less is now ignored.
- When reading the kernel's connection map failed partway, which can happen on a busy node when the map keeps changing during the read, caretta published what it had read as if it were everything, so links it hadn't reached dropped out of the metrics for that poll. Such a poll is now skipped: nothing is published or removed from the map, the metrics keep the previous poll's numbers, and the error is logged and counted in the new `caretta_failed_map_iterations` metric. If every poll keeps failing, the liveness probe restarts caretta after 30 seconds.
- A slow or unreachable DNS server could stall caretta's polling: with `enableDnsResolving` on, the default, each new external IP was looked up in the middle of a poll, one at a time and with no time limit, so a node seeing many new external IPs at once, as right after startup, could go minutes without a poll, until the liveness probe restarted caretta and it started over. Lookups now run in the background with a 2-second limit, and a poll never waits for one: a new external IP shows under its address until its name arrives, usually by the next poll, and traffic counted under the address before then stays there until `linkTTL` drops it. Names are looked up again after an hour, so a changed DNS record is picked up (the old name stays in use until the new one arrives), and a failed lookup is retried after 10 minutes instead of on every poll.
- Grafana 13 uses about 290 MiB on its own, which left it at the edge of its 300Mi limit, so a dashboard with heavy queries or a `grafana cli` command got it killed. Its limit is now 512Mi with a 320Mi request, and VictoriaMetrics' memory request is 96Mi, closer to the 70 MiB it uses.
- Caretta's memory, and the number of series in VictoriaMetrics, grew for as long as it ran: `caretta_links_observed` and `caretta_tcp_states` kept every label set they had ever seen, so each pod that came and went and each closed connection stayed in the metrics, until a busy cluster's caretta pods ran out of memory. The metrics now show only the latest poll: a closed connection leaves `caretta_tcp_states` at the next poll, and an idle link leaves `caretta_links_observed` after `linkTTL`. The metric names and labels are unchanged.

### Removed

- The Go profiler (`/debug/pprof`) is no longer served on the metrics port, where anyone who could reach the pod could pull heap dumps or run CPU profiles.
- The `caretta_watcher_resets_count` metric, which informers have no equivalent for. `caretta_watcher_events_count` is still there, for pod, node and Service events.
- Support for `batch/v1beta1` CronJobs, which Kubernetes removed in 1.25.
- The chart's PodSecurityPolicy, its ClusterRole rule and the `rbac.pspEnabled` value. Kubernetes removed PodSecurityPolicy in 1.25, so they rendered on no supported cluster; Pod Security Admission replaces it, and the README says how to label caretta's namespace for it.

### Project

- Releases publish the image to both GHCR and Docker Hub, and the Helm chart to GHCR as an OCI artifact: `helm install caretta oci://ghcr.io/tronyx/charts/caretta --version X.Y.Z`. Every push to `develop` publishes test images tagged `develop-<short sha>`.
- The image builds both architectures natively instead of emulating arm64 under QEMU, and the GitHub Actions are current.
- eBPF code is generated with `go tool bpf2go` and libbpf 1.8.0 headers. The maps use BTF definitions, since libbpf 1.x no longer has the old `bpf_map_def`.
- The workflow that mirrored Grafana and VictoriaMetrics images to groundcover's Quay is kept as `subcharts.yaml.OLD`, no longer run.
- `examples/demo-shop.yaml` deploys a small app (a load generator, nginx frontend and API, and Postgres) that gives caretta a service map to show.
- Dependabot opens pull requests against `develop` for new Go modules (Kubernetes ones grouped), the Go builder and distroless base images, the Grafana and VictoriaMetrics subcharts and the images they run, and the GitHub Actions, so the project doesn't fall years behind again.
- Every pull request and push to `develop` runs gofmt, `go vet`, the tests (with the race detector) and `helm lint` before the image is built. The checks run in a `test` stage of the Dockerfile, with the same Go and generated eBPF code as the image.
- Trivy scans the image before it's published, both for `develop` and for releases. A critical vulnerability that has a fix stops the image from being published, and `develop`'s results are listed in the repository's Security tab. The newest release's images are rescanned weekly, so a vulnerability found after it was published shows up too.
