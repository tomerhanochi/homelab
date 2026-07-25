# Homelab

A self-hosted, single-node Kubernetes homelab built on **bootc** (a bootable
container image) and managed with **GitOps** (FluxCD). The OS is immutable and
rebuilt from an image; everything running on the cluster is reconciled from this
repo.

## Overview

- **bootc image** — a Fedora-based bootable container with K3s pre-installed and
  hardened.
- **FluxCD** — reconciles the cluster from `apps/`; the only manual step is
  bootstrapping Flux itself.
- **Kustomize + SOPS/age** — declarative manifests with encrypted secrets that
  Flux decrypts in-cluster.
- **Cilium** — eBPF CNI providing the Gateway API, L2 load balancing, and network
  policies.
- **authentik** — OIDC provider for single sign-on across apps and the cluster,
  with passkey-only self-registration.

## Components

| Application | Description |
|-------------|-------------|
| **cilium** | CNI: Gateway API, L2 announcements, network policies |
| **gateway** | Shared Cilium Gateway with per-host HTTPS listeners |
| **cert-manager** | TLS: Let's Encrypt (Cloudflare DNS-01) + an internal CA (trust-manager) |
| **external-dns** | Creates Cloudflare DNS records from Gateway routes |
| **cloudnative-pg** | PostgreSQL operator (per-app clusters) |
| **authentik** | OIDC provider (SSO): passkey-only registration, clients declared as blueprints |
| **headlamp** | Kubernetes web console (SSO login) |
| **jellyfin** | Media server |
| **forgejo** | Git forge |
| **kavita** | Reading server (books/manga/comics) |
| **qbittorrent** | Torrent client, egress via ProtonVPN (gluetun sidecar), SSO via oauth2-proxy |
| **filebrowser** | Web file manager, SSO via oauth2-proxy |

## Layout

```
homelab/
├── images/os/   # bootc image source (Containerfile, setup.sh, K3s + Cilium config)
├── apps/        # GitOps: Flux config (apps/flux*) and every Kubernetes app
├── .sops.yaml   # SOPS/age recipients
├── Brewfile     # local CLI tooling
├── INSTALLATION.md
└── README.md
```

Flux is configured under `apps/`:

- `apps/flux-operator/` — the Flux GitOps Toolkit controllers (generated manifest;
  regenerate, don't hand-edit).
- `apps/flux/repositories/` — the `GitRepository` tracking this repo.
- `apps/flux/cluster/` — one Flux `Kustomization` per app, with `dependsOn`
  ordering and SOPS decryption.

## How it works

1. The server boots the bootc image; K3s starts (Cilium is applied as a bundled
   manifest).
2. Flux is bootstrapped manually once (see [INSTALLATION.md](INSTALLATION.md)).
3. Flux applies `apps/flux/cluster/`, reconciling every app in `dependsOn` order
   and decrypting SOPS secrets in-cluster.
4. external-dns writes Cloudflare records and cert-manager issues Let's Encrypt
   certificates for each gateway hostname.

## Installation

See [INSTALLATION.md](INSTALLATION.md).

## License

[MIT](LICENSE)
