FROM golang:1.26-alpine AS build

WORKDIR /app
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod GOWORK=off go -C tools mod download
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOWORK=off go -C tools build -mod=readonly -trimpath \
        -ldflags="-s -w" -o /email-relay-smoke ./email-relay-smoke

FROM alpine:3.22
COPY --from=build /email-relay-smoke /email-relay-smoke
ENTRYPOINT ["/email-relay-smoke"]
CMD ["-addr", "0.0.0.0:2527", "-output", "/tmp/accepted.jsonl"]
