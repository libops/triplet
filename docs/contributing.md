# Contributing

## Contributing to Triplet

Build Triplet from the repository root:

```bash
make build
```

Run the test suite:

```bash
make test
```

Before opening a pull request, format the code and run the relevant checks for
the area you changed:

```bash
make fmt
make test
```

For changes that affect IIIF behavior, run the conformance checks:

```bash
make install-tools
make conformance
```

### Updating IIIF generated code

Triplet does not generate IIIF schema code directly in this repository. Generated
IIIF artifacts come from
[`github.com/libops/iiif-spec`](https://github.com/libops/iiif-spec), which
pulls in the upstream IIIF machine-readable specifications and publishes Go
packages for Triplet to import.

Triplet imports generated Image and Presentation packages from:

- `github.com/libops/iiif-spec/image/v3/gen`
- `github.com/libops/iiif-spec/image/v3/schema`
- `github.com/libops/iiif-spec/presentation/v3/gen/...`

When the generated IIIF surface needs to change, make and test the generator
change in `github.com/libops/iiif-spec` first. After that change is available as
a tag or commit, update Triplet's module dependency:

```bash
go get github.com/libops/iiif-spec@<version-or-commit>
go mod tidy
make fmt
make test
make install-tools
make conformance
```

For local testing before the `iiif-spec` change is published, use a local Go
workspace with Triplet and `iiif-spec` checked out next to each other:

```bash
go work init .
go work use ../iiif-spec
make test
make conformance
```

The `go.work` file is for local development only. After the `iiif-spec` change
is published, update Triplet to the published tag or commit before opening a
Triplet pull request:

```bash
go get github.com/libops/iiif-spec@<version-or-commit>
go mod tidy
```

For performance-sensitive changes, regenerate benchmark fixtures as needed and
run the IIIF benchmark target:

```bash
make benchmark-fixtures
make benchmark-iiif
```

## Contributing to the docs

Triplet's documentation is built as a Zensical site from the files in `docs/`.
Use the docs Make targets from the repository root.

Build the static site into `site/`:

```bash
make docs-build
```

Serve the docs with live reload at <http://localhost:8888>:

```bash
make docs-serve
```

Build the static site and serve `site/` with Python's HTTP server, matching the
GitHub Pages artifact more closely:

```bash
make docs-preview
```
