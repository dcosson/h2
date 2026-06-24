# Hosted Linear relay for h2 (see docs/linear-agent-setup.md).
# Build context is the repo root:
#   docker build -f deploy/linear-relay/Dockerfile -t h2-linear-relay .
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/h2 ./cmd/h2

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/h2 /usr/local/bin/h2
# Configuration is via H2_RELAY_* environment variables (see compose/README).
# Persist installs by mounting a volume and setting H2_RELAY_STATE_PATH.
ENV H2_RELAY_ADDR=":8080"
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/h2", "linear", "relay"]
