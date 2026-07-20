# Deploy

This directory is reserved for deployment assets beyond the local `docker-compose.yml` file.

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
