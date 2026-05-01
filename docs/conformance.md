# Conformance

Triplet consumes machine-readable IIIF artifacts from
[`github.com/libops/iiif-spec`](https://github.com/libops/iiif-spec) instead of
owning local spec vendoring and schema generation tooling.

For Go code, Triplet imports:

- `github.com/libops/iiif-spec/image/v3/gen`
- `github.com/libops/iiif-spec/image/v3/schema`
- `github.com/libops/iiif-spec/presentation/v3/gen/...`

Triplet's local `types/` packages are thin aliases or wrappers on top of those
imported wire types where the server needs stable names or extension fields
beyond the upstream schemas.

Triplet also tracks extension support in code and tests. In particular, the
Presentation annotation path validates the IIIF Text Granularity extension.

The IIIF API surfaces are configured independently:

```yaml
iiif:
  image:
    enabled: true
    prefix: /iiif/3
  presentation:
    enabled: false
    prefix: /presentation/v3
    root: ./testdata/presentation
    # dsn: scribe:scribe@tcp(mariadb:3306)/scribe?parseTime=true
    write_enabled: false
```

Presentation annotation writes use strong ETags and require `If-Match`, and the
Presentation CORS policy exposes `ETag` for browser-based annotation editors.
