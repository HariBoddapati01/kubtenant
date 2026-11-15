#!/usr/bin/env bash
# End-to-end demo on a local kind cluster. Also doubles as the CI e2e test —
# every step fails loudly, so a green run means: chart installs, the operator
# provisions a Tenant, self-heals drift, and prunes removed namespaces.
#
#   make demo              # build, install, demo, leave the cluster running
#   KEEP=false make demo   # ...and delete the kind cluster at the end (CI)
set -euo pipefail

CLUSTER="${CLUSTER:-kubtenant-demo}"
IMG="${IMG:-kubtenant:demo}"
KEEP="${KEEP:-true}"

for tool in docker kind helm kubectl; do
  command -v "$tool" >/dev/null || { echo "error: $tool is required" >&2; exit 1; }
done

step() { printf '\n\033[1;36m== %s\033[0m\n' "$*"; }

cleanup() {
  if [[ "$KEEP" != "true" ]]; then
    step "deleting kind cluster $CLUSTER"
    kind delete cluster --name "$CLUSTER" || true
  fi
}
trap cleanup EXIT

step "kind cluster"
kind get clusters 2>/dev/null | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 120s
kubectl config use-context "kind-$CLUSTER" >/dev/null

step "build + load image"
docker build -t "$IMG" .
kind load docker-image "$IMG" --name "$CLUSTER"

step "helm install"
helm upgrade --install kubtenant ./charts/kubtenant \
  --namespace kubtenant-system --create-namespace \
  --set image.repository="${IMG%%:*}" \
  --set image.tag="${IMG##*:}" \
  --set image.pullPolicy=Never \
  --wait --timeout 120s

step "create sample Tenant (acme: 3 namespaces, quota, limits, netpol, owner RBAC)"
kubectl apply -f config/samples/tenant.yaml
kubectl wait tenant/acme --for=condition=Ready --timeout=120s
kubectl get tenant acme
kubectl get ns -l platform.hariboddapati.dev/tenant=acme

step "self-heal: delete the quota in acme-dev and watch it come back"
kubectl delete resourcequota kubtenant-quota -n acme-dev
for _ in $(seq 1 30); do
  kubectl get resourcequota kubtenant-quota -n acme-dev >/dev/null 2>&1 && break
  # nudge: controller reconciles on owned-object events, this is belt+braces
  kubectl annotate tenant acme demo.kubtenant/nudge="$(date +%s)" --overwrite >/dev/null
  sleep 2
done
kubectl get resourcequota kubtenant-quota -n acme-dev
echo "self-heal OK"

step "garbage collection: drop acme-staging from spec and watch it get pruned"
kubectl patch tenant acme --type=json \
  -p '[{"op":"replace","path":"/spec/namespaces","value":["acme-dev","acme-prod"]}]'
for _ in $(seq 1 45); do
  if ! kubectl get ns acme-staging >/dev/null 2>&1; then break; fi
  phase=$(kubectl get ns acme-staging -o jsonpath='{.status.phase}' 2>/dev/null || true)
  [[ "$phase" == "Terminating" ]] && break
  sleep 2
done
phase=$(kubectl get ns acme-staging -o jsonpath='{.status.phase}' 2>/dev/null || echo "Deleted")
[[ "$phase" == "Terminating" || "$phase" == "Deleted" ]] || { echo "error: acme-staging not pruned (phase=$phase)" >&2; exit 1; }
echo "prune OK (acme-staging: $phase)"

step "done"
echo "Tenant 'acme' is live on kind cluster '$CLUSTER'."
[[ "$KEEP" == "true" ]] && echo "Explore it, then clean up with: kind delete cluster --name $CLUSTER"
