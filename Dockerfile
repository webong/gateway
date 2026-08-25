# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the gateway executable. Composer's vendor directory is excluded from
# the Docker context and must not be interpreted as Go's vendor directory.
RUN CGO_ENABLED=0 GOOS=linux go build -mod=mod -o net-gateway ./cmd/relayer

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/net-gateway .

# Expose the port
EXPOSE 5001

# Run the application
CMD ["./net-gateway"]
