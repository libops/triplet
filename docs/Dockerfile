FROM python:3.12-alpine3.22@sha256:a190708a2dec1bd18b1decb539f8e8f5407abaa9bf39cacda583f7f8c11db322

WORKDIR /work

RUN apk add --no-cache git && \
    pip install uv && \
    uv pip install --break-system-packages --system zensical==0.0.29

EXPOSE 8080

ENTRYPOINT ["zensical"]
CMD ["serve", "--dev-addr", "0.0.0.0:8080"]
