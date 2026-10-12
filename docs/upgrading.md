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

The groundcover chart is at [groundcover-com/caretta](https://github.com/groundcover-com/caretta). Its last release was v0.0.17. To move to this fork, remove the groundcover chart and install this one:

1. Remove the groundcover chart:

   ```bash
   helm uninstall caretta --namespace caretta
   ```

2. In your values file, remove the image repositories and tags that point at groundcover images. Also remove the values for the Pod Security Policy. Kubernetes 1.25 removed Pod Security Policies, and this chart does not have one.
3. Install this chart, as in [Install](installation.md#install).

The metrics that VictoriaMetrics stored are lost, unless you enabled its persistent volume. The new installation makes a new random Grafana password.

### Upgrade in place

This fork does not test an upgrade in place from the groundcover chart. The VictoriaMetrics chart changed a lot since then, and Kubernetes does not let Helm change some of its fields. If `helm upgrade` stops with an error that a field is immutable, use the steps in [Move from the groundcover chart](#move-from-the-groundcover-chart).

If the upgrade in place works, read the next section.

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
