#!/usr/bin/env bash
# End-to-end test: installs the chart with the given image on the current kubectl context (a kind cluster in CI),
# runs the demo app, and checks that caretta maps it and that the bundled VictoriaMetrics and Grafana work.
#
#   IMAGE_REPOSITORY=caretta IMAGE_TAG=e2e scripts/e2e.sh
#
# The image must already be on the cluster's nodes (kind load docker-image ...).
set -euo pipefail

: "${IMAGE_REPOSITORY:?set IMAGE_REPOSITORY}"
: "${IMAGE_TAG:?set IMAGE_TAG}"
NAMESPACE=${NAMESPACE:-caretta}
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
VM_PORT=18428
GRAFANA_PORT=13000
FORWARDS=()

log() { printf '\n==> %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

cleanup() {
  for pid in "${FORWARDS[@]+"${FORWARDS[@]}"}"; do kill "$pid" 2>/dev/null || true; done
}

diagnostics() {
  log "Diagnostics"
  kubectl get pods -A -o wide || true
  kubectl -n "$NAMESPACE" describe pods || true
  kubectl -n "$NAMESPACE" logs ds/caretta --tail=100 || true
  kubectl get events -A --sort-by=.lastTimestamp | tail -40 || true
}
on_exit() {
  local rc=$?
  cleanup
  [ "$rc" -eq 0 ] || diagnostics
  exit "$rc"
}
trap on_exit EXIT

port_forward() {
  kubectl -n "$NAMESPACE" port-forward "$1" "$2" >/dev/null 2>&1 &
  FORWARDS+=("$!")
  for _ in $(seq 30); do
    curl -s -o /dev/null "http://localhost:${2%%:*}/" && return 0
    sleep 1
  done
  fail "port-forward to $1 didn't come up"
}

vm_query() {
  curl -sf --get "http://localhost:$VM_PORT/api/v1/query" --data-urlencode "query=$1" | jq -r '.data.result[0].value[1] // empty'
}

# Retries a query until it returns a value, as metrics take a few scrapes to arrive.
wait_for_metric() {
  local description=$1 query=$2 value
  for _ in $(seq 60); do
    value=$(vm_query "$query")
    if [ -n "$value" ] && [ "$value" != "0" ]; then
      printf '  ok: %s (%s)\n' "$description" "$value"
      return 0
    fi
    sleep 5
  done
  fail "$description: no data for $query"
}

log "Installing caretta ($IMAGE_REPOSITORY:$IMAGE_TAG) with NetworkPolicies on"
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace "$NAMESPACE" pod-security.kubernetes.io/enforce=privileged --overwrite
helm upgrade --install caretta "$REPO_ROOT/chart" --namespace "$NAMESPACE" \
  --set image.repository="$IMAGE_REPOSITORY" --set image.tag="$IMAGE_TAG" --set image.pullPolicy=IfNotPresent \
  --set networkPolicy.enabled=true \
  --wait --timeout 5m

log "Starting the demo app"
kubectl create namespace demo-shop --dry-run=client -o yaml | kubectl apply -f -
# Postgres keeps the password it was first started with, so an existing one is never replaced.
kubectl -n demo-shop get secret postgres-auth >/dev/null 2>&1 \
  || kubectl -n demo-shop create secret generic postgres-auth --from-literal=password="$(openssl rand -hex 24)"
kubectl apply -f "$REPO_ROOT/examples/demo-shop.yaml"
for deployment in postgres api frontend loadgen; do
  kubectl -n demo-shop rollout status "deployment/$deployment" --timeout=5m
done

log "Checking caretta's logs"
logs=$(kubectl -n "$NAMESPACE" logs ds/caretta)
echo "$logs" | jq -Re 'fromjson? | select(.msg == "Tracepoint attached")' >/dev/null || fail "probes weren't attached"
if echo "$logs" | jq -Re 'fromjson? | select(.level == "ERROR")' >/dev/null; then
  fail "caretta logged errors: $(echo "$logs" | jq -Rc 'fromjson? | select(.level == "ERROR")')"
fi
echo "  ok: JSON logs, probes attached, no errors"

log "Checking caretta's metrics in VictoriaMetrics"
port_forward svc/caretta-vm "$VM_PORT:8428"
wait_for_metric "VictoriaMetrics scrapes caretta" 'min(up{job="caretta"})'
wait_for_metric "loadgen -> frontend" 'sum(caretta_links_observed{client_name="loadgen",client_kind="Deployment",server_name="frontend",server_namespace="demo-shop"})'
wait_for_metric "frontend -> api" 'sum(caretta_links_observed{client_name="frontend",server_name="api",server_port="8080"})'
wait_for_metric "api -> postgres:5432" 'sum(caretta_links_observed{client_name="api",server_name="postgres",server_port="5432"})'
wait_for_metric "polls are timed" 'sum(caretta_poll_duration_seconds_count)'
[ "$(vm_query 'sum(caretta_failed_map_iterations)')" = "0" ] || fail "map reads failed"
echo "  ok: no failed map reads"

log "Checking Grafana"
port_forward svc/caretta-grafana "$GRAFANA_PORT:80"
password=$(kubectl -n "$NAMESPACE" get secret caretta-grafana -o jsonpath='{.data.admin-password}' | base64 -d)
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$GRAFANA_PORT/api/search")" = "401" ] || fail "Grafana allows anonymous access"
curl -sf -u "admin:$password" "http://localhost:$GRAFANA_PORT/api/search?type=dash-db" | jq -e 'any(.[]; .title == "Caretta Dashboard")' >/dev/null \
  || fail "the Caretta dashboard is missing"
datasource=$(curl -sf -u "admin:$password" "http://localhost:$GRAFANA_PORT/api/datasources/name/Prometheus" | jq -r .uid)
curl -sf -u "admin:$password" "http://localhost:$GRAFANA_PORT/api/datasources/uid/$datasource/health" | jq -e '.status == "OK"' >/dev/null \
  || fail "Grafana can't query VictoriaMetrics"
echo "  ok: login required, dashboard present, datasource healthy"

log "Checking that caretta shuts down cleanly"
pod=$(kubectl -n "$NAMESPACE" get pod -l app=caretta -o jsonpath='{.items[0].metadata.name}')
kubectl -n "$NAMESPACE" logs -f "$pod" > /tmp/caretta-e2e-shutdown.log 2>&1 &
follow=$!
kubectl -n "$NAMESPACE" delete pod "$pod" --wait=true --timeout=60s
wait "$follow" || true
grep -q '"msg":"Stopping Caretta"' /tmp/caretta-e2e-shutdown.log || fail "caretta didn't log its shutdown"
if grep -qE '"level":"ERROR"' /tmp/caretta-e2e-shutdown.log; then
  fail "shutdown logged errors: $(grep '"level":"ERROR"' /tmp/caretta-e2e-shutdown.log)"
fi
kubectl -n "$NAMESPACE" rollout status ds/caretta --timeout=3m
echo "  ok: stopped without errors and came back"

log "All end-to-end checks passed"
