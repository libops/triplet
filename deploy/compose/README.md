# Self-host with docker-compose

Single-host deployment of triplet. Source images come from the local
`./images/` directory mounted into the container. Presentation manifests and
annotation pages come from `./presentation/`.

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

Then:

```sh
curl http://localhost:8080/iiif/3/sample.png/info.json
```

For TLS, front this with nginx, Caddy, or your reverse proxy of choice — the
triplet container does not terminate TLS itself.
