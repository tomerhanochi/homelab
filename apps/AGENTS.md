# Apps Directory Conventions

This directory contains the Kubernetes applications managed by ArgoCD via GitOps.

## Structure & Conventions

- **Directory-based Discovery**: Each subdirectory under `apps/` is automatically discovered by the ArgoCD `ApplicationSet` (defined in `apps/argocd/applicationset.yaml`).
- **Kustomize-first**: Every application must contain a `kustomization.yaml` file.
- **Namespacing**: Each application should define its own namespace in a `namespace.yaml` file and include it in the `resources` list of its `kustomization.yaml`.
- **Helm Integration**: Third-party applications are integrated using Kustomize's `helmCharts` field. A local `values.yaml` is used to override default chart values.
- **Labeling**: Resources should include `labels` in the `kustomization.yaml` (typically `app.kubernetes.io/part-of: <app-name>`) for consistent resource tracking. Use the `labels` field with `pairs` as `commonLabels` is deprecated.

## Applications

| Application | Purpose |
| :--- | :--- |
| **argocd** | The GitOps engine that synchronizes this repository's state with the cluster. Includes an `ApplicationSet` for automatic app discovery. |
| **cilium** | The CNI (Container Network Interface) providing high-performance networking, security (NetworkPolicies), and load balancing. |
| **cloudnative-pg** | The CloudNativePG operator, which manages the lifecycle of PostgreSQL clusters on Kubernetes. |
| **postgres** | A global, single-node PostgreSQL cluster instance managed by the `cloudnative-pg` operator. |
