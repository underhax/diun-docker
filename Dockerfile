FROM ghcr.io/crazy-max/diun:4.33.1@sha256:8cc638b4c54e8a0c47898a51b20dae73037ad43310168b5da543a311dc668f77 AS donor

FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS go-builder

WORKDIR /build

COPY go.mod ./
COPY init/ ./init/

RUN mkdir -p /out/data /out/etc/ssl/certs && \
    chmod 0755 /out/etc /out/etc/ssl /out/etc/ssl/certs && \
    chmod 0700 /out/data && \
    cp /etc/ssl/certs/ca-certificates.crt /out/etc/ssl/certs/ca-certificates.crt && \
    chmod 0444 /out/etc/ssl/certs/ca-certificates.crt && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /out/init ./init

FROM scratch

COPY --from=go-builder /out/etc /etc
COPY --from=donor --chmod=0555 /usr/local/bin/diun /usr/local/bin/diun
COPY --from=go-builder --chmod=0555 /out/init /usr/local/bin/init
COPY --from=go-builder --chmod=0700 /out/data /data

ENV DIUN_DB_PATH="/data/diun.db"

ENTRYPOINT ["/usr/local/bin/init"]
CMD ["serve"]
