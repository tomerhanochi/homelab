# Homelab

## Installation

Follow [this guide](https://bootc-dev.github.io/bootc/bootc-install.html) on how to install a bootc image on a bare metal server, and use ghcr.io/tomerhanochi/homelab:latest as the source image to install.

## Post Installation

* Run the following commands in the root of this repository:
  ```bash
  HOMELAB_IP="<homelab-ip>";

  kubeconfig=$(mktemp);
  ssh -i <core-private-key-file> "core@${HOMELAB_IP}" run0 cat /var/lib/rancher/k3s/kubeconfig | sed 's/127.0.0.1/${HOMELAB_IP}/g' > "${kubeconfig}";
  # If you already have helm installed you can skip this command.
  if ! command -v helm &> /dev/null; then
    alias helm='$(which podman 2> /dev/null || which docker 2> /dev/null) run -it --rm -v "$(pwd):/apps" -w /apps -v "${HOME}/.kube:/root/.kube" -v ${HOME}/.helm:/root/.helm -v ${HOME}/.config/helm:/root/.config/helm -v ${HOME}/.cache/helm:/root/.cache/helm docker.io/alpine/helm:latest';
  fi

  helm repo add cilium https://helm.cilium.io;
  helm repo add argocd https://argoproj.github.io/argo-helm;
  helm repo update;

  # Install Cilium
  helm template --values manifests/cilium/helm/values.yaml cilium cilium/cilium | kubectl --config "${kubeconfig}" apply -Rf manifests/cilium/vanilla -f -;

  # Install ArgoCD
  helm template --values manifests/argocd/helm/values.yaml argocd argocd/argo-cd | kubectl --config "${kubeconfig}" apply -Rf manifests/argocd/vanilla -f -;

  # Install Argocd Applications
  helm template --values manifests/argocd-apps/helm/values.yaml argocd-apps argocd/argo-apps | kubectl --config "${kubeconfig}" apply -Rf manifests/argocd-apps/vanilla -f -;

  rm "${kubeconfig}"
  ```
