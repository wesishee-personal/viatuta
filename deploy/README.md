# Deploying Viatuta

Target: one EC2 instance (`t3.small`, Amazon Linux 2023) running `docker-compose.prod.yml`
behind Caddy at https://viatuta.wesishee.com. CI (`.github/workflows/deploy.yml`)
builds two images to GHCR on every push to `main`, then restarts the stack through
AWS Systems Manager Run Command. The workflow authenticates to AWS with GitHub OIDC, so
there are no AWS keys or SSH keys in GitHub, and the instance has no SSH port open.

The instance is defined in `deploy/aws/viatuta-stack.yaml` (CloudFormation): encrypted
30 GiB gp3 disk, IMDSv2 required, 2 GiB swap, Docker + Compose installed at boot, an
Elastic IP, and a security group opening 80/443 only. The same stack creates the GitHub
OIDC provider and a deploy role that trusts only this repo's `production` environment and
can only run commands on this one instance.

Known tradeoff: the job's `GITHUB_TOKEN` (read-only for packages, expires when the job ends)
is passed to the instance inside the SSM command so it can pull the private images. SSM
keeps command parameters in its history and in CloudTrail, so anyone with read access to
those can see an already-expired token. Moving the images to ECR would remove this.

Cost note: the AWS Free Tier on accounts created after 2025-07-15 is credit-based
(up to $200, free plan lasts 6 months). Add a Budget alert in Billing, and decide
before the plan ends whether to upgrade to a paid plan.

## One-time setup

1. **Stack** - create `viatuta-stack` in `us-east-1` from `deploy/aws/viatuta-stack.yaml`
   (console, or `aws cloudformation deploy --capabilities CAPABILITY_IAM`). The defaults
   are right for `wesishee-personal/viatuta`. `AmiId` has no default: pass the current Amazon
   Linux 2023 AMI (command in the parameter's description). It is pinned on purpose, because
   a changed AMI replaces the instance and the database lives on its disk. If the account already has a GitHub OIDC
   provider, pass its ARN as `ExistingOidcProviderArn`. `viatuta_agent` is read-only by
   default; grant it CloudFormation, EC2, IAM and SSM write access only for this step.
   When it finishes, copy the `PublicIp`, `InstanceId` and `DeployRoleArn` outputs.
2. **DNS** - at wesishee.com's DNS provider (Squarespace) add `A viatuta -> <PublicIp>`
   (leave the existing GitHub Pages records alone). Check with `dig +short viatuta.wesishee.com`.
3. **Secrets** - nothing to do by hand. The stack generates `POSTGRES_PASSWORD` and
   `VIATUTA_JWT_SECRET` in Secrets Manager (`viatuta/production/postgres-password` and
   `viatuta/production/jwt-secret`, encrypted with the `alias/viatuta-secrets` KMS key), and
   each deploy writes them to `~/viatuta/.env` (mode 600) on the instance using its role.
   Rotation is off: rotating the Postgres password would also have to change it inside the
   database, so do that manually if you ever need to.
4. **GitHub environment** - create a `production` environment (Settings -> Environments).
   The deploy role trusts only this environment, so it must exist. Restrict its deployment
   branches to `main`, and optionally require approval. Add these to the environment:
   | Kind | Name | Value |
   |---|---|---|
   | Variable | `AWS_DEPLOY_ROLE_ARN` | the `DeployRoleArn` stack output |
   | Variable | `DEPLOY_INSTANCE_ID` | the `InstanceId` stack output |

   And this repository secret (Settings -> Secrets and variables -> Actions):
   | Secret | Value |
   |---|---|
   | `VITE_MAPBOX_TOKEN` | public `pk.` Mapbox token (restrict it to the URL) |
5. **First deploy** — push to `main` (or run the Deploy workflow). Then load data once:
   ```sh
   # No SSH: get the extract onto the server from a Session Manager shell, e.g. download it
   # straight from its source into ~/viatuta/data/ (or stage it in a private S3 bucket and
   # `aws s3 cp` it down; the instance role has no S3 access by default).
   # then, with API_IMAGE/WEB_IMAGE/TAG exported as the last deploy used:
   docker compose -f docker-compose.prod.yml run --rm cli /app/viatuta ingest-osm --input /app/data/austin.osm.pbf
   docker compose -f docker-compose.prod.yml run --rm cli /app/viatuta score-edges
   docker compose -f docker-compose.prod.yml restart api
   ```
   (The graph loads at API startup, hence the restart. Elevation data under
   `data/elevation` goes in the same directory if scoring needs it.)

## Backups

```sh
docker compose -f docker-compose.prod.yml exec -T db pg_dump -U viatuta viatuta | gzip > ~/backup-$(date +%F).sql.gz
```
Put that in a nightly cron and copy it off the box.

## iOS

Set `apiBaseURL` in `ios/Viatuta/API/AppConfig.swift` to `https://viatuta.wesishee.com`
for device/release builds; drop `NSAllowsLocalNetworking` from `ios/project.yml` there.
