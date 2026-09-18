#!/bin/bash
# Installs the pinned development tools of the devcontainer. Runs as root at container
# creation. Bump the versions here; Go tools of the repository itself are pinned in
# Taskfile.yaml and installed by `task tools`.
set -euo pipefail

KIND_VERSION=v0.33.0
KUBECTL_VERSION=v1.37.0
HELM_VERSION=v4.3.0
KUBEBUILDER_VERSION=v4.16.0
YQ_VERSION=v4.53.6
TASK_VERSION=v3.53.1
LEFTHOOK_VERSION=v1.13.6

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

install_bin() { # name url
  echo "Installing $1..."
  curl -fsSLo "/usr/local/bin/$1" "$2"
  chmod +x "/usr/local/bin/$1"
}

install_bin kind "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-${ARCH}"
install_bin kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl"
install_bin kubebuilder "https://github.com/kubernetes-sigs/kubebuilder/releases/download/${KUBEBUILDER_VERSION}/kubebuilder_linux_${ARCH}"
install_bin yq "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${ARCH}"

echo "Installing helm..."
curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-${ARCH}.tar.gz" |
  tar -xzO "linux-${ARCH}/helm" > /usr/local/bin/helm
chmod +x /usr/local/bin/helm

echo "Installing task and lefthook..."
GOBIN=/usr/local/bin go install "github.com/go-task/task/v3/cmd/task@${TASK_VERSION}"
GOBIN=/usr/local/bin go install "github.com/evilmartians/lefthook@${LEFTHOOK_VERSION}"

echo "Installing bash completions..."
completions=/usr/share/bash-completion/completions
mkdir -p "$completions"
kind completion bash > "$completions/kind"
kubectl completion bash > "$completions/kubectl"
helm completion bash > "$completions/helm"
kubebuilder completion bash > "$completions/kubebuilder"
task --completion bash > "$completions/task"
grep -q bash_completion ~/.bashrc || echo 'source /usr/share/bash-completion/bash_completion' >> ~/.bashrc

echo "Waiting for Docker..."
for _ in $(seq 1 30); do
  docker info > /dev/null 2>&1 && break
  sleep 1
done
docker network inspect kind > /dev/null 2>&1 || docker network create kind > /dev/null

echo
kind version
kubectl version --client
helm version --short
kubebuilder version
yq --version
task --version
lefthook version
go version
