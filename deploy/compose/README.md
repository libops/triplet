# Self-host with docker-compose

Single-host deployment of Triplet. Source images come from the local
`./images/` directory mounted read-only into the container. Byte-exact,
path-keyed Presentation resources and derivative caches use Docker named
volumes. A one-shot, network-disabled initializer grants the rootless Triplet
user ownership of only those three writable volume roots before the server
starts. The server itself still runs without root or Linux capabilities on a
read-only root filesystem, and host directories do not need world-writable
permissions.

```sh
mkdir -p images
cp /path/to/your/sample.png images/
docker compose up
```

MariaDB is behind the `integration` Compose profile so normal image-serving
smoke runs only start triplet. To start MariaDB too:

```sh
COMPOSE_PROFILES=integration docker compose up -d mariadb
```

To test a locally built image instead of GHCR:

```sh
docker build --target runtime -t triplet:dev ../..
TRIPLET_IMAGE=triplet:dev docker compose up
```

CI and branch-based integration runs can use a pushed GHCR image tag:

```sh
GIT_BRANCH=my-branch docker compose up
```

Then:

```sh
curl http://localhost:8080/iiif/3/sample.png/info.json
```

Presentation writes are disabled by default. To exercise generic conditional
resource writes and conformance checks:

```sh
TRIPLET_PRESENTATION_WRITE_ENABLED=true TRIPLET_PRESENTATION_WRITE_TOKEN=dev-token docker compose up
TRIPLET_PRESENTATION_WRITE_TOKEN=dev-token ../../scripts/conformance.sh
```

Back up the `presentation` named volume as durable application data. The
`cache` and `source-cache` volumes contain rebuildable derivatives.

For TLS, front this with nginx, Caddy, or your reverse proxy of choice — the
triplet container does not terminate TLS itself.
