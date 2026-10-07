#!/usr/bin/env bash
# Test a locally built image in disposable kind containers. Never contact a production Kubernetes context.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
if [[ $# -ne 2 ]]; then
  echo "usage: test-clean-cluster.sh LOCAL_OPERATOR_IMAGE_TAG RECEIPT_PATH" >&2
  exit 2
fi
image="$1"
receipt="$2"
work=$(mktemp -d "${TMPDIR:-/tmp}/anvil-kind.XXXXXXXX")
cluster="anvil-cpu-$(date +%s)-${RANDOM}"
job_container=""
connected_job=0
cleanup() {
  if [[ "${1}" != 0 && -s "${work}/kubeconfig" ]] && command -v kubectl >/dev/null; then
    kubectl --request-timeout=10s get pods,trainingruns,trainingartifacts,evaluationruns,modelpromotions -A -o wide || true
    kubectl --request-timeout=10s get events -n anvil-cpu-example --sort-by=.lastTimestamp | tail -n 80 || true
    kubectl --request-timeout=10s logs -n anvil-cpu-example deployment/cpu-anvil-operator --tail=80 || true
  fi
  if [[ "${connected_job}" == 1 ]]; then docker network disconnect kind "${job_container}" >/dev/null 2>&1 || true; fi
  kind delete cluster --name "${cluster}" >/dev/null 2>&1 || true
  rm -rf "${work}"
}
trap 'result=$?; cleanup "${result}"; exit "${result}"' EXIT
mkdir -p "${work}/bin"
export PATH="${work}/bin:${PATH}"
export KUBECONFIG="${work}/kubeconfig"
if ! command -v kind >/dev/null; then GOBIN="${work}/bin" go install sigs.k8s.io/kind@v0.33.0; fi
kind version | grep -F 'v0.33.0' >/dev/null
case "$(uname -m)" in
  aarch64|arm64) target_arch=arm64 ;;
  x86_64) target_arch=amd64 ;;
  *) echo "Unsupported test architecture" >&2; exit 2 ;;
esac
curl -fsSL --retry 3 -o "${work}/helm.tar.gz" "https://get.helm.sh/helm-v4.3.0-linux-${target_arch}.tar.gz"
curl -fsSL --retry 3 -o "${work}/helm.sha256" "https://get.helm.sh/helm-v4.3.0-linux-${target_arch}.tar.gz.sha256sum"
printf '%s  %s\n' "$(awk '{print $1}' "${work}/helm.sha256")" "${work}/helm.tar.gz" | sha256sum -c -
tar -xzf "${work}/helm.tar.gz" -C "${work}"
cp "${work}/linux-${target_arch}/helm" "${work}/bin/helm"
curl -fsSL --retry 3 -o "${work}/bin/kubectl" "https://dl.k8s.io/release/v1.37.0/bin/linux/${target_arch}/kubectl"
curl -fsSL --retry 3 -o "${work}/kubectl.sha256" "https://dl.k8s.io/release/v1.37.0/bin/linux/${target_arch}/kubectl.sha256"
printf '%s  %s\n' "$(cat "${work}/kubectl.sha256")" "${work}/bin/kubectl" | sha256sum -c -
chmod +x "${work}/bin/kubectl"
if [[ -n "${ANVILCTL_PATH:-}" ]]; then
  cp "${ANVILCTL_PATH}" "${work}/bin/anvilctl"
else
  go build -o "${work}/bin/anvilctl" ./cmd/anvilctl
fi
kind create cluster --name "${cluster}" --kubeconfig "${KUBECONFIG}" \
  --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5 --wait 180s
# A CI job container uses a different loopback interface from its Docker daemon.
# Join only the disposable kind network and use the control-plane certificate's internal address.
job_container=$(docker inspect --format '{{.Id}}' "$(hostname)" 2>/dev/null || true)
if [[ -n "${job_container}" ]]; then
  job_address=$(docker inspect --format '{{with index .NetworkSettings.Networks "kind"}}{{.IPAddress}}{{end}}' "${job_container}")
  if [[ -z "${job_address}" ]]; then
    docker network connect kind "${job_container}"
    connected_job=1
  fi
  api_address=$(docker inspect --format '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "${cluster}-control-plane")
  kubectl config set-cluster "kind-${cluster}" --server="https://${api_address}:6443"
fi
kind load docker-image "${image}" --name "${cluster}"
node="${cluster}-control-plane"
# Docker's local load does not require a registry publication. Resolve the imported OCI manifest digest.
digest=$(docker exec "${node}" ctr --namespace=k8s.io images ls | awk -v image="${image}" '$1 == image {print $3}')
[[ "${digest}" =~ ^sha256:[a-f0-9]{64}$ ]]
repository="${image%:*}"
reference="${repository}@${digest}"
docker exec "${node}" ctr --namespace=k8s.io images tag "${image}" "${reference}"
curl -fsSL --retry 3 -o "${work}/argo.yaml" \
  https://github.com/argoproj/argo-workflows/releases/download/v4.1.3/quick-start-minimal.yaml
printf '%s  %s\n' 6c682d0a95a0252a1e32157b0cef35a048221e0bc719de8190185df493f5a326 "${work}/argo.yaml" | sha256sum -c -
kubectl create namespace argo
kubectl apply --server-side -n argo -f "${work}/argo.yaml"
kubectl rollout status deployment/workflow-controller -n argo --timeout=180s
python3 quickstart/smoke.py --namespace anvil-cpu-example --operator-image "${reference}" \
  --kubeconfig "${KUBECONFIG}" --anvilctl "${work}/bin/anvilctl" --receipt "${receipt}"
python3 - "${receipt}" <<'PY'
import hashlib
import json
from pathlib import Path
import sys

path = Path(sys.argv[1])
receipt = json.loads(path.read_text())
receipt.update(cleanClusterTested=True, argoVersion='4.1.3',
               kindNodeImage='kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5',
               acceptanceScriptSHA256=hashlib.sha256(Path('scripts/test-clean-cluster.sh').read_bytes()).hexdigest())
path.write_text(json.dumps(receipt, sort_keys=True, indent=2) + '\n')
PY
