# Deploys

`deploy/compose/` contains a single-host docker-compose setup for self-hosters.

The runtime needs a public base URL so generated IIIF identifiers are stable:

```yaml
server:
  public_base_url: "${TRIPLET_PUBLIC_BASE_URL}"
```

The runtime exposes Prometheus metrics at `/metrics` when `metrics.enabled` is
true.

```yaml
metrics:
  enabled: true
```

## Presentation storage migrations

When using MariaDB for Presentation storage, apply the schema as a migration
step with a DDL-capable account:

```yaml
iiif:
  presentation:
    enabled: true
    prefix: /presentation/v3
    dsn: scribe:scribe@tcp(mariadb:3306)/scribe?parseTime=true
    write_enabled: false
```

```sh
triplet -config config.yaml -migrate-presentation-mariadb
```

Normal server startup does not run DDL, so the runtime DSN can use a
least-privilege account after migration.
