# Apps Directory Conventions

This directory contains the Kubernetes applications managed by ArgoCD via GitOps.

## Structure & Conventions

- **Directory-based Discovery**: Each subdirectory under `apps/` is automatically discovered by the ArgoCD `ApplicationSet` (defined in `apps/argocd/applicationset.yaml`).
- **Kustomize-first**: Every application must contain a `kustomization.yaml` file.
- **Namespacing**: Each application should define its own namespace in a `namespace.yaml` file and include it in the `resources` list of its `kustomization.yaml`.
- **Helm Integration**: Third-party applications are integrated using Kustomize's `helmCharts` field. A local `values.yaml` is used to override default chart values.
- **Labeling**: Resources should include `labels` in the `kustomization.yaml` (typically `app.kubernetes.io/part-of: <app-name>`) for consistent resource tracking. Use the `labels` field with `pairs` as `commonLabels` is deprecated.
- **One Resource Per File**: Each Kubernetes resource must be in its own file. If multiple resources are related (e.g., a PostgreSQL cluster with its database, role, and secret), group them into a subdirectory with its own `kustomization.yaml` that aggregates them. See `apps/authentik/postgres/` as an example.

## Applications

| Application | Purpose |
| :--- | :--- |
| **argocd** | The GitOps engine that synchronizes this repository's state with the cluster. Includes an `ApplicationSet` for automatic app discovery. |
| **cilium** | The CNI (Container Network Interface) providing high-performance networking, security (NetworkPolicies), and load balancing. |
| **cloudnative-pg** | The CloudNativePG operator, which manages the lifecycle of PostgreSQL clusters on Kubernetes. |
| **authentik** | Identity and access management (IAM) provider with its own namespaced PostgreSQL cluster. |
| **tailscale** | The Tailscale operator, which exposes Kubernetes resources (like Services and Ingress) onto your Tailscale network. |
