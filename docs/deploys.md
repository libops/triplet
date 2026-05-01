# Deploys

`deploy/compose/` contains a single-host docker-compose setup for self-hosters.

The runtime needs a public base URL so generated IIIF identifiers are stable:

```yaml
server:
  public_base_url: "${TRIPLET_PUBLIC_BASE_URL}"
```

GCS support is implemented as a storage/cache backend without leaking Google
APIs above the storage abstraction. This has not yet been deployed against GCS,
so treat the backend as untested until it has been exercised in a real
deployment.

AWS/S3 is intentionally out of scope for this spike.

```yaml
sources:
  gcs:
    # Implemented, but not deployment-tested yet.
    bucket_url: gs://my-bucket
    prefix: images

cache:
  bucket_url: gs://triplet-cache
  prefix: derivatives
  source_bucket_url: gs://triplet-source-cache
  source_prefix: sources
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
