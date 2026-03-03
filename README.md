# Homelab

A self-hosted Kubernetes homelab powered by **bootc** (bootable container) and **GitOps** (ArgoCD).

## Overview

Homelab is a complete Kubernetes distribution designed for bare-metal servers. It combines:

- **bootc Image**: A Fedora-based bootable container with K3s pre-configured
- **GitOps Management**: ArgoCD synchronizes the cluster state with this Git repository
- **Kustomize + SOPS**: Declarative infrastructure with encrypted secrets
- **Cilium Networking**: High-performance CNI with network policies

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Bare Metal Server                         │
│  ┌───────────────────────────────────────────────────────┐  │
│  │              Homelab bootc Image                       │  │
│  │  ┌─────────────────────────────────────────────────┐  │  │
│  │  │              K3s Kubernetes                       │  │  │
│  │  │  ┌───────────────────────────────────────────┐  │  │  │
│  │  │  │           ArgoCD (GitOps Engine)           │  │  │  │
│  │  │  │  ┌─────────────────────────────────────┐  │  │  │  │
│  │  │  │  │     Applications (apps/)             │  │  │  │  │
│  │  │  │  │  - Cilium (CNI)                      │  │  │  │  │
│  │  │  │  │  - ArgoCD (GitOps)                   │  │  │  │  │
│  │  │  │  │  - Authentik (IAM)                   │  │  │  │  │
│  │  │  │  │  - CloudNativePG (PostgreSQL)        │  │  │  │  │
│  │  │  │  │  - Tailscale (Remote Access)         │  │  │  │  │
│  │  │  │  │  - Jellyfin (Media)                  │  │  │  │  │
│  │  │  │  └─────────────────────────────────────┘  │  │  │  │
│  │  │  └───────────────────────────────────────────┘  │  │  │
│  │  └─────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                              │
│  Git Repository ◄───────► ArgoCD ApplicationSet             │
│  (kustomize + sops)         Auto-discovers apps/            │
└─────────────────────────────────────────────────────────────┘
```

## Components

### bootc Image (`image/`)

The foundation is a [bootc](https://github.com/containers/bootc) image that provides:

- **Fedora 42 Minimal** base (via `quay.io/fedora-testing/fedora-bootc:42-minimal`)
- **K3s** Kubernetes distribution (stable channel, auto-updated)
- **Security Hardening**:
  - SELinux enabled with k3s-selinux policies
  - Kernel parameters tuned for Kubernetes (`vm.overcommit_memory=1`, etc.)
  - Pod Security Admission configured
  - Audit logging enabled
  - Secrets encryption at rest
  - Network policies enabled
- **System Configuration**:
  - `core` user with passwordless sudo via polkit
  - SSH configured for key-based authentication
  - tmpfiles for `/var/home/core` persistence
  - systemd userdb for `core` user identity

**Key Image Files:**

| File | Purpose |
|------|---------|
| `Containerfile` | Builds the bootc image |
| `setup.sh` | Installs K3s and dependencies |
| `usr/lib/rancher/k3s/config.yaml` | K3s server configuration |
| `usr/lib/systemd/system/k3s.service` | K3s systemd unit |
| `usr/lib/sysctl.d/90-kubelet.conf` | Kernel tuning for Kubernetes |
| `usr/share/polkit-1/rules.d/core.rules` | Polkit rules for `core` user |

### Applications (`apps/`)

All Kubernetes applications are managed via GitOps:

| Application | Description |
|-------------|-------------|
| **argocd** | GitOps engine with ApplicationSet for auto-discovery |
| **cilium** | CNI providing networking, security, and load balancing |
| **authentik** | Identity and access management (IAM) |
| **cloudnative-pg** | PostgreSQL operator for database management |
| **tailscale** | Tailscale operator for secure remote access |
| **jellyfin** | Media server |

See [apps/AGENTS.md](apps/AGENTS.md) for conventions and structure.

## How It Works

1. **Boot**: Server boots from the homelab bootc image
2. **K3s Starts**: systemd launches K3s with hardened configuration
3. **Manual Bootstrap**: Cilium and ArgoCD are applied manually (see [INSTALLATION.md](INSTALLATION.md))
4. **GitOps Sync**: ArgoCD ApplicationSet discovers all `apps/*/kustomization.yaml` files
5. **Auto-Sync**: ArgoCD continuously synchronizes the cluster with Git state
6. **Secrets Management**: SOPS-encrypted secrets are decrypted by KSOPS during kustomize builds

## Repository Structure

```
homelab/
├── image/                    # bootc image source
│   ├── Containerfile         # Image build definition
│   ├── setup.sh              # Installation script
│   └── usr/                  # Files installed into the image
├── apps/                     # Kubernetes applications (GitOps)
│   ├── AGENTS.md             # Conventions and documentation
│   ├── argocd/               # ArgoCD (GitOps engine)
│   ├── cilium/               # Cilium (CNI)
│   ├── authentik/            # Authentik (IAM)
│   ├── cloudnative-pg/       # CloudNativePG (PostgreSQL)
│   ├── tailscale/            # Tailscale (remote access)
│   └── jellyfin/             # Jellyfin (media server)
├── .sops.yaml                # SOPS encryption configuration
├── Brewfile                  # Homebrew dependencies
├── INSTALLATION.md           # Installation instructions
└── README.md                 # This file
```

## Installation

See [INSTALLATION.md](INSTALLATION.md) for complete installation instructions.

## License

[MIT](LICENSE)
