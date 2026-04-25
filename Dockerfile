# syntax=docker/dockerfile:1.7

FROM debian:bookworm AS vips-build

ARG VIPS_VERSION=8.18.0

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    libcgif-dev \
    libexif-dev \
    libexpat1-dev \
    libfftw3-dev \
    libfontconfig1-dev \
    libfreetype-dev \
    libfribidi-dev \
    libgif-dev \
    libglib2.0-dev \
    libgsf-1-dev \
    libharfbuzz-dev \
    libheif-dev \
    libimagequant-dev \
    libjpeg62-turbo-dev \
    libjxl-dev \
    liblcms2-dev \
    libmagickcore-6.q16-dev \
    libopenexr-dev \
    libopenjp2-7-dev \
    liborc-0.4-dev \
    libpango1.0-dev \
    libpng-dev \
    libpoppler-glib-dev \
    librsvg2-dev \
    libspng-dev \
    libtiff-dev \
    libwebp-dev \
    libxml2-dev \
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
  && meson compile -C build \
  && meson install -C build \
  && ldconfig \
  && rm -rf /tmp/vips*

FROM vips-build AS base
COPY --from=golang:1.26-bookworm /usr/local/go /usr/local/go

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

FROM base AS runtime

RUN groupadd --system triplet \
  && useradd --system --gid triplet --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin triplet

COPY --from=build /out/triplet /usr/local/bin/triplet
COPY config.example.yaml /etc/triplet/config.yaml

EXPOSE 8080
USER triplet:triplet
ENTRYPOINT ["/usr/local/bin/triplet"]
CMD ["-config", "/etc/triplet/config.yaml"]
