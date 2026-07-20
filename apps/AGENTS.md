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
  the CA bundle. Include it in the `resources` list.
- **One resource per file.** Group related resources into subdirectories with
  their own `kustomization.yaml` (e.g. `networkpolicies/`, `cert-manager/config/`).
- **Helm integration (Flux)**: third-party charts are deployed with a Flux
  `HelmRelease` + `HelmRepository` (Flux's kustomize-controller does **not** run
  `kustomize build --enable-helm`). Put values inline under `spec.values`, or in
  a `values.yaml` surfaced via a `configMapGenerator` + `spec.valuesFrom` when
  the same values are also needed for a manual bootstrap (see `apps/cilium`).
  Reference example: `apps/jellyfin`.
- **Plain manifests**: for apps without a chart, write `deployment.yaml`,
  `service.yaml`, etc. Reference example: `apps/paperless-ngx`.
- **Labeling**: use `labels` with `pairs` for `app.kubernetes.io/part-of`.

## NetworkPolicies (`apps/<app>/networkpolicies/`)

Cilium enforces policy. Each app includes:
- `ingress-deny-by-default.yaml` — `NetworkPolicy`, `podSelector: {}`, `policyTypes: [Ingress]`.
- `ingress-allow-intra-namespace.yaml` — allow ingress from the same namespace.
- `ingress-allow-gateway.yaml` — **CiliumNetworkPolicy** with `fromEntities: [ingress]`
  (only for apps exposed via the gateway).
- `ingress-allow-cloudnative-pg-operator.yaml` — for apps with a CNPG `Cluster`.
- `ingress-allow-<other>.yaml` — when another namespace must reach this app
  (e.g. jellyseerr → sonarr/radarr).

## Exposure (Gateway API)

Apps are exposed through the shared Cilium `Gateway` (`apps/gateway`). Each has an
HTTPS listener (`sectionName` == app name) with a cert-manager-issued Let's
Encrypt certificate. Expose an app with an `HTTPRoute` (`route.yaml`) referencing
`parentRefs: [{name: default, namespace: gateway, sectionName: <app>}]`.
external-dns then creates the Cloudflare DNS record from the route. Internal-only
backends (sonarr, radarr) have no listener and no `route.yaml`.

## Storage

- Shared media/download library lives on the host at `/var/mnt/data` (`media/`
  clean library + `torrents/` downloads). Apps that hardlink (sonarr, radarr,
  qbittorrent) mount the whole `/var/mnt/data` at `/data` via an inline
  `hostPath`; consumers (jellyfin, kavita) mount only `/var/mnt/data/media`
  read-only. The host dir must be owned by UID/GID `1000`.
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
| **cilium** | eBPF CNI: networking, Gateway API, L2 LB, network policy (bootstrapped manually, then Flux-managed). |
| **cert-manager** | TLS: Let's Encrypt via Cloudflare DNS-01 (`letsencrypt` ClusterIssuer) plus an internal CA and trust-manager CA bundle. |
| **cloudnative-pg** | PostgreSQL operator managing per-app clusters. |
| **gateway** | Cilium `Gateway` with per-host HTTPS listeners + the LB IP pool / L2 announcement. |
| **external-dns** | Syncs Cloudflare DNS records from Gateway HTTPRoutes. |
| **authentik** | OIDC provider for SSO (Postgres + Redis). Clients + passkey enrollment via blueprints. |
| **headlamp** | Kubernetes web console; SSO-only login (proxies the user's OIDC token to the API server). |
| **jellyfin / jellyseerr** | Media server and request frontend. |
| **sonarr / radarr** | TV / movie PVR backends (internal only). |
| **qbittorrent** | Torrent client with ProtonVPN egress via a gluetun sidecar (kill switch on). |
| **forgejo** | Git forge (official Helm chart, CNPG Postgres). |
| **kavita** | Reading server. |
| **paperless-ngx** | Document management (Postgres + redis + gotenberg + tika). |
| **homepage** | Dashboard with Kubernetes service discovery. |
