# Homelab Installation Guide

The cluster needs a single manual bootstrap step — installing **Flux** — after
which Flux reconciles everything in `apps/` from Git. Secrets are SOPS-encrypted
with an **age** key that Flux decrypts in-cluster.

## Prerequisites

1. Install the required CLI tools using [Homebrew](https://brew.sh/):
   ```bash
   brew bundle --file=Brewfile
   ```
2. Obtain the **age private key** for this repo and point SOPS at it (the matching
   public recipient is in `.sops.yaml`):
   ```bash
   mkdir -p ~/.config/sops/age
   # paste the private key (starts with AGE-SECRET-KEY-1...) into this file:
   $EDITOR ~/.config/sops/age/homelab.agekey
   export SOPS_AGE_KEY_FILE=~/.config/sops/age/homelab.agekey
   ```
3. Have a Cloudflare API token with **Zone → DNS → Edit** on `tomerhanochi.com`
   (used by cert-manager and external-dns), and your ProtonVPN **WireGuard**
   private key + interface address (for qBittorrent).

## Install the OS

Follow [the bootc install guide](https://bootc-dev.github.io/bootc/bootc-install.html)
using `ghcr.io/tomerhanochi/homelab:latest` as the source image.

## Post-installation

### 1. Configure kubeconfig access

```bash
HOMELAB_IP="<homelab-ip>"
SSH_KEY="<path-to-core-private-key>"

export KUBECONFIG=$(mktemp)
ssh -t -i "${SSH_KEY}" "core@${HOMELAB_IP}"  run0 cat /var/lib/rancher/k3s/kubeconfig | perl -pe 's/\x1b\][^\x1b]*\x1b\\|\x1b\[[!?0-9;]*[a-zA-Z@]//g' | \
  sed "s/127.0.0.1/${HOMELAB_IP}/g" > "${KUBECONFIG}"
```

### 2. Install Flux and point it at this repo

```bash
kubectl create namespace flux-system

# The age key Flux uses to decrypt SOPS secrets (data key MUST be named age.agekey).
kubectl -n flux-system create secret generic sops-age \
  --from-file=age.agekey="${SOPS_AGE_KEY_FILE}"

# GitOps Toolkit controllers.
kustomize build apps/flux-operator | kubectl apply -f -
kubectl -n flux-system wait --for=condition=Available deploy --all --timeout=300s

# The GitRepository, then Flux's own self-reconciliation.
kustomize build apps/flux/repositories | kubectl apply -f -
kubectl apply -f apps/flux/cluster/flux.yaml
```

### 3. Wait for GitOps synchronization

Flux reconciles apps in `dependsOn` order (operators and cert-manager → gateway →
authentik → the rest). Watch progress:

```bash
flux get kustomizations --watch
```

Once cert-manager, external-dns, and the gateway are healthy, external-dns creates
the Cloudflare DNS records and cert-manager issues certificates for every hostname.

### 4. First-run SSO setup (authentik)

The OIDC providers/applications and the passwordless (passkey) enrollment flow are
declared as authentik **blueprints** (`apps/authentik/blueprints/`) and applied by
the worker on startup. Claim the admin account:

1. Read the initial `akadmin` password and sign in:
   ```bash
   sops -d apps/authentik/secret.sops.yaml | grep AUTHENTIK_BOOTSTRAP_PASSWORD
   ```
   Open `https://sso.tomerhanochi.com`, log in as `akadmin`, and register a passkey
   under **Settings → MFA Devices**.
2. **Self-service registration is passkey-only** (the *Sign up* link runs the
   passkey enrollment flow — no password). New users are created **inactive** until
   they finish enrollment, and land in no groups. Add them to groups under
   **Directory → Users** to grant per-app and cluster (RBAC) access.

### 5. Configure applications manually

Most apps are wired to authentik entirely via GitOps. The exceptions below can only
be configured through their own UI. Each OIDC client secret is committed
(SOPS-encrypted) as a single-value file — read one with, e.g.:

```bash
sops -d apps/authentik/client-secrets/jellyfin.sops.txt
```

**qBittorrent** (`https://qbittorrent.tomerhanochi.com`) and **filebrowser**
(`https://files.tomerhanochi.com`) sit behind an oauth2-proxy that authenticates
against authentik, so they need no OIDC setup — just add users to the
`qbittorrent-users` / `filebrowser-users` groups. filebrowser auto-creates each
user on first login (promote one with `filebrowser users update <name> --perm.admin`
if you need the settings UI).

#### Jellyfin

At `https://jellyfin.tomerhanochi.com`, complete the first-run wizard (create the
local admin). Install the **SSO Authentication** plugin (`jellyfin-plugin-sso`),
then add an OIDC provider named `authentik`:

- OID endpoint: `https://sso.tomerhanochi.com/application/o/jellyfin/`
- Client ID: `jellyfin`
- Client secret: `apps/authentik/client-secrets/jellyfin.sops.txt`

Add libraries: **Movies** → `/media/movies`, **TV Shows** → `/media/tv`.

#### Kavita

At `https://kavita.tomerhanochi.com`, create the first admin, then under
**Settings → OIDC**:

- Authority: `https://sso.tomerhanochi.com/application/o/kavita/`
- Client ID: `kavita`
- Client secret: `apps/authentik/client-secrets/kavita.sops.txt`

Add libraries: **Comics** → `/library/comics`, **Books** → `/library/books`.

#### qBittorrent

The WebUI is SSO-gated at `https://qbittorrent.tomerhanochi.com`, but the official
image sets a temporary admin password on first boot. Grab it and set a permanent
one over a port-forward:

```bash
kubectl -n qbittorrent port-forward svc/qbittorrent 8080:8080
kubectl -n qbittorrent logs deploy/qbittorrent -c qbittorrent | grep -i "temporary password"
```

Open `http://localhost:8080`, sign in as `admin`, and in **Tools → Options → Web
UI → Authentication** set a permanent password. Set the default save path to
`/data/torrents` (incomplete → `/data/torrents/incomplete`).

### 6. Cluster access via SSO (kubectl + Headlamp)

The API server trusts authentik's `kubernetes` OIDC application as its issuer.

- **Headlamp** (`https://headlamp.tomerhanochi.com`) signs in via SSO and forwards
  your id_token to the API server — no extra setup.
- **kubectl** via [kubelogin](https://github.com/int128/kubelogin):
  ```bash
  kubectl oidc-login setup \
    --oidc-issuer-url=https://sso.tomerhanochi.com/application/o/kubernetes/ \
    --oidc-client-id=kubernetes \
    --oidc-client-secret="$(sops -d apps/authentik/client-secrets/kubernetes.sops.txt)"
  ```

Authorization is via RBAC: bind your authentik identity/group (subjects are
prefixed `oidc:`) to a Role/ClusterRole — e.g. a `ClusterRoleBinding` for the group
`oidc:kubernetes-cluster-admins` (see `apps/authentik/cluster-admins-rbac.yaml`).

### 7. Cleanup

```bash
rm "${KUBECONFIG}"; unset KUBECONFIG
```

## Verification

```bash
flux get kustomizations          # all Ready=True
kubectl get pods -A              # all Running
kubectl get certificate -A       # all Ready=True
```
