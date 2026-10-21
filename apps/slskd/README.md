# slskd credentials

Replace the encrypted Soulseek account placeholders in
`soulseek-credentials.sops.yaml` with your account credentials:

```sh
sops apps/slskd/soulseek-credentials.sops.yaml
```

Flux decrypts this file before applying the Secret. Restart the Deployment after
changing the credentials so slskd reads the updated values.
