# syntax=docker/dockerfile:1

# The taggr image. Built for linux/amd64 and linux/arm64 by
# .github/workflows/release.yml; docker/agent/Dockerfile is a different image,
# the self-hosted Azure Pipelines agent.

ARG GO_VERSION=1.23

# Cross-compiled from the build platform, so building for another architecture
# needs no emulation.
FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
        -ldflags="-s -w \
            -X github.com/DavidW475/taggr/cmd.buildVersion=${VERSION} \
            -X github.com/DavidW475/taggr/cmd.buildCommit=${COMMIT}" \
        -o /out/taggr .

# A static binary needs no distribution, only the CA certificates the image
# already carries to reach the platform APIs over https.
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG COMMIT=unknown

LABEL org.opencontainers.image.title="taggr" \
      org.opencontainers.image.description="Create semantic version tags on git hosting platforms" \
      org.opencontainers.image.source="https://github.com/DavidW475/taggr" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"

COPY --from=build /out/taggr /usr/local/bin/taggr

USER nonroot:nonroot
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/taggr"]
