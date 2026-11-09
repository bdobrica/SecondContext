# Deploy

This directory contains a published-image deployment example alongside the local
development stack in `docker-compose.yml`.

## Deploying alongside another application

[compose.secondcontext.yml](compose.secondcontext.yml) adds a gateway, a one-shot
migration service, PostgreSQL and Qdrant to an application's Compose project. Both
gateway services use `SECOND_CONTEXT_IMAGE`; the migration command is
`/app/migrate up`, with SQL files bundled at `/app/migrations`. The image's default
command remains `/app/api` and its working directory is `/app`.

Copy this Compose file into the consuming application's directory. Add values from
[secondcontext.env.example](secondcontext.env.example) to that application's
private `.env`, selecting an explicit gateway release tag or digest containing the
migration packaging fix. Older published images lack these assets; publish a new
gateway version using the [manual release workflow](../README.md#publishing-docker-images).
Use dedicated database credentials and a long random service token. The defaults
use OpenAI, with configurable provider URL, chat/embedding models and vector size;
the vector size must match the embedding model's output.

For example, in Oria's Compose file, configure the service that calls SecondContext
(replace `oria` with its actual Compose service name):

```yaml
services:
  oria:
    environment:
      SECOND_CONTEXT_BASE_URL: http://secondcontext:8080
      SECOND_CONTEXT_SERVICE_TOKEN: ${SECOND_CONTEXT_SERVICE_TOKEN}
    depends_on:
      secondcontext:
        condition: service_healthy
```

Use Oria's actual token setting if named differently. It must match the value in
the gateway's `AUTH_SERVICE_TOKENS`. By default this credential has the `oria:`
namespace; Oria must send `Authorization: Bearer <token>` and
`X-SecondContext-Subject: oria:<canonical-lowercase-UUID>` on each service request,
as specified in the [HTTP contract](../docs/contracts/service-context-v1.md).
`SECOND_CONTEXT_SERVICE_NAMESPACE` can select another application's namespace.

Start the merged project (substitute the application's primary Compose filename):

```bash
docker compose -f compose.yml -f compose.secondcontext.yml up -d --wait
docker compose -f compose.yml -f compose.secondcontext.yml run --rm --no-deps secondcontext-migrate /app/migrate version
```

The gateway starts after PostgreSQL is healthy, migrations succeed and Qdrant is
healthy. A failed migration blocks gateway startup. Oria waits for the gateway's
health check when configured as above. SecondContext's PostgreSQL and Qdrant have
their own containers and `secondcontext_postgres_data` / `secondcontext_qdrant_data`
volumes; they do not use Oria's databases or volumes. Neither backend publishes host
ports, and only the gateway joins the application's default network. Attach the
calling service to that same network if the application uses custom networks.
The gateway can reach its model provider through the default network. This example
keeps the Python knowledge service disabled; its provisioning remains optional.

To try the deployment from this repository without publishing, build
`docker build -t secondcontext-gateway:local .`, copy the example environment to
`deploy/.env`, edit its credentials, and set
`SECOND_CONTEXT_IMAGE=secondcontext-gateway:local`:

```bash
docker compose --env-file deploy/.env -f deploy/compose.secondcontext.yml up -d --wait
docker compose --env-file deploy/.env -f deploy/compose.secondcontext.yml exec -T secondcontext wget -q -O - http://127.0.0.1:8080/healthz
```

No gateway port is published in this example. Add a port mapping or reverse proxy
in the consuming deployment if host access is needed. The gateway health check
tests PostgreSQL connectivity; verify a real authenticated response with your model
configuration before enabling application traffic.

### Upgrades

Back up PostgreSQL and preserve a compatible Qdrant snapshot before upgrading.
Update `SECOND_CONTEXT_IMAGE` for both services, quiesce calling applications and
stop existing gateway instances before applying migrations. In the merged project:

```bash
docker compose -f compose.yml -f compose.secondcontext.yml pull secondcontext secondcontext-migrate
docker compose -f compose.yml -f compose.secondcontext.yml stop secondcontext
docker compose -f compose.yml -f compose.secondcontext.yml run --rm --no-deps secondcontext-migrate
docker compose -f compose.yml -f compose.secondcontext.yml run --rm --no-deps secondcontext-migrate /app/migrate version
docker compose -f compose.yml -f compose.secondcontext.yml up -d --wait secondcontext
```

Resume callers after validation. Do not use `down --volumes` during an upgrade;
that deletes persistent storage. A dirty migration version requires operator
inspection and recovery before startup, rather than an automatic rollback or force.

## Security-sensitive configuration

Production deployments that enable authentication must set `AUTH_BEARER_TOKENS` to a comma-separated list of `subject=token` entries. Each subject and token must be non-empty and unique. Bare tokens, blank entries, duplicate subjects, and duplicate token values are rejected during startup. Keep token values in the deployment platform's secret store; configuration errors report only the failing entry position and never the token value.

The boolean settings `AUTH_ENABLED`, `HTTP_METRICS_ENABLED`, and `POSTGRES_ENABLED` accept only Go `strconv.ParseBool` forms: `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false`, and `False`. Unset or blank settings use their defaults. A non-empty invalid value causes startup to fail, which prevents a typo from silently disabling authentication or infrastructure.

## Reverse proxies and client addresses

The API trusts no forwarding headers by default. Set `HTTP_TRUSTED_PROXY_CIDRS` only when a reverse proxy sits directly in front of the API, using a comma-separated list of the exact IPv4 and IPv6 proxy networks under your control:

```text
HTTP_TRUSTED_PROXY_CIDRS=10.20.0.0/16,2001:db8:1234::/48
```

Only an immediate socket peer in one of these networks may supply `X-Forwarded-For`. The API walks the chain from right to left through configured proxy networks and selects the first untrusted address as the client. This requires every controlled proxy hop to append, rather than overwrite, the header. A malformed address makes the API ignore the entire chain and use the socket peer. IPv4-mapped IPv6 peers are normalized and match equivalent IPv4 CIDRs.

Do not configure trusted proxy CIDRs when the API port is directly reachable by clients. The repository's Docker Compose file publishes port 8080 directly and therefore explicitly leaves `HTTP_TRUSTED_PROXY_CIDRS` empty. Broad ranges such as `0.0.0.0/0`, `::/0`, a Docker host's client-facing network, or a cloud provider's complete address space allow untrusted callers to forge rate-limit and audit-log identities.

The resolved address is used consistently for unauthenticated rate limiting and the structured HTTP log field `remote_ip`. `X-Real-IP`, `True-Client-IP`, and RFC 7239 `Forwarded` are not accepted.

## Required verification

Pull requests and pushes to `main` run `make verify`. The workflow provisions pinned Postgres 16.13 and Qdrant 1.15.5 services with readiness checks, applies migrations, and requires the verbose database-backed suite to complete without skipped tests. This makes the tenant-isolation boundary a required, visible test class rather than an optional local check.

Run the same checks locally with Docker Compose v2:

```bash
make verify
```

Use `make test-unit` for the fast dependency-free lane or `make test-integration` for only the mandatory integration lane. An externally managed isolated database may be selected with `POSTGRES_DSN`; otherwise the integration target creates and removes its own Compose services and volumes.

`make test-docker` builds the gateway image and exercises this deployment against
fresh isolated volumes, including clean/repeated migrations, a service-authenticated
request and startup refusal after a migration failure. GitHub Actions runs it as
a separate required verification job; it uses no model calls or live credentials.

## Database upgrades and recovery

Back up Postgres before applying migrations. Restore canonical state first and
reconcile incomplete outcome stages and the memory index before serving retrieval.
Outcome retries repair their associated points; there is no general-purpose
gateway memory-index rebuild CLI. Preserve a compatible Qdrant snapshot and use
an explicit reconciliation plan. See [operations](../docs/operations.md) for
inspection and retry procedures.

For multi-user service clients and deletion, apply migration 000003 and use the
[service authentication and purge contract](../docs/contracts/service-context-v1.md).
Coordinate all API instances and budget the additional subject-lock connections as
described in [operations](../docs/operations.md).

## Optional knowledge service and image releases

The [knowledge-base guide](../docs/knowledge-base.md) covers the separate API/UI,
database and collection. Provision/migrate/start it with the opt-in Compose profile
using the [knowledge runbook](../docs/operations.md#knowledge-service); ordinary
gateway deployment needs no Python service.

The gateway and knowledge API/UI have independent manually bumped Docker image
versions. The [publication workflow](../README.md#publishing-docker-images) pushes
to Quay only for committed version increases on `main`. Publication does not
deploy an application or apply its migrations. Use explicit versions/digests and
run each application's migration command during deployment.

### Managed history (CH3)

The Compose fragment exposes `SECOND_CONTEXT_HISTORY_*` settings and defaults
capture to disabled. Publish a compatible image, apply migration 000005 with writers
stopped, and follow [history rollout/recovery](../docs/conversation-history.md).
Verify the scoped capability before enabling a consumer. Oria's OCH2/OCH3 adapter,
accepted-reply outbox and new disclosure remain separate release prerequisites.
Do not downgrade the schema or run an older image that ignores session fences.
