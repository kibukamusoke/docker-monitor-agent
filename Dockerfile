FROM golang:1.26.4-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -buildid=" \
    -o /out/docker-agent .

RUN printf 'root:x:0:0:root:/root:/sbin/nologin\nnonroot:x:65532:65532:nonroot:/nonexistent:/sbin/nologin\n' > /out/passwd && \
    printf 'root:x:0:\nnonroot:x:65532:\n' > /out/group

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/passwd /etc/passwd
COPY --from=builder /out/group /etc/group
COPY --from=builder /out/docker-agent /docker-agent

ENV AGENT_PORT=9876

EXPOSE 9876

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/docker-agent", "healthcheck"]

ENTRYPOINT ["/docker-agent"]
