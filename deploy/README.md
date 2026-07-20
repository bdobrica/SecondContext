# Deploy

This directory is reserved for deployment assets beyond the local `docker-compose.yml` file.

## Security-sensitive configuration

Production deployments that enable authentication must set `AUTH_BEARER_TOKENS` to a comma-separated list of `subject=token` entries. Each subject and token must be non-empty and unique. Bare tokens, blank entries, duplicate subjects, and duplicate token values are rejected during startup. Keep token values in the deployment platform's secret store; configuration errors report only the failing entry position and never the token value.

The boolean settings `AUTH_ENABLED`, `HTTP_METRICS_ENABLED`, and `POSTGRES_ENABLED` accept only Go `strconv.ParseBool` forms: `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false`, and `False`. Unset or blank settings use their defaults. A non-empty invalid value causes startup to fail, which prevents a typo from silently disabling authentication or infrastructure.
