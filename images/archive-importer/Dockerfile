# syntax=docker/dockerfile:1
FROM golang:1.26.3-bookworm AS build
WORKDIR /src
# Paths are relative to the repo root (kaniko's build context = CI_PROJECT_DIR).
COPY images/archive-importer/go.mod images/archive-importer/go.sum ./
RUN go mod download
COPY images/archive-importer/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/archive-importer ./cmd/archive-importer

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/archive-importer /usr/local/bin/archive-importer
USER nonroot
ENTRYPOINT ["/usr/local/bin/archive-importer"]
