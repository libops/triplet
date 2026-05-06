FROM python:3.14-alpine3.22@sha256:355522f4e6b640e9255e6b6830ef52f32ece54df43db93f61101f48c5d4c6658

WORKDIR /work

RUN apk add --no-cache git && \
    pip install uv && \
    uv pip install --break-system-packages --system zensical==0.0.29

EXPOSE 8080

ENTRYPOINT ["zensical"]
CMD ["serve", "--dev-addr", "0.0.0.0:8080"]
