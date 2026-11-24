# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src
COPY client/frontend/package*.json client/frontend/
COPY server/frontend/package*.json server/frontend/
RUN --mount=type=cache,target=/root/.npm \
    npm ci --prefix client/frontend && npm ci --prefix server/frontend
COPY client/frontend/ client/frontend/
COPY server/frontend/ server/frontend/
RUN npm run build --prefix client/frontend && npm run build --prefix server/frontend

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/client/frontend/dist client/frontend/dist
COPY --from=web /src/server/frontend/dist server/frontend/dist
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    make dist "VERSION=$VERSION"

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out/data && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build \
      -ldflags "-s -w -X github.com/memory-co/agentuse/internal/version.Version=$VERSION" \
      -o /out/agentuse-server ./server/cmd/agentuse-server

FROM scratch
LABEL org.opencontainers.image.source=https://github.com/memory-co/agentuse
ENV HOME=/data TMPDIR=/data AGENTUSE_DIST=/opt/agentuse/dist
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/agentuse-server /usr/local/bin/agentuse-server
COPY --from=build /src/dist/ /opt/agentuse/dist/
COPY --from=build --chown=65532:65532 /out/data/ /data/
WORKDIR /data
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/agentuse-server"]
CMD ["start", "--self-upgrade=false"]
