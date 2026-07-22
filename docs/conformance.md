# Conformance

Triplet consumes machine-readable IIIF artifacts from
[`github.com/libops/iiif-spec`](https://github.com/libops/iiif-spec) instead of
owning local spec vendoring and schema generation tooling.

For Go code, Triplet imports:

- `github.com/libops/iiif-spec/image/v3/gen`
- `github.com/libops/iiif-spec/image/v3/schema`
- `github.com/libops/iiif-spec/presentation/v3/gen/...`
- `github.com/libops/iiif-spec/presentation/v3/schema`
- `github.com/libops/iiif-spec/extension/textgranularity/schema`

Triplet's local `types/` packages are thin aliases or wrappers on top of those
imported wire types where the server needs stable names or extension fields
beyond the upstream schemas.

Presentation writes and reads use the extension-aware validators from
`iiif-spec`, including its standalone Canvas validation. This keeps legal
JSON-LD extension properties byte-exact while still validating core properties
and requiring the Presentation context to be the final top-level context.
Annotation pages and standalone annotations additionally use the generic IIIF
Text Granularity schema. Application-specific OCR profiles belong in the
application, not in Triplet.

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
    # dsn: triplet:triplet@tcp(mariadb:3306)/triplet?parseTime=true
    write_enabled: false
```

Presentation supports path-keyed `Manifest`, `Canvas`, `Collection`,
`AnnotationCollection`, `AnnotationPage`, and `Annotation` resources. It
preserves request bytes exactly and validates each representation before both
storage and delivery. Create uses `If-None-Match: *`; replace and delete use a
strong `If-Match`. Conditional GET/HEAD, complete mutation CORS headers, and
route/body `id` coherence are covered by handler and store contract tests.
