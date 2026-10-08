# syntax=docker/dockerfile:1
FROM golang:1.26.3-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends libusb-1.0-0-dev pkg-config \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/ ./third_party/
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -buildvcs=false -o /out/djonehub ./cmd/djonehub-macos

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libusb-1.0-0 ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/djonehub /usr/local/bin/djonehub
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/djonehub/
ENV XDG_CONFIG_HOME=/data
VOLUME ["/data"]
EXPOSE 7575
HEALTHCHECK --interval=10s --timeout=5s --start-period=15s --retries=3 \
    CMD curl --noproxy '*' -fsS http://127.0.0.1:7575/api/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/djonehub"]
CMD ["-listen", "0.0.0.0:7575"]
