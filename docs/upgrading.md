# Upgrading

This page tells you how to upgrade Caretta, and how to move from the groundcover chart to this fork.

## Upgrade to a new version

1. Read the changes in the new version in the [changelog](../CHANGELOG.md). A version that needs extra steps tells you so at the top of its section.
2. Upgrade the chart:

   ```bash
   helm upgrade caretta oci://ghcr.io/tronyx/charts/caretta --version 0.2.0 --namespace caretta --reset-then-reuse-values
   ```

3. Make sure that all pods are ready:

   ```bash
   kubectl get pods --namespace caretta
   ```

Replace `0.2.0` with the new version. The agents restart one node at a time, and each agent starts again from zero. Because the metrics are counters, `rate()` and `increase()` treat this as a counter reset, and the dashboard is not affected.

### Your values

Use `--reset-then-reuse-values`, or give your values file again with `-f values.yaml`. Do not use `--reuse-values`. With `--reuse-values`, Helm also keeps the default values of the old chart. A new default, for example a new image version, then does not take effect.

## Move from the groundcover chart

The groundcover chart is at [groundcover-com/caretta](https://github.com/groundcover-com/caretta). Its last chart version is 0.0.16. You can upgrade it in place, or remove it and install this chart.

In both cases, the metrics that VictoriaMetrics stored are lost, unless you enabled its persistent volume.

### Upgrade in place

1. Save the values of your release to a file:

   ```bash
   helm get values caretta --namespace caretta -o yaml > values.yaml
   ```

2. Remove these values from the file, if it has them:
   - `image.repository` and `image.tag`, and each `image` value under `grafana` and `victoria-metrics-single`. They point at the images of groundcover.
   - `rbac.pspEnabled`, `grafana.rbac.pspEnabled` and `victoria-metrics-single.rbac.pspEnabled`. Kubernetes 1.25 removed Pod Security Policies, and this chart does not have them.

   If the file contains only `null`, you did not set values. Keep the file as it is.

3. Upgrade with the file:

   ```bash
   helm upgrade caretta oci://ghcr.io/tronyx/charts/caretta --version 0.2.0 --namespace caretta --reset-values -f values.yaml
   ```

4. Make sure that all pods are ready:

   ```bash
   kubectl get pods --namespace caretta
   ```

5. Change the Grafana password, as in [The Grafana password](#the-grafana-password).

For this upgrade, use `--reset-values` with your file, not `--reset-then-reuse-values`. With `--reset-then-reuse-values`, Helm keeps the old values that you removed from the file. For example, if you set `image.tag` for the groundcover chart, the agent then tries to get `ghcr.io/tronyx/caretta:v0.0.16`. That image does not exist, and the upgrade fails. To recover, do steps 2 and 3 again.

This upgrade is tested from chart 0.0.16 with its default values and with a pinned image tag, on Kubernetes 1.37. The names, labels and selectors of the workloads are the same in both charts, so Kubernetes accepts the upgrade.

### Remove and install again

1. Remove the groundcover chart:

   ```bash
   helm uninstall caretta --namespace caretta
   ```

2. In your values file, remove the values in step 2 of [Upgrade in place](#upgrade-in-place).
3. Install this chart, as in [Install](installation.md#install).

The new installation makes a new random Grafana password.

### The Grafana password

The groundcover chart used the fixed Grafana password `caretta`, and let anyone see the dashboards without a login. This chart makes a random password and requires a login.

> [!IMPORTANT]
> An upgrade keeps the password that is already in the `caretta-grafana` Secret. After an upgrade in place from the groundcover chart, the password stays `caretta` until you follow these steps.

1. Delete the Secret:

   ```bash
   kubectl delete secret --namespace caretta caretta-grafana
   ```

2. Upgrade again, with the same command as before. The chart makes a new random password.
3. Restart Grafana so that it uses the new password:

   ```bash
   kubectl rollout restart --namespace caretta deployment/caretta-grafana
   ```

4. Get the new password, as in [Open the map in Grafana](../README.md#open-the-map-in-grafana).

If you enabled persistence for Grafana, Grafana keeps the password in its database, and these steps do not change it. In that case, log in and change the password in your Grafana profile.

## Version notes

### 0.2.0

If you set `serviceAccount.create` to `false`, also set `serviceAccount.name` to the service account that Caretta must use. Before 0.2.0, the chart made its own service account in all cases. Without a name, the agent now runs as the `default` service account of the namespace. That account cannot read the cluster, and the agent does not start.
