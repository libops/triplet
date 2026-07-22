# syntax=docker/dockerfile:1.25@sha256:0adf442eae370b6087e08edc7c50b552d80ddf261576f4ebd6421006b2461f12

FROM debian:trixie-20260610@sha256:fe7312b5f05bf5f43fad76bcd8945642e4e47a68aefd1b73f447615899d0fac1 AS vips-build

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG TARGETARCH

# renovate: datasource=github-releases depName=libvips packageName=libvips/libvips
ARG VIPS_VERSION=8.18.3
ARG \
    # renovate: datasource=repology depName=debian_13/build-essential
    BUILD_ESSENTIAL_VERSION=12.12 \
    # renovate: datasource=repology depName=debian_13/ca-certificates
    CA_CERTIFICATES_VERSION=20250419 \
    # renovate: datasource=repology depName=debian_13/curl
    CURL_VERSION=8.14.1-2+deb13u4 \
    # renovate: datasource=repology depName=debian_13/libcgif-dev
    LIBCGIF_DEV_VERSION=0.5.0-1 \
    # renovate: datasource=repology depName=debian_13/libexpat1-dev
    LIBEXPAT1_DEV_VERSION=2.7.1-2 \
    # renovate: datasource=repology depName=debian_13/libglib2.0-dev
    LIBGLIB2_0_DEV_VERSION=2.84.4-3~deb13u3 \
    # renovate: datasource=repology depName=debian_13/libimagequant-dev
    LIBIMAGEQUANT_DEV_VERSION=2.18.0-1+b2 \
    # renovate: datasource=repology depName=debian_13/libjpeg62-turbo-dev
    LIBJPEG62_TURBO_DEV_VERSION=1:2.1.5-4 \
    # renovate: datasource=repology depName=debian_13/liblcms2-dev
    LIBLCMS2_DEV_VERSION=2.16-2+deb13u2 \
    # renovate: datasource=repology depName=debian_13/libopenjp2-7-dev
    LIBOPENJP2_7_DEV_VERSION=2.5.3-2.1~deb13u2 \
    # renovate: datasource=repology depName=debian_13/libpng-dev
    LIBPNG_DEV_VERSION=1.6.48-1+deb13u5 \
    # renovate: datasource=repology depName=debian_13/libspng-dev
    LIBSPNG_DEV_VERSION=0.7.4-2 \
    # renovate: datasource=repology depName=debian_13/libtiff-dev
    LIBTIFF_DEV_VERSION=4.7.0-3+deb13u3 \
    # renovate: datasource=repology depName=debian_13/libwebp-dev
    LIBWEBP_DEV_VERSION=1.5.0-0.1 \
    # renovate: datasource=repology depName=debian_13/meson
    MESON_VERSION=1.7.0-1 \
    # renovate: datasource=repology depName=debian_13/ninja-build
    NINJA_BUILD_VERSION=1.12.1-1 \
    # renovate: datasource=repology depName=debian_13/pkg-config
    PKG_CONFIG_VERSION=1.8.1-4 \
    # renovate: datasource=repology depName=debian_13/python3
    PYTHON3_VERSION=3.13.5-1

RUN NINJA_BUILD_VERSION_HACK="${NINJA_BUILD_VERSION}$([ "${TARGETARCH}" = "arm64" ] && echo "+b1" || echo "")" && \
  apt-get update && apt-get install -y --no-install-recommends \
    build-essential="${BUILD_ESSENTIAL_VERSION}" \
    ca-certificates="${CA_CERTIFICATES_VERSION}" \
    curl="${CURL_VERSION}" \
    libcgif-dev="${LIBCGIF_DEV_VERSION}" \
    libexpat1-dev="${LIBEXPAT1_DEV_VERSION}" \
    libglib2.0-dev="${LIBGLIB2_0_DEV_VERSION}" \
    libimagequant-dev="${LIBIMAGEQUANT_DEV_VERSION}" \
    libjpeg62-turbo-dev="${LIBJPEG62_TURBO_DEV_VERSION}" \
    liblcms2-dev="${LIBLCMS2_DEV_VERSION}" \
    libopenjp2-7-dev="${LIBOPENJP2_7_DEV_VERSION}" \
    libpng-dev="${LIBPNG_DEV_VERSION}" \
    libspng-dev="${LIBSPNG_DEV_VERSION}" \
    libtiff-dev="${LIBTIFF_DEV_VERSION}" \
    libwebp-dev="${LIBWEBP_DEV_VERSION}" \
    meson="${MESON_VERSION}" \
    ninja-build="${NINJA_BUILD_VERSION_HACK}" \
    pkg-config="${PKG_CONFIG_VERSION}" \
    python3="${PYTHON3_VERSION}" \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /tmp
RUN curl -fsSL -o vips.tar.xz "https://github.com/libvips/libvips/releases/download/v${VIPS_VERSION}/vips-${VIPS_VERSION}.tar.xz" \
  && tar -xf vips.tar.xz \
  && meson setup "vips-${VIPS_VERSION}/build" "vips-${VIPS_VERSION}" --prefix=/usr/local --buildtype=release \
    -Ddeprecated=false \
    -Dexamples=false \
    -Dcplusplus=false \
    -Dmodules=disabled \
    -Dintrospection=disabled \
    -Dcfitsio=disabled \
    -Dcgif=enabled \
    -Dexif=disabled \
    -Dfftw=disabled \
    -Dfontconfig=disabled \
    -Darchive=disabled \
    -Dheif=disabled \
    -Dimagequant=enabled \
    -Djpeg=enabled \
    -Duhdr=disabled \
    -Djpeg-xl=disabled \
    -Dlcms=enabled \
    -Dmagick=disabled \
    -Dmatio=disabled \
    -Dnifti=disabled \
    -Dopenexr=disabled \
    -Dopenjpeg=enabled \
    -Dopenslide=disabled \
    -Dhighway=disabled \
    -Dorc=disabled \
    -Dpangocairo=disabled \
    -Dpdfium=disabled \
    -Dpoppler=disabled \
    -Dquantizr=disabled \
    -Draw=disabled \
    -Drsvg=disabled \
    -Dpng=enabled \
    -Dspng=enabled \
    -Dtiff=enabled \
    -Dwebp=enabled \
    -Dzlib=enabled \
    -Dnsgif=true \
    -Dppm=true \
    -Danalyze=false \
    -Dradiance=false \
  && meson compile -C "vips-${VIPS_VERSION}/build" \
  && meson install -C "vips-${VIPS_VERSION}/build" \
  && ldconfig \
  && vips -l > /tmp/vips-list \
  && grep -q gifsave_buffer /tmp/vips-list \
  && rm -rf /tmp/vips*

FROM vips-build AS base
COPY --from=golang:1.26-bookworm@sha256:5f68ec6805843bd3981a951ffada82a26a0bd2631045c8f7dba483fa868f5ec5 /usr/local/go /usr/local/go

WORKDIR /src
ENV PATH=/usr/local/go/bin:$PATH
ENV PKG_CONFIG_PATH=/usr/local/lib/x86_64-linux-gnu/pkgconfig:/usr/local/lib/pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/lib/pkgconfig
ENV LD_LIBRARY_PATH=/usr/local/lib/x86_64-linux-gnu:/usr/local/lib
# Limit glibc arena growth from GLib/libvips worker threads.
ENV MALLOC_ARENA_MAX=2

FROM base AS deps
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

FROM deps AS build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/triplet ./cmd/triplet \
  && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/triplet-healthcheck ./cmd/triplet-healthcheck \
  && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/triplet-cache-cleanup ./cmd/triplet-cache-cleanup

FROM base AS test-runner
WORKDIR /app
ENTRYPOINT ["/bin/bash"]

FROM debian:trixie-20260610@sha256:fe7312b5f05bf5f43fad76bcd8945642e4e47a68aefd1b73f447615899d0fac1 AS runtime

ENV LD_LIBRARY_PATH=/usr/local/lib/x86_64-linux-gnu:/usr/local/lib
ENV TRIPLET_PUBLIC_BASE_URL=http://localhost:8080
# Limit glibc arena growth from GLib/libvips worker threads.
ENV MALLOC_ARENA_MAX=2

ARG \
    # renovate: datasource=repology depName=debian_13/ca-certificates
    RUNTIME_CA_CERTIFICATES_VERSION=20250419 \
    # renovate: datasource=repology depName=debian_13/libcgif0
    LIBCGIF0_VERSION=0.5.0-1 \
    # renovate: datasource=repology depName=debian_13/libexpat1
    LIBEXPAT1_VERSION=2.7.1-2 \
    # renovate: datasource=repology depName=debian_13/libglib2.0-0t64
    LIBGLIB2_0_0T64_VERSION=2.84.4-3~deb13u3 \
    # renovate: datasource=repology depName=debian_13/libimagequant0
    LIBIMAGEQUANT0_VERSION=2.18.0-1+b2 \
    # renovate: datasource=repology depName=debian_13/libjpeg62-turbo
    LIBJPEG62_TURBO_VERSION=1:2.1.5-4 \
    # renovate: datasource=repology depName=debian_13/liblcms2-2
    LIBLCMS2_2_VERSION=2.16-2+deb13u2 \
    # renovate: datasource=repology depName=debian_13/libopenjp2-7
    LIBOPENJP2_7_VERSION=2.5.3-2.1~deb13u2 \
    # renovate: datasource=repology depName=debian_13/libpng16-16t64
    LIBPNG16_16T64_VERSION=1.6.48-1+deb13u5 \
    # renovate: datasource=repology depName=debian_13/libspng0
    LIBSPNG0_VERSION=0.7.4-2 \
    # renovate: datasource=repology depName=debian_13/libtiff6
    LIBTIFF6_VERSION=4.7.0-3+deb13u3 \
    # renovate: datasource=repology depName=debian_13/libwebp7
    LIBWEBP7_VERSION=1.5.0-0.1 \
    # renovate: datasource=repology depName=debian_13/libwebpdemux2
    LIBWEBPDEMUX2_VERSION=1.5.0-0.1 \
    # renovate: datasource=repology depName=debian_13/libwebpmux3
    LIBWEBPMUX3_VERSION=1.5.0-0.1 \
    # renovate: datasource=repology depName=debian_13/zlib1g
    ZLIB1G_VERSION=1:1.3.dfsg+really1.3.1-1+b1

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates="${RUNTIME_CA_CERTIFICATES_VERSION}" \
    libcgif0="${LIBCGIF0_VERSION}" \
    libexpat1="${LIBEXPAT1_VERSION}" \
    libglib2.0-0t64="${LIBGLIB2_0_0T64_VERSION}" \
    libimagequant0="${LIBIMAGEQUANT0_VERSION}" \
    libjpeg62-turbo="${LIBJPEG62_TURBO_VERSION}" \
    liblcms2-2="${LIBLCMS2_2_VERSION}" \
    libopenjp2-7="${LIBOPENJP2_7_VERSION}" \
    libpng16-16t64="${LIBPNG16_16T64_VERSION}" \
    libspng0="${LIBSPNG0_VERSION}" \
    libtiff6="${LIBTIFF6_VERSION}" \
    libwebp7="${LIBWEBP7_VERSION}" \
    libwebpdemux2="${LIBWEBPDEMUX2_VERSION}" \
    libwebpmux3="${LIBWEBPMUX3_VERSION}" \
    zlib1g="${ZLIB1G_VERSION}" \
  && rm -rf /var/lib/apt/lists/* /tmp/*

COPY --from=vips-build /usr/local /usr/local
RUN rm -rf \
    /usr/local/include \
    /usr/local/lib/pkgconfig \
    /usr/local/lib/x86_64-linux-gnu/pkgconfig \
    /usr/local/share/aclocal \
    /usr/local/share/doc \
    /usr/local/share/gtk-doc \
    /usr/local/share/man \
  && ldconfig \
  && groupadd --system triplet \
  && useradd --system --gid triplet --uid 100 --home-dir /nonexistent --shell /usr/sbin/nologin triplet

WORKDIR /var/lib/triplet
RUN mkdir -p /var/lib/triplet/cache /var/lib/triplet/presentation \
    /var/lib/triplet/source-cache /var/lib/triplet/testdata/images \
  && chown -R triplet:triplet /var/lib/triplet
COPY --chown=triplet:triplet deploy/compose/images/ /var/lib/triplet/testdata/images/
COPY --chown=triplet:triplet deploy/compose/presentation/ /var/lib/triplet/presentation/

COPY --from=build /out/triplet /usr/local/bin/triplet
COPY --from=build /out/triplet-healthcheck /usr/local/bin/triplet-healthcheck
COPY --from=build /out/triplet-cache-cleanup /usr/local/bin/triplet-cache-cleanup
COPY config.example.yaml /etc/triplet/config.yaml
RUN ldd /usr/local/bin/triplet >/dev/null

EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=5s --retries=3 CMD ["/usr/local/bin/triplet-healthcheck", "-url", "http://127.0.0.1:8080/healthz"]
USER triplet:triplet
ENTRYPOINT ["/usr/local/bin/triplet"]
CMD ["-config", "/etc/triplet/config.yaml"]
