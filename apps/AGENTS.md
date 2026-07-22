# Apps Directory Conventions

This directory contains the Kubernetes applications reconciled by **FluxCD** via
GitOps. Each app is applied by a Flux `Kustomization` in `flux/cluster/<app>.yaml`
(which sets `dependsOn` ordering and SOPS decryption). Adding an app means adding
`apps/<app>/` **and** a `flux/cluster/<app>.yaml`, plus a listener in
`apps/gateway/gateway.yaml` if it is exposed.

## Structure & Conventions

- **Kustomize-first**: every app has a `kustomization.yaml`
  (`apiVersion: kustomize.config.k8s.io/v1beta1`) listing its resources and a
  `labels: [{pairs: {app.kubernetes.io/part-of: <app>}}]` block (never the
  deprecated `commonLabels`).
- **Namespacing**: each app defines its own `namespace.yaml` (namespace name ==
  app name) with the label `inject-ca-bundle: "true"` so trust-manager injects
  the CA bundle. Include it in the `resources` list. Two exceptions: `media` is a
  shared namespace hosting several components (`jellyfin`, `seerr`, `sonarr`,
  `radarr`) under `apps/media/<component>/`; `cilium` holds only generic
  cluster-wide Cilium policies and has no namespace.
- **Pod Security**: the cluster enforces the `restricted` PSA by default. Every
  workload must be admissible — set a pod + container securityContext
  (`runAsNonRoot: true`, `runAsUser`/`runAsGroup` non-zero, `fsGroup`,
  `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`,
  `seccompProfile.type: RuntimeDefault`). Images that normally start as root for
  PUID/PGID (hotio, paperless, kavita) are run **rootless** (drop PUID/PGID; rely
  on `fsGroup` + host dirs owned `1000:1000`). The sole exception is
  `qbittorrent`, whose gluetun sidecar needs `NET_ADMIN`: its namespace is
  labelled `pod-security.kubernetes.io/enforce: privileged` and it stays in its
  own namespace (it is not part of `media`).
- **One resource per file.** Group related resources into subdirectories with
  their own `kustomization.yaml` (e.g. `networkpolicies/`, `cert-manager/config/`).
- **Helm integration (Flux)**: third-party charts are deployed with a Flux
  `HelmRelease` + `HelmRepository`/`OCIRepository` (Flux's kustomize-controller
  does **not** run `kustomize build --enable-helm`). Put values inline under
  `spec.values`. Reference example: `apps/media/jellyfin`.
- **Plain manifests**: for apps without a chart, write `deployment.yaml`,
  `service.yaml`, etc. Reference example: `apps/paperless-ngx`.
- **Labeling**: use `labels` with `pairs` for `app.kubernetes.io/part-of`.
- **Cluster-scoped objects** (`CiliumClusterwideNetworkPolicy`, `ClusterRole`,
  `ClusterRoleBinding`, …) live **with the app they belong to** (locality of
  behaviour) — e.g. homepage's `ClusterRole`/`ClusterRoleBinding` sit in
  `apps/homepage/`, and the CNPG database-pod `CiliumClusterwideNetworkPolicy`
  sits in `apps/cloudnative-pg-operator/networkpolicies/`. Only the *generic*
  cluster-wide Cilium policies that belong to no single app (default-deny, shared
  DNS egress) live in the `cilium` app, nested by kind
  (`apps/cilium/ciliumclusterwidenetworkpolicy/<name>.yaml`), reconciled by
  `flux/cluster/cilium.yaml`.

## NetworkPolicies (`apps/<app>/networkpolicies/`)

Cilium enforces policy. **Default-deny (ingress AND egress) is cluster-wide**,
declared once in `apps/cilium/ciliumclusterwidenetworkpolicy/` and applied to
every namespace except `kube-system` and `cilium` (the only exemptions). Every
other namespace — including infra ones (`flux-system`, `cert-manager`,
`external-dns`, `cloudnative-pg`, `gateway`) — is default-denied and carries its
own allow policies. Because egress is denied by default, same-namespace traffic
must be allowed in **both** directions. The generic cluster-wide policies also
grant **DNS** egress (to kube-dns) for all workloads — never add per-app DNS
rules. The CNPG database-pod policy (operator → database ingress + database →
apiserver egress for pods labelled `cnpg.io/cluster`) lives with the operator in
`apps/cloudnative-pg-operator/networkpolicies/cloudnative-pg.yaml`.

So each app's `networkpolicies/` typically contains only:
- `ingress-allow-intra-namespace.yaml` — allow ingress from the same namespace.
- `egress-allow-intra-namespace.yaml` — allow egress to the same namespace.
- `egress-allow-<dest>.yaml` — app-specific egress as a `CiliumNetworkPolicy`
  (e.g. `toEntities: [world]` for internet, `toEntities: [kube-apiserver]`,
  cross-namespace `toEndpoints`). Internet egress to the authentik OIDC issuer
  currently uses `world` (sso.tomerhanochi.com resolves to the gateway LB IP);
  this can later be tightened to the gateway CIDR `192.168.68.64/32`.
- `ingress-allow-<other>.yaml` — when another namespace must reach this app
  (e.g. `media` → qbittorrent).
- `ingress-allow-gateway.yaml` — for exposed apps only: a `CiliumNetworkPolicy`
  with `fromEntities: [ingress]` scoped to the app's container port(s).

## Exposure (Gateway API)

Apps are exposed through the shared Cilium `Gateway` (`apps/gateway`). Each has an
HTTPS listener (`sectionName` == app name) with a cert-manager-issued Let's
Encrypt certificate. Expose an app with an `HTTPRoute` (`route.yaml`) referencing
`parentRefs: [{name: default, namespace: gateway, sectionName: <app>}]`, and add
a per-app `ingress-allow-gateway.yaml` (`CiliumNetworkPolicy`,
`fromEntities: [ingress]`, scoped to the app's container port) so gateway traffic
is admitted past default-deny. external-dns then creates the Cloudflare DNS record
from the route. Internal-only backends (sonarr, radarr, qbittorrent) have no
listener, no `route.yaml`, and no gateway ingress policy. Only `jellyfin` and
`seerr` in the `media` namespace are exposed.

## Storage

- Shared media/download library lives on the host at `/var/mnt/data` (`media/`
  clean library + `torrents/` downloads). `restricted` PSA forbids `hostPath`, so
  the library is exposed through statically-provisioned `local`
  `PersistentVolume`s (cluster-scoped, `storageClassName: ""`, `nodeAffinity`
  pinned to `kubernetes.io/hostname: control-plane`) bound by a namespaced `PVC`
  via `volumeName`. Apps that hardlink (sonarr, radarr, qbittorrent) get a
  read-write PV over the whole `/var/mnt/data` at `/data`; consumers (jellyfin,
  kavita) get a `ReadOnlyMany` PV over `/var/mnt/data/media`. One PV+PVC per
  consumer (multiple PVs may point at the same host path). The host dir must be
  owned by UID/GID `1000`. Reference: `apps/media/jellyfin/persistentvolume.yaml`.
- Per-app config/state uses a `PersistentVolumeClaim` with the default
  StorageClass (k3s local-path), `ReadWriteOnce`, and a `Recreate` strategy.

## Databases

Postgres-backed apps get a CloudNativePG `Cluster` named `<app>-database` (db and
owner without hyphens), with credentials in a `<app>-database-credentials` secret.
CNPG auto-generates TLS; connect via `<app>-database-rw.<app>.svc:5432`.

## Secrets (SOPS + age)

Secrets are `*.sops.yaml` files encrypted with the age recipient in `.sops.yaml`
and decrypted in-cluster by Flux (no ksops). They are listed directly in the
app's `kustomization.yaml` `resources`. Create/rotate values with
`sops set <file> '["stringData"]["KEY"]' '"value"'`. Only the personal secrets
(Cloudflare token, ProtonVPN key) ship as placeholders to fill at install; app DB
passwords and OIDC client secrets are committed encrypted (see
[INSTALLATION.md](../INSTALLATION.md)).

## SSO (authentik / OIDC)

authentik (`https://sso.tomerhanochi.com`) is the OIDC provider. Everything is
GitOps: OIDC providers/applications and the passkey enrollment flow are declared
as **blueprints** (`apps/authentik/blueprints/`), mounted from the
`authentik-blueprints` ConfigMap and applied by the authentik worker. Each client
has a fixed `client_id` (the app name) and a `client_secret` injected into the
blueprint via `!Env` from the `authentik-oidc` secret; the same secret value is
committed into the consuming app's own secret. **Each app has its own per-app
issuer** `https://sso.tomerhanochi.com/application/o/<slug>/` (not one shared
issuer). Native-OIDC apps read the client id/secret from a secret (forgejo,
paperless-ngx, headlamp). Jellyfin and kavita can only be configured through
their own APIs, so a small per-app bootstrap tool does it automatically (still
GitOps, no manual UI): `images/jellyfin-bootstrap` (SSO plugin install +
provider registration) and `images/kavita-bootstrap` (first admin + OIDC via
`/api/Settings`) run as an initContainer/Job in `apps/jellyfin` and `apps/kavita`,
reading credentials from a per-app secret. Seerr has no client of its own — it
signs in via Jellyfin, which is behind SSO. qbittorrent has no OIDC. The k3s
API server trusts authentik's `kubernetes` application as an issuer (see
`images/os/.../k3s/config.yaml`); Headlamp and kubelogin use it for cluster access.
Self-service registration is **passkey-only** (WebAuthn); new users are created
inactive. Adding a client = add a provider+application entry to
`apps/authentik/blueprints/oidc-clients.yaml`, a `*_CLIENT_SECRET` to the
`authentik-oidc` secret, and wire the app to its per-app issuer.

## Applications

| Application | Purpose |
| :--- | :--- |
| **cert-manager** | TLS: Let's Encrypt via Cloudflare DNS-01 (`letsencrypt` ClusterIssuer) plus an internal CA and trust-manager CA bundle. |
| **cloudnative-pg** | PostgreSQL operator managing per-app clusters. |
| **gateway** | Cilium `Gateway` with per-host HTTPS listeners + the LB IP pool / L2 announcement. |
| **external-dns** | Syncs Cloudflare DNS records from Gateway HTTPRoutes. |
| **authentik** | OIDC provider for SSO (Postgres + Redis). Clients + passkey enrollment via blueprints. |
| **headlamp** | Kubernetes web console; SSO-only login (proxies the user's OIDC token to the API server). |
| **media** | Shared namespace: jellyfin + seerr (exposed) and sonarr/radarr PVR backends (internal). |
| **qbittorrent** | Torrent client with ProtonVPN egress via a gluetun sidecar (kill switch on); own **privileged** namespace (gluetun needs `NET_ADMIN`). |
| **cilium** | Generic cluster-wide Cilium policies: default-deny + shared DNS egress. |
| **forgejo** | Git forge (official Helm chart, CNPG Postgres). |
| **kavita** | Reading server. |
| **paperless-ngx** | Document management (Postgres + redis + gotenberg + tika). |
| **homepage** | Dashboard with Kubernetes service discovery. |
