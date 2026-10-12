# How it is tested

This page describes the tests of Caretta, and the checks that GitHub runs on each change. You can also run each test on your own computer.

## The tests

| Tests | What they check |
| --- | --- |
| Unit tests | The Go code: the poll, the links, the expiry of links, the metrics, the names for addresses, the health probes, the configuration and the order of the steps at shutdown. They run with the Go race detector. |
| eBPF tests | The real eBPF programs in a real kernel. The tests load the programs, make real TCP connections, and make sure that the kernel records each connection with the correct role, ports, process and counts of bytes. They also make sure that a poll turns the connections into links and removes the closed connections from the kernel. They run on the kernel of your computer, or in a VM with another kernel. |
| Chart tests | The Helm chart: the security context, the configuration of the agent, the probes, RBAC, the service account, each case of the NetworkPolicies, the image versions and the Grafana login. |
| End-to-end tests | The full installation on a kind cluster. See [The end-to-end tests](#the-end-to-end-tests). |

## Run the tests

You need Docker for all of the tests. The commands run from the root folder of the repository.

### Unit tests

```bash
docker build --target test .
```

The build uses the same Go version and the same generated eBPF code as the image. It also runs `gofmt` and `go vet`.

### eBPF tests

The eBPF tests load programs into the kernel, so they need Linux and root.

1. Build the tests:

   ```bash
   docker build --target integration-test --output /tmp/integration .
   ```

2. Run them as root:

   ```bash
   sudo /tmp/integration/caretta-integration.test -test.v
   ```

On a Mac, you can run them in the node of a kind cluster:

```bash
docker cp /tmp/integration/caretta-integration.test caretta-test-control-plane:/root/
docker exec caretta-test-control-plane /root/caretta-integration.test -test.v
```

Copy the file to `/root`, not to `/tmp`. The node of a kind cluster mounts an empty `/tmp`.

### eBPF tests on other kernels

[vimto](https://github.com/lmb/vimto) runs the eBPF tests in a small VM (virtual machine) with another kernel. The kernels come from [cilium/ci-kernels](https://github.com/cilium/ci-kernels). The settings of the VM are in [.vimto.toml](../.vimto.toml). You need Linux with Go and QEMU. You do not need root.

1. Install vimto:

   ```bash
   CGO_ENABLED=0 go install lmb.io/vimto@v0.4.0
   ```

2. Build the tests, as in step 1 of [eBPF tests](#ebpf-tests).

3. In the root folder of the repository, run the tests on a kernel, for example 5.10:

   ```bash
   vimto -kernel :5.10 exec -- /tmp/integration/caretta-integration.test -test.v
   ```

vimto uses KVM to make the VM fast. If your computer does not have KVM, set `VIMTO_DISABLE_KVM=true`. The tests then take longer.

### Chart tests

```bash
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD/chart:/apps" helmunittest/helm-unittest:4.3.0-1.2.1 .
```

The tests are in [chart/tests](../chart/tests). They use [helm-unittest](https://github.com/helm-unittest/helm-unittest).

### The end-to-end tests

The end-to-end tests install the chart and the demo app on your current kubectl context, and then make these checks:

- Each agent loads its eBPF programs and logs no errors.
- VictoriaMetrics collects the metrics of each agent.
- Caretta maps each connection of the demo app.
- No poll fails.
- Grafana requires a login, has the Caretta dashboard, and can query VictoriaMetrics.
- The agent stops without errors and starts again.

To run them on a kind cluster:

1. Build the image:

   ```bash
   docker build -t caretta:e2e .
   ```

2. Load the image into the cluster:

   ```bash
   kind load docker-image caretta:e2e
   ```

3. Run the tests:

   ```bash
   IMAGE_REPOSITORY=caretta IMAGE_TAG=e2e scripts/e2e.sh
   ```

Run the tests only on a test cluster. They install Caretta and the demo app on the current kubectl context. If a check fails, the script shows the status of the pods, the events and the log of the agent.

## The checks on GitHub

GitHub runs these checks on each pull request and on each push to `develop`:

| Check | What it does |
| --- | --- |
| Test and lint | The unit tests, `helm lint`, the chart tests and markdownlint for the Markdown files. |
| eBPF on the amd64 kernel and eBPF on the arm64 kernel | The eBPF tests, as root, on the kernels of an amd64 and an arm64 runner. |
| eBPF on Linux, with a kernel version | The eBPF tests in a VM, on each long-term kernel from 5.4, and on the newest stable kernel. See [eBPF tests on other kernels](#ebpf-tests-on-other-kernels). |
| End-to-end on kind | The end-to-end tests on a new kind cluster, with the image from the same run. |
| build | Builds the image, and scans it for known vulnerabilities. On `develop`, it also publishes the image with the tag `develop-` and the first 7 characters of the commit. |

The build starts only after the tests pass. If the image has a critical vulnerability that has a fix, the build fails and does not publish the image.

### Other checks

| Workflow | When | What it does |
| --- | --- | --- |
| release | For each version tag | Scans and publishes the image and the chart, and makes the GitHub release from the changelog. |
| scan | Each week | Scans the images of the newest release again, for vulnerabilities that were found after the release. |
| Sync the wiki | For each change to `docs/` on `main` | Copies the `docs/` folder to the wiki of the repository. |

Dependabot also opens pull requests for new versions of the Go modules, the images, the Helm charts and the GitHub Actions.
