# Homelab Installation Guide

The cluster needs a single manual bootstrap step — installing **Flux** (the
GitOps engine) — after which Flux reconciles everything in `apps/` from Git.
Secrets are SOPS-encrypted with an **age** key that Flux's kustomize-controller
decrypts in-cluster.

## Prerequisites

1. Install the required CLI tools using [Homebrew](https://brew.sh/):
   ```bash
   brew bundle --file=Brewfile
   ```
2. Obtain the **age private key** for this repo and save it somewhere safe, then
   point SOPS at it. The matching public recipient is in `.sops.yaml`.
   ```bash
   mkdir -p ~/.config/sops/age
   # paste the private key (starts with AGE-SECRET-KEY-1...) into this file:
   $EDITOR ~/.config/sops/age/homelab.agekey
   export SOPS_AGE_KEY_FILE=~/.config/sops/age/homelab.agekey
   ```
3. Have a Cloudflare API token with **Zone → DNS → Edit** on `tomerhanochi.com`
   (used by cert-manager for ACME DNS-01 and by external-dns), and your
   ProtonVPN **WireGuard** private key + interface address (for qBittorrent).

## Install the OS

Follow [the bootc install guide](https://bootc-dev.github.io/bootc/bootc-install.html)
and use `ghcr.io/tomerhanochi/homelab:latest` as the source image.

## Post Installation

### 1. Configure kubeconfig access

```bash
HOMELAB_IP="<homelab-ip>"
SSH_KEY="<path-to-core-private-key>"

export KUBECONFIG=$(mktemp)
ssh -t -i "${SSH_KEY}" "core@${HOMELAB_IP}"  run0 cat /var/lib/rancher/k3s/kubeconfig | perl -pe 's/\x1b\][^\x1b]*\x1b\\|\x1b\[[!?0-9;]*[a-zA-Z@]//g' | \
  sed "s/127.0.0.1/${HOMELAB_IP}/g" > "${KUBECONFIG}"
```

### 2. Install Flux and point it at this repo

Create the `flux-system` namespace and the SOPS decryption key, install the
GitOps Toolkit controllers, then apply the `GitRepository` + root `Kustomization`
that make Flux reconcile `flux/cluster` (which fans out to every app):

```bash
kubectl create namespace flux-system

# The age key Flux uses to decrypt SOPS secrets (data key MUST be named age.agekey).
kubectl -n flux-system create secret generic sops-age \
  --from-file=age.agekey="${SOPS_AGE_KEY_FILE}"

# Controllers (source/kustomize/helm/notification).
kustomize build apps/flux-operator | kubectl apply -f -

# Wait for the controllers
kubectl -n flux-system wait --for=condition=Available deploy --all --timeout=300s

# Wire up the repositories.
kustomize build apps/flux/repositories | kubectl apply -f -
# Wire up the flux self-synchornization
kubectl apply -f apps/flux/cluster/flux.yaml
```

### 3. Wait for GitOps synchronization

Flux reconciles apps in dependency order (cloudnative-pg / cert-manager →
cert-manager-config → gateway → apps). Watch progress:

```bash
flux get kustomizations --watch
```

Once cert-manager, external-dns, and the gateway are healthy, external-dns
creates the Cloudflare DNS records and cert-manager issues Let's Encrypt
certificates for every hostname.

### 4. First-run SSO setup (authentik)

SSO is fully GitOps and requires **no manual configuration**. Every OIDC client
and the passwordless (passkey) enrollment flow are created declaratively by
authentik **blueprints** (`apps/authentik/blueprints/`), applied by the authentik
worker on startup. The two apps that can only be configured through their own API
(Jellyfin, Kavita) are wired automatically by their bootstrap Jobs
(`images/jellyfin-bootstrap`, `images/kavita-bootstrap`) — you don't touch their
UIs. Seerr signs in "with Jellyfin", so it inherits SSO through Jellyfin. You only
claim the authentik admin account:

1. Read the initial `akadmin` password and sign in:
   ```bash
   sops -d apps/authentik/secret.sops.yaml | grep AUTHENTIK_BOOTSTRAP_PASSWORD
   ```
   Open `https://sso.tomerhanochi.com`, log in as `akadmin`, and (recommended)
   register a passkey under **Settings → MFA Devices**.
2. **Self-service registration is passkey-only**: the login page's *Sign up* link
   runs the `passkey-enrollment` flow (no password — WebAuthn only). New users are
   created **inactive**; activate them under **Directory → Users** and add them to
   groups to drive per-app and cluster (RBAC) authorization.

> The Jellyfin/Kavita bootstrap daemons create a local admin account in each app
> (kept alongside SSO so you can't be locked out). Read those credentials with
> `sops -d apps/media/jellyfin/bootstrap/secret.sops.yaml` and
> `sops -d apps/kavita/bootstrap/secret.sops.yaml` if you ever need them.

### 5. Cluster access via SSO (kubectl + Headlamp)

The API server trusts authentik's `kubernetes` OIDC application as its issuer
(`https://sso.tomerhanochi.com/application/o/kubernetes/`).

- **Headlamp** (`https://headlamp.tomerhanochi.com`) signs in via SSO and forwards
  your id_token to the API server — no extra setup.
- **kubectl** via [kubelogin](https://github.com/int128/kubelogin), using the
  committed `kubernetes` client secret:
  ```bash
  kubectl oidc-login setup \
    --oidc-issuer-url=https://sso.tomerhanochi.com/application/o/kubernetes/ \
    --oidc-client-id=kubernetes \
    --oidc-client-secret="$(sops -d apps/authentik/oidc-secret.sops.yaml | \
      awk '/KUBERNETES_CLIENT_SECRET/{print $2}')"
  ```

Authorization is via RBAC: bind your authentik identity/group (subjects are
prefixed `oidc:`) to a Role/ClusterRole — e.g. a `ClusterRoleBinding` for the
group `oidc:admins`. Headlamp and kubectl share this RBAC.

### 6. Cleanup

```bash
rm "${KUBECONFIG}"; unset KUBECONFIG
```

## Verification

```bash
flux get kustomizations          # all Ready=True
kubectl get pods -A              # all Running
kubectl get certificate -A       # all Ready=True (Let's Encrypt)
```
