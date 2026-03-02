# Authentik Configuration Guide

This guide explains how to manage and add additional OIDC clients to Authentik using a secure, GitOps-friendly approach powered by Kustomize and SOPS.

## Architecture

Our setup separates non-sensitive configuration from sensitive values.

1.  **Blueprints (`blueprints.yaml`):** Non-secret client configurations are defined in this file. It is a standard ConfigMap that is safe to store in plaintext in Git.
2.  **Encrypted Secrets (`*.sops.yaml`):** Sensitive OIDC `client_secret` values are stored in individual, encrypted YAML files (e.g., `argocd-oidc-client-secret.sops.yaml`).
3.  **Secret Generator (`secret-generator.yaml`):** This Kustomize generator uses the `ksops` plugin to decrypt the `.sops.yaml` files during deployment, transforming them into standard Kubernetes Secrets.
4.  **Helm Configuration (`values.yaml`):**
    *   `blueprints.configMaps` tells Authentik to load our non-secret blueprints.
    *   `extraVolumes` and `extraVolumeMounts` mount the decrypted secrets into the Authentik pods at separate paths (e.g., `/run/secrets/argocd/` and `/run/secrets/jellyfin/`).
5.  **`!File` Tag:** Inside `blueprints.yaml`, the `client_secret` is securely populated using Authentik's `!File` tag, which reads the value from the mounted secret file at runtime (e.g., `!File /run/secrets/argocd/client_secret`).

This architecture ensures no plaintext secrets are ever stored in the Git repository.

## How to Add a New OIDC Client

1.  **Create a New Secret File:**
    Create a new file named `[app-name]-oidc-client-secret.sops.yaml`. Define the secret value within it.
    ```yaml
    # This file will be encrypted with SOPS
    apiVersion: v1
    kind: Secret
    metadata:
      name: [app-name]-oidc-client-secret
      namespace: authentik
    stringData:
      client_secret: "a-very-secure-secret"
    ```

2.  **Encrypt the File:**
    Use `sops --encrypt --in-place [app-name]-oidc-client-secret.sops.yaml` to encrypt the new file.

3.  **Update the Secret Generator:**
    Open `apps/authentik/secret-generator.yaml` and add your new `.sops.yaml` file to the `files` list.

4.  **Mount the New Secret:**
    Open `apps/authentik/values.yaml` and add new `extraVolumes` and `extraVolumeMounts` entries for your new secret in both the `server` and `worker` sections. Ensure the `mountPath` is unique.

5.  **Create the Blueprint:**
    Open `apps/authentik/blueprints.yaml` and add a new blueprint configuration for your application. Use the `!File` tag to reference the `client_secret` from the unique mount path you defined.
    ```yaml
    # In blueprints.yaml
    [app-name].yaml: |
      # ... blueprint metadata ...
      client_secret: !File /run/secrets/[app-name]/client_secret
      # ... rest of blueprint ...
    ```

6.  **Apply Changes:**
    Commit the new and updated files to Git. Your GitOps controller will automatically decrypt the secrets and apply the new configuration.

## References
- [KSOPS Plugin](https://github.com/viaduct-ai/kustomize-sops)
- [Authentik `!File` Tag](https://goauthentik.io/docs/blueprints/tags#file)
