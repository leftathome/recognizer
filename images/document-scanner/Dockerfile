FROM golang:1.26-bookworm AS builder

WORKDIR /build
COPY images/document-scanner/scanner-session-manager/ .
RUN CGO_ENABLED=0 go build -o /scanner-session-manager ./cmd/scanner

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    sane-utils \
    libsane-dev \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Epson epsonscan2 driver (installed at image build time from Epson's packages)
# COPY epsonscan2/ /opt/epsonscan2/
# RUN dpkg -i /opt/epsonscan2/*.deb || apt-get -f install -y

COPY --from=builder /scanner-session-manager /usr/local/bin/scanner-session-manager

EXPOSE 8080

CMD ["scanner-session-manager"]
