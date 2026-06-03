#!/usr/bin/env bash
set -euo pipefail

CLUSTER="${KIND_CLUSTER_NAME:-kubesage-notready}"
WORKER_NODE="${KIND_WORKER_NODE:-${CLUSTER}-worker}"

usage() {
  cat <<USAGE
Usage:
  $0 create    [cluster-name]
  $0 inject    [cluster-name]
  $0 recover   [cluster-name]
  $0 status    [cluster-name]
  $0 cleanup   [cluster-name]

Creates a disposable two-node kind cluster and injects a real NodeNotReady
condition by stopping the worker node container. Do not run this against a
shared or production cluster.
USAGE
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

cluster_exists() {
  kind get clusters | grep -Fxq "$CLUSTER"
}

create_cluster() {
  if cluster_exists; then
    echo "kind cluster '$CLUSTER' already exists"
    return
  fi
  kind create cluster --name "$CLUSTER" --config - <<CONFIG
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
CONFIG
  kubectl --context "kind-${CLUSTER}" wait --for=condition=Ready node --all --timeout=120s
}

inject_notready() {
  create_cluster
  echo "stopping worker node container '$WORKER_NODE'"
  docker stop "$WORKER_NODE" >/dev/null
  echo "waiting for Kubernetes to mark the worker node Ready=False"
  kubectl --context "kind-${CLUSTER}" wait --for=condition=Ready=False "node/${WORKER_NODE}" --timeout=180s
  kubectl --context "kind-${CLUSTER}" get nodes
}

recover_node() {
  if ! cluster_exists; then
    echo "kind cluster '$CLUSTER' does not exist" >&2
    exit 1
  fi
  echo "starting worker node container '$WORKER_NODE'"
  docker start "$WORKER_NODE" >/dev/null
  kubectl --context "kind-${CLUSTER}" wait --for=condition=Ready "node/${WORKER_NODE}" --timeout=180s
  kubectl --context "kind-${CLUSTER}" get nodes
}

show_status() {
  if ! cluster_exists; then
    echo "kind cluster '$CLUSTER' does not exist" >&2
    exit 1
  fi
  kubectl --context "kind-${CLUSTER}" get nodes -o wide
}

ACTION="${1:-}"
if [[ $# -ge 2 ]]; then
  CLUSTER="$2"
  WORKER_NODE="${KIND_WORKER_NODE:-${CLUSTER}-worker}"
fi

require_cmd kind
require_cmd kubectl
require_cmd docker

case "$ACTION" in
  create)
    create_cluster
    show_status
    ;;
  inject)
    inject_notready
    ;;
  recover)
    recover_node
    ;;
  status)
    show_status
    ;;
  cleanup)
    kind delete cluster --name "$CLUSTER"
    ;;
  *)
    usage
    exit 2
    ;;
esac
