# syntax=docker/dockerfile:1
FROM golang:1.26.3-bookworm AS build
WORKDIR /src
# Paths are relative to the repo root (kaniko's build context = CI_PROJECT_DIR).
COPY images/archive-importer/go.mod images/archive-importer/go.sum ./
COPY images/archive-importer/ .
# No eager `go mod download`: with no arguments it fetches the ENTIRE module
# graph, including the private walhelm-go dep that only ./cmd/walhelm-fetch
# needs and that public runners cannot reach (its sole remote is
# gitlab.orac.local -- see Dockerfile.walhelm-fetch). `go build` downloads
# only the pruned set this binary actually imports, which walhelm-go is not
# part of. Costs the separate dependency cache layer; buildx layer cache
# still covers the common case.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/archive-importer ./cmd/archive-importer

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/archive-importer /usr/local/bin/archive-importer
USER nonroot
ENTRYPOINT ["/usr/local/bin/archive-importer"]
