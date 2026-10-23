FROM golang:1.24-alpine AS build

WORKDIR /app
COPY . .
RUN GOWORK=off go -C src/spinner mod download
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go -C src/spinner build -mod=mod -o /email-relay-smoke ./cmd/email-relay-smoke

FROM alpine:3.22
COPY --from=build /email-relay-smoke /email-relay-smoke
ENTRYPOINT ["/email-relay-smoke"]
CMD ["-addr", "0.0.0.0:2527", "-output", "/tmp/accepted.jsonl"]
