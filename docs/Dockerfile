FROM python:3.14-alpine3.22@sha256:6b91e66ab2a880ce9ca5a1b91c70f45963ff71ff68268df056336e1a657d5efd

WORKDIR /work

RUN apk add --no-cache git && \
    pip install uv && \
    uv pip install --break-system-packages --system zensical==0.0.29

EXPOSE 8080

ENTRYPOINT ["zensical"]
CMD ["serve", "--dev-addr", "0.0.0.0:8080"]
