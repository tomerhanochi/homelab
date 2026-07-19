#!/usr/bin/env bash
#
# bootstrap-sso.sh — create the OIDC clients in pocket-id and write their
# credentials into the SOPS-encrypted secrets in this repo.
#
# pocket-id has no GitOps CRD for OAuth clients, so they are created via its
# REST API using the STATIC_API_KEY (which grants admin API access). For apps
# whose OIDC config lives in a k8s Secret (forgejo, paperless-ngx) this script
# rewrites that Secret in-place with `sops set` (re-encrypting with the age
# recipient in .sops.yaml). For apps configured in their own web UI (jellyfin,
# kavita, kubernetes/kubelogin) it prints the client id/secret for you to paste.
#
# Seerr is intentionally NOT given its own pocket-id client: it authenticates via
# "Sign in with Jellyfin", and Jellyfin itself is put behind pocket-id SSO with
# the jellyfin-plugin-sso (using the 'jellyfin' client below). So SSO for Seerr
# flows through Jellyfin.
#
# Prerequisites:
#   - kubectl pointed at the cluster (to read the STATIC_API_KEY)
#   - curl, jq, sops on PATH
#   - SOPS_AGE_KEY_FILE pointing at your age private key (needed so `sops set`
#     can decrypt+re-encrypt the secrets)
#   - pocket-id reachable at $POCKET_ID_URL
#
# After running, commit and push the changed apps/**/*.sops.yaml files so Flux
# applies them.
#
# NOTE: pocket-id's API field names can shift across v2.x. If a call fails,
# check your instance's API docs at $POCKET_ID_URL/api and adjust the JSON keys
# (callbackURLs / id / secret) below.
set -euo pipefail

POCKET_ID_URL="${POCKET_ID_URL:-https://sso.tomerhanochi.com}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for c in kubectl curl jq sops; do
  command -v "$c" >/dev/null || { echo "error: '$c' not found on PATH" >&2; exit 1; }
done
: "${SOPS_AGE_KEY_FILE:?set SOPS_AGE_KEY_FILE to your age private key file}"

echo "==> reading STATIC_API_KEY from the pocketid namespace"
API_KEY="$(kubectl -n pocketid get secret pocketid-secrets \
  -o jsonpath='{.data.STATIC_API_KEY}' | base64 -d)"
[ -n "$API_KEY" ] || { echo "error: could not read STATIC_API_KEY" >&2; exit 1; }

api() { curl -fsS -H "X-API-KEY: ${API_KEY}" "$@"; }

# create_client NAME CALLBACKS_JSON_ARRAY -> prints "CLIENT_ID CLIENT_SECRET"
create_client() {
  local name="$1" callbacks="$2" id secret existing
  existing="$(api "${POCKET_ID_URL}/api/oidc/clients?pagination[limit]=100" \
    | jq -r --arg n "$name" '(.data // .)[]? | select(.name==$n) | .id' | head -n1 || true)"
  if [ -n "${existing:-}" ] && [ "$existing" != "null" ]; then
    id="$existing"
    echo "    client '${name}' already exists (${id}); rotating its secret" >&2
  else
    id="$(api -X POST "${POCKET_ID_URL}/api/oidc/clients" \
      -H 'Content-Type: application/json' \
      -d "{\"name\":\"${name}\",\"callbackURLs\":${callbacks},\"isPublic\":false,\"pkceEnabled\":true}" \
      | jq -r '.id')"
    echo "    created client '${name}' (${id})" >&2
  fi
  secret="$(api -X POST "${POCKET_ID_URL}/api/oidc/clients/${id}/secret" | jq -r '.secret')"
  printf '%s %s\n' "$id" "$secret"
}

sset() { sops set "$1" "$2" "$3"; }        # sops set <file> <path> <json-value>
jstr() { jq -Rn --arg v "$1" '$v'; }        # encode $1 as a JSON string literal

echo "==> forgejo"
read -r FID FSECRET < <(create_client forgejo \
  '["https://git.tomerhanochi.com/user/oauth2/pocket-id/callback"]')
sset "${REPO_ROOT}/apps/forgejo/oauth-secret.sops.yaml" '["stringData"]["key"]'    "$(jstr "$FID")"
sset "${REPO_ROOT}/apps/forgejo/oauth-secret.sops.yaml" '["stringData"]["secret"]' "$(jstr "$FSECRET")"

echo "==> paperless-ngx"
read -r PID PSECRET < <(create_client paperless \
  '["https://paperless.tomerhanochi.com/accounts/oidc/pocketid/login/callback/"]')
PROVIDERS="$(jq -cn --arg id "$PID" --arg sec "$PSECRET" \
  '{openid_connect:{APPS:[{provider_id:"pocketid",name:"Pocket ID",client_id:$id,secret:$sec,settings:{server_url:"https://sso.tomerhanochi.com/.well-known/openid-configuration"}}]}}')"
sset "${REPO_ROOT}/apps/paperless-ngx/secret.sops.yaml" \
  '["stringData"]["PAPERLESS_SOCIALACCOUNT_PROVIDERS"]' "$(jstr "$PROVIDERS")"

# --- Apps configured in their own web UI: credentials are printed, not stored ---
echo "==> creating clients for UI-configured apps (paste these into each app):"
print_client() { # NAME CALLBACKS
  read -r id secret < <(create_client "$1" "$2")
  printf '    %-16s client_id=%s client_secret=%s\n' "$1" "$id" "$secret"
}
print_client kubernetes     '["http://localhost:8000","http://localhost:18000"]'
print_client jellyfin       '["https://jellyfin.tomerhanochi.com/sso/OID/redirect/pocket-id"]'
print_client kavita         '["https://kavita.tomerhanochi.com/settings/oidc"]'

cat <<'EOF'

==> done.
Next steps:
  1. Review and commit the updated encrypted secrets:
       git add apps/forgejo/oauth-secret.sops.yaml apps/paperless-ngx/secret.sops.yaml
       git commit -m "feat: wire pocket-id OIDC clients"
       git push
     Flux will apply them within a couple of minutes.
  2. For the UI-configured apps above, paste the printed client_id/secret into
     each app's OIDC settings (issuer https://sso.tomerhanochi.com).
  3. For kubectl SSO, configure kubelogin with the 'kubernetes' client (see
     INSTALLATION.md).
EOF
