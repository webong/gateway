FROM golang:1.24-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
COPY lib/driver/go.mod ./lib/driver/go.mod
COPY ext/roadrunner/go.mod ./ext/roadrunner/go.mod
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=mod -o /email-relay-smoke ./src/spinner/cmd/email-relay-smoke

FROM alpine:3.22
COPY --from=build /email-relay-smoke /email-relay-smoke
ENTRYPOINT ["/email-relay-smoke"]
CMD ["-addr", "0.0.0.0:2527", "-output", "/tmp/accepted.jsonl"]
