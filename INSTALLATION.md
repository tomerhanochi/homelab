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

The OIDC **clients** are fully GitOps: every provider/application and the
passwordless (passkey) enrollment flow are declared as authentik **blueprints**
(`apps/authentik/blueprints/`), applied by the authentik worker on startup.
Wiring each application to its client, however, is done by hand in that app's own
UI (step 5). First, claim the authentik admin account:

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

### 5. Configure applications manually

The apps that can only be configured through their own API/UI are **not**
bootstrapped — set each one up by hand once. Every OIDC client secret is
committed (SOPS-encrypted); read one with, e.g.:

```bash
sops -d apps/authentik/oidc-secret.sops.yaml | awk '/JELLYFIN_CLIENT_SECRET/{print $2}'
```

Sonarr, Radarr, and qBittorrent have no public hostname — reach them with a
port-forward.

#### Jellyfin

At `https://jellyfin.tomerhanochi.com`, complete the first-run wizard (create the
local admin). Install the **SSO Authentication** plugin (`jellyfin-plugin-sso`)
from its plugin repo, then add an OIDC provider named `authentik`:

- Issuer / OID endpoint: `https://sso.tomerhanochi.com/application/o/jellyfin/`
- Client ID: `jellyfin`
- Client secret: the `JELLYFIN_CLIENT_SECRET` (read as above)

Add libraries: **Movies** → `/media/movies`, **TV Shows** → `/media/tv`. Seerr
signs in "with Jellyfin", so it inherits SSO once Jellyfin is done.

#### Kavita

At `https://kavita.tomerhanochi.com`, create the first admin, then under
**Settings → OIDC** set:

- Authority: `https://sso.tomerhanochi.com/application/o/kavita/`
- Client ID: `kavita`
- Client secret: the `KAVITA_CLIENT_SECRET`

Add libraries: **Comics** → `/library/comics`, **Books** → `/library/books`.

#### qBittorrent

qBittorrent stays internal (no hostname — its egress is tunnelled through the
gluetun VPN sidecar). Configure it first, over a port-forward, and set the WebUI
credentials that the Sonarr/Radarr download clients then authenticate with:

1. Forward the WebUI straight from the pod (you're talking to qBittorrent as
   `localhost`, which it always trusts):
   ```bash
   kubectl -n qbittorrent port-forward svc/qbittorrent 8080:8080
   ```
2. The official image sets a **temporary** admin password on first boot; grab it
   from the logs, then open `http://localhost:8080` and sign in as `admin`:
   ```bash
   kubectl -n qbittorrent logs deploy/qbittorrent -c qbittorrent | grep -i "temporary password"
   ```
3. In **Tools → Options → Web UI → Authentication**, set the **username** (keep
   `admin`) and a permanent **password**. These are the credentials you give the
   Sonarr/Radarr download clients below — without them the arr apps can't reach
   qBittorrent, since it authenticates every client.
4. Set the default save path to `/data/torrents` (incomplete →
   `/data/torrents/incomplete`), and add categories `tv-sonarr` →
   `/data/torrents/tv` and `radarr` → `/data/torrents/movies`. Set a share-ratio
   limit if you want.

Re-run the port-forward whenever you need the WebUI later.

#### Sonarr and Radarr

These stay internal (no hostname). Port-forward to reach the WebUI, e.g.:

```bash
kubectl -n media port-forward svc/sonarr 8989:8989   # radarr: svc/radarr 7878:7878
```

In each, add a root folder (`/data/media/tv` for Sonarr, `/data/media/movies`
for Radarr) and a **qBittorrent** download client:

- Host: `qbittorrent.qbittorrent.svc.cluster.local`, port `8080`
- Username `admin` and the WebUI password you set for qBittorrent (see the
  qBittorrent section above)
- Category: `tv-sonarr` (Sonarr) / `radarr` (Radarr)
- Enable **Remove Completed Downloads** so imports MOVE instead of copy (the
  exFAT no-hardlink workaround)

### 6. Cluster access via SSO (kubectl + Headlamp)

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

### 7. Cleanup

```bash
rm "${KUBECONFIG}"; unset KUBECONFIG
```

## Verification

```bash
flux get kustomizations          # all Ready=True
kubectl get pods -A              # all Running
kubectl get certificate -A       # all Ready=True (Let's Encrypt)
```
