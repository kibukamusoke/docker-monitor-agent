# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install build dependencies and git (needed for go mod)
RUN apk add --no-cache gcc musl-dev git

# Copy go mod file
COPY go.mod ./

# Copy source code first (needed for go mod tidy to resolve imports)
COPY . .

# Generate go.sum and download dependencies
RUN go mod tidy && go mod download

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o docker-agent .

# Final stage
FROM alpine:3.19

# Install ca-certificates for HTTPS and procps for system stats
RUN apk add --no-cache ca-certificates procps

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/docker-agent .

# Create non-root user (but we'll still need docker socket access)
RUN addgroup -g 1000 agent && \
    adduser -D -u 1000 -G agent agent

# The agent needs access to Docker socket, so we run as root
# but the API itself is limited to specific operations
USER root

# Default port
ENV AGENT_PORT=9876

EXPOSE 9876

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:9876/agent/health || exit 1

ENTRYPOINT ["./docker-agent"]
