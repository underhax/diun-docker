FROM ghcr.io/crazy-max/diun:4.33.0@sha256:e324b793eb32dfb7f74d3a39421ebf090141caaadee69b8f78da63112408ee25 AS donor

FROM golang:1.27.2-alpine3.24@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS go-builder

WORKDIR /build

COPY go.mod ./
COPY init/ ./init/

RUN mkdir -p /out/data && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /out/init ./init

FROM scratch

COPY --from=go-builder --chmod=0444 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=donor --chmod=0555 /usr/local/bin/diun /usr/local/bin/diun
COPY --from=go-builder --chmod=0555 /out/init /usr/local/bin/init
COPY --from=go-builder --chmod=0700 /out/data /data

ENV DIUN_DB_PATH="/data/diun.db"

ENTRYPOINT ["/usr/local/bin/init"]
CMD ["serve"]

