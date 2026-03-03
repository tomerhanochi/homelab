# Homelab Installation Guide

## Prerequisites

1. Install the required CLI tools using [Homebrew](https://brew.sh/):
   ```bash
   brew bundle --file=Brewfile
   ```
2. Verify that you have the location of the SOPS decryption SSH private key.

## Installation

Follow [this guide](https://bootc-dev.github.io/bootc/bootc-install.html) on how to install a bootc image on a bare metal server, and use `ghcr.io/tomerhanochi/homelab:latest` as the source image to install.

## Post Installation

### 1. Configure kubeconfig access

Set up SSH access to your cluster and create a temporary kubeconfig:

```bash
HOMELAB_IP="<homelab-ip>"
SSH_KEY="<path-to-core-private-key>"

# Create temporary kubeconfig
export KUBECONFIG=$(mktemp)
ssh -i "${SSH_KEY}" "core@${HOMELAB_IP}" run0 cat /var/lib/rancher/k3s/kubeconfig | \
  sed 's/127.0.0.1/${HOMELAB_IP}/g' > "${KUBECONFIG}"
```

### 2. Install Cilium

Cilium must be installed first as it provides the CNI for the cluster:

```bash
kustomize build --enable-helm apps/cilium | kubectl apply -f -
```

Wait for Cilium to be ready:

```bash
kubectl wait --for=condition=ready pod -l k8s-app=cilium -n cilium --timeout=300s
```

### 3. Install ArgoCD

Install ArgoCD which will manage the rest of your applications via GitOps:

```bash
export SOPS_AGE_SSH_PRIVATE_KEY_FILE="<sops-decryption-ssh-private-key-file>"
kustomize build --enable-helm --enable-alpha-plugins --enable-exec apps/argocd | kubectl apply --server-side -f -
unset SOPS_AGE_SSH_PRIVATE_KEY_FILE
```

Wait for ArgoCD to be ready:

```bash
kubectl wait --for=condition=ready pod -l app.kubernetes.io/part-of=argocd -n argocd --timeout=300s
```

### 4. Install ArgoCD ApplicationSet

After ArgoCD is fully running, apply the ApplicationSet that enables the App of Apps pattern:

```bash
kustomize build apps/argocd-objects | kubectl apply -f -
```

### 5. Wait for GitOps synchronization

ArgoCD will automatically discover and install all applications defined in the `apps/` directory via the ApplicationSet. Wait for all resources to synchronize:

```bash
# Watch ArgoCD applications sync status
kubectl get applications -n argocd -w
```

### 6. Cleanup

```bash
rm "${KUBECONFIG}"
unset KUBECONFIG
```

## Verification

After synchronization is complete, verify all applications are running:

```bash
kubectl get pods -A
```

All applications should show `Running` status.
