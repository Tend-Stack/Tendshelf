# Tendshelf server image. Build from the repository root:
#   docker build --build-arg VERSION=2026.7.0 -t tendshelf .
# Docs: docs/deploy.md ("Docker").

# Build stage: the Go version matches go.mod (1.27); pinned by digest so a rebuild is repeatable.
FROM golang:1.27.3-trixie@sha256:af1bfc2614ebb1dbd31af514b78740fd678850e76498762b425ecee4c5c6ea80 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Same flags as `make build`: pure Go, no cgo, version stamped into the binary.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/tendshelf ./cmd/tendshelf

# Runtime stage: Debian slim with ffmpeg (ffprobe comes in the same package).
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# A user that owns nothing but /data. Rebuild with --build-arg UID=... to match the owner of your folders.
ARG UID=1000
RUN useradd --system --uid ${UID} --no-create-home --shell /usr/sbin/nologin tendshelf \
    && mkdir /data /books \
    && chown tendshelf /data
COPY --from=build /out/tendshelf /usr/local/bin/tendshelf
USER tendshelf
# TENDSHELF_DATA also serves `docker exec ... tendshelf reset-password`, which has no -addr.
ENV TENDSHELF_DATA=/data
VOLUME /data
EXPOSE 8095
# The server has no health subcommand and the image has no curl or wget, so bash's built-in /dev/tcp
# sends a plain GET /healthz and grep checks for a 200 answer.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/8095 && printf 'GET /healthz HTTP/1.0\\r\\n\\r\\n' >&3 && head -n1 <&3 | grep -q ' 200 '"]
# Inside the container the server must listen on all addresses (the default is 127.0.0.1 only).
# Publish the port on the host as 127.0.0.1:8095 unless you want the network to reach it directly.
ENTRYPOINT ["tendshelf"]
CMD ["-addr", "0.0.0.0:8095", "-data", "/data"]
