# syntax=docker/dockerfile:1
FROM golang:1.26.3-bookworm AS build
WORKDIR /src
COPY go.mod ./
# go.sum is absent in the initial scaffold; copied later as a separate
# COPY once dependencies land (Task B7 adds golang.org/x/sys).
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/archive-importer ./cmd/archive-importer

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/archive-importer /usr/local/bin/archive-importer
USER nonroot
ENTRYPOINT ["/usr/local/bin/archive-importer"]
