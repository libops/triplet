# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM debian:trixie-20260421@sha256:35b8ff74ead4880f22090b617372daff0ccae742eb5674455d542bef71ef1999 AS vips-build

ARG VIPS_VERSION=8.18.0

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    libcgif-dev \
    libexpat1-dev \
    libglib2.0-dev \
    libjpeg62-turbo-dev \
    liblcms2-dev \
    libopenjp2-7-dev \
    libpng-dev \
    libspng-dev \
    libtiff-dev \
    libwebp-dev \
    meson \
    ninja-build \
    pkg-config \
    python3 \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /tmp
RUN curl -fsSL -o vips.tar.xz "https://github.com/libvips/libvips/releases/download/v${VIPS_VERSION}/vips-${VIPS_VERSION}.tar.xz" \
  && tar -xf vips.tar.xz \
  && cd "vips-${VIPS_VERSION}" \
  && meson setup build --prefix=/usr/local --buildtype=release \
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
    -Dimagequant=disabled \
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
  && meson compile -C build \
  && meson install -C build \
  && ldconfig \
  && rm -rf /tmp/vips*

FROM vips-build AS base
COPY --from=golang:1.26-bookworm@sha256:47ce5636e9936b2c5cbf708925578ef386b4f8872aec74a67bd13a627d242b19 /usr/local/go /usr/local/go

WORKDIR /src
ENV PATH=/usr/local/go/bin:$PATH
ENV PKG_CONFIG_PATH=/usr/local/lib/x86_64-linux-gnu/pkgconfig:/usr/local/lib/pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/lib/pkgconfig
ENV LD_LIBRARY_PATH=/usr/local/lib/x86_64-linux-gnu:/usr/local/lib

FROM base AS deps
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

FROM deps AS build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags='-s -w' -o /out/triplet ./cmd/triplet

FROM base AS test-runner
WORKDIR /app
ENTRYPOINT ["/bin/bash"]

FROM debian:trixie-20260421@sha256:35b8ff74ead4880f22090b617372daff0ccae742eb5674455d542bef71ef1999 AS runtime

ENV LD_LIBRARY_PATH=/usr/local/lib/x86_64-linux-gnu:/usr/local/lib

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libcgif0 \
    libexpat1 \
    libglib2.0-0t64 \
    libjpeg62-turbo \
    liblcms2-2 \
    libopenjp2-7 \
    libpng16-16t64 \
    libspng0 \
    libtiff6 \
    libwebp7 \
    libwebpdemux2 \
    libwebpmux3 \
    zlib1g \
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
  && ldconfig

RUN groupadd --system triplet \
  && useradd --system --gid triplet --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin triplet

COPY --from=build /out/triplet /usr/local/bin/triplet
COPY config.example.yaml /etc/triplet/config.yaml
RUN ldd /usr/local/bin/triplet >/dev/null

EXPOSE 8080
USER triplet:triplet
ENTRYPOINT ["/usr/local/bin/triplet"]
CMD ["-config", "/etc/triplet/config.yaml"]
