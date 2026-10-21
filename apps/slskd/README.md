# slskd credentials

Replace the encrypted Soulseek account placeholders with your own credentials:

```sh
sops apps/slskd/soulseek-username.sops.txt
sops apps/slskd/soulseek-password.sops.txt
```

Flux decrypts these files before building the Secret. Kustomize gives the Secret
content hash, so changing either credential rolls the Deployment automatically.
