# ArgoCD Objects

## Why This Is Separate from ArgoCD

The ApplicationSet (App of Apps pattern) must be installed **separately** from the ArgoCD installation itself due to a bootstrapping constraint:

- **ArgoCD must be fully installed and running** before any `Application` or `ApplicationSet` resources can be processed
- The ArgoCD helm chart installs the ArgoCD controllers, but the ApplicationSet controller needs to be ready before it can reconcile ApplicationSets
- If the ApplicationSet is included in the same Kustomization as the ArgoCD helm chart, it may be applied before ArgoCD is ready to handle it, causing sync failures

### Solution

1. **`apps/argocd`**: Installs the ArgoCD helm chart and namespace only
2. **`apps/argocd-objects`**: Contains the ApplicationSet that creates child applications for all apps in `apps/*`

This separation ensures ArgoCD is fully operational before the App of Apps ApplicationSet is applied.
