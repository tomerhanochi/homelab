# Apps Directory Conventions

This directory contains the Kubernetes applications managed by ArgoCD via GitOps.

## Structure & Conventions

- **Directory-based Discovery**: Each subdirectory under `apps/` is automatically discovered by the ArgoCD `ApplicationSet` (defined in `apps/argocd/applicationset.yaml`).
- **Kustomize-first**: Every application must contain a `kustomization.yaml` file.
- **Namespacing**: Each application should define its own namespace in a `namespace.yaml` file and include it in the `resources` list of its `kustomization.yaml`.
- **Helm Integration**: Third-party applications are integrated using Kustomize's `helmCharts` field. A local `values.yaml` is used to override default chart values.
- **Labeling**: Resources should include `labels` in the `kustomization.yaml` (typically `app.kubernetes.io/part-of: <app-name>`) for consistent resource tracking. Use the `labels` field with `pairs` as `commonLabels` is deprecated.
- **One Resource Per File**: Each Kubernetes resource must be in its own file. If multiple resources are related (e.g., a PostgreSQL cluster with its database, role, and secret), group them into a subdirectory with its own `kustomization.yaml` that aggregates them. See `apps/authentik/postgres/` as an example.
- **NetworkPolicies**: Each application must have a `networkpolicies/` subdirectory containing ingress policies:
  - `ingress-deny-by-default.yaml` - Denies all ingress traffic by default (empty policy with `policyTypes: [Ingress]`)
  - `ingress-allow-intra-namespace.yaml` - Allows ingress from pods within the same namespace
  - `ingress-allow-tailscale-proxy.yaml` - Allows ingress from Tailscale proxy pods (uses `namespaceSelector` + `podSelector` in same `from` entry for AND logic)
  - `ingress-allow-cloudnative-pg-operator.yaml` - (If applicable) Allows ingress from cloudnative-pg operator to database pods
  - All policy names must use the `ingress-` prefix for clarity
- **Tailscale ProxyClass**: Applications exposed via Tailscale must use a ProxyClass (`tailscale-ingress-proxy-class`) that applies the label `tailscale.com/proxy-type: ingress` to proxy pods. NetworkPolicies should match this label for precise ingress control.
- **Manual Ingress Resources**: For Tailscale ingress, create manual `ingress.yaml` files instead of using Helm chart values. Use `defaultBackend` (not `rules`) and annotate with `tailscale.com/proxy-group` and `tailscale.com/funnel`.

## Applications

| Application | Purpose |
| :--- | :--- |
| **argocd** | The GitOps engine that synchronizes this repository's state with the cluster. Includes an `ApplicationSet` for automatic app discovery. |
| **cilium** | The CNI (Container Network Interface) providing high-performance networking, security (NetworkPolicies), and load balancing. |
| **cloudnative-pg** | The CloudNativePG operator, which manages the lifecycle of PostgreSQL clusters on Kubernetes. |
| **authentik** | Identity and access management (IAM) provider with its own namespaced PostgreSQL cluster. |
| **tailscale** | The Tailscale operator, which exposes Kubernetes resources (like Services and Ingress) onto your Tailscale network. |
