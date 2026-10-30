#!/bin/bash
# Runs on the instance via SSM Run Command (AWS-RunShellScript) as root.
# deploy.yml prepends `export` lines for API_IMAGE, WEB_IMAGE, TAG, GHCR_USER,
# GHCR_TOKEN and COMPOSE_B64 (the base64 docker-compose.prod.yml), then this body.
set -euo pipefail

cd /home/ec2-user/viatuta
echo "$COMPOSE_B64" | base64 -d > docker-compose.prod.yml
chown ec2-user:ec2-user docker-compose.prod.yml

# Rewrite .env from Secrets Manager on every deploy, using the instance role
# (see SecretsKey and read-app-secrets in deploy/aws/viatuta-stack.yaml).
# Written with mode 600 and never echoed; the values are alphanumeric so they
# are safe as unquoted .env values.
export AWS_DEFAULT_REGION=us-east-1
get_secret() {
  aws secretsmanager get-secret-value --secret-id "$1" --query SecretString --output text
}
# Assign first so a failed fetch aborts the script instead of writing an empty value.
pg_password="$(get_secret viatuta/production/postgres-password)"
jwt_secret="$(get_secret viatuta/production/jwt-secret)"
[ -n "$pg_password" ] && [ -n "$jwt_secret" ] || { echo "empty secret value" >&2; exit 1; }
(
  umask 077
  printf 'POSTGRES_PASSWORD=%s\nVIATUTA_JWT_SECRET=%s\n' "$pg_password" "$jwt_secret" > .env.new
)
unset pg_password jwt_secret
chown ec2-user:ec2-user .env.new
mv .env.new .env

# Keep the login in a throwaway config dir so no credentials persist on disk.
export DOCKER_CONFIG
DOCKER_CONFIG="$(mktemp -d)"
trap 'rm -rf "$DOCKER_CONFIG"' EXIT

echo "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin
export API_IMAGE WEB_IMAGE TAG
docker compose -f docker-compose.prod.yml pull api web
docker compose -f docker-compose.prod.yml up -d --remove-orphans
docker image prune -f
