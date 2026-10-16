# Immich

Deployed with the Immich Helm chart through a Flux HelmRelease, including
its built-in Valkey service with ephemeral storage for queues and cache.
Served at https://photos.tomerhanochi.com with native Authentik OIDC for web
and mobile. Like Jellyfin, permissions use Authentik application entitlements:
`homelab-users` receive `user`; `homelab-admins` receive `user` and `admin`.
The OAuth role mapping derives Immich's scalar `immich_role` claim from these
entitlements. Password login is disabled.
Machine learning, smart search, facial recognition, OCR, and visual duplicate
detection are disabled; no machine learning service is deployed.

## Storage

The OS image enables `var-mnt-data-photos.mount`, binding
`/var/mnt/wd-005t-02/photos` onto `/var/mnt/data/photos`. Like the existing media
mounts, this adds no dependency to K3s. Rebuild and boot the updated OS image
(or install and start the mount manually) before using the service.

Create the source directory on the mounted disk and make it writable by UID/GID
1000 before starting Immich. The static photos PV requires an existing directory
and is retained when the application is removed. Immich manages its `/data`
layout here, including uploads, thumbnails, encoded videos, and database backups.
Existing loose photos require an external library; they are not imported merely
by placing them in this directory.

PostgreSQL uses a separate 10Gi CloudNativePG volume. The PostgreSQL 18 image
loads VectorChord through a read-only image volume; bootstrap SQL installs
VectorChord, pgvector, cube, and earthdistance. Image volumes require Kubernetes
1.35+, or 1.33/1.34 with ImageVolume enabled on the API server and kubelet, and a
compatible container runtime (containerd 2.1+).

## Encrypt before publishing

These files intentionally contain generated plaintext secrets:

- `apps/immich/postgres-cluster-credentials.sops.yaml`
- `apps/immich/config.sops.yaml`
- `apps/authentik/client-secrets/immich.sops.txt`

From the repository root, encrypt them in place:

```sh
sops --encrypt --in-place apps/immich/postgres-cluster-credentials.sops.yaml
sops --encrypt --in-place apps/immich/config.sops.yaml
sops --encrypt --in-place --input-type binary --output-type binary apps/authentik/client-secrets/immich.sops.txt
```

The OAuth client secret is identical in `config.sops.yaml` and the Authentik
client secret file; update both together when rotating it. Flux is already
configured to decrypt these files.

Configuration is supplied through the chart's existing configuration Secret
(`immich-config.yaml`, containing JSON, which is valid YAML), so changes to
settings belong in that file. Restart the Immich deployment after changing it.
