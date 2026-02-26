FROM golang:1.26.0-alpine AS builder

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on

WORKDIR /build

# Copy go.mod and go.sum first (cached layer unless dependencies change)
COPY go.mod go.sum ./

# Download dependencies (cached layer)
RUN go mod download

# Copy source code (this layer changes frequently)
COPY . .

# Build the binancetrader service
RUN go build -ldflags="-s -w" -o binancetrader ./cmd/binancetrader

# Slim final image
FROM alpine:latest

EXPOSE 8123

COPY --from=builder /build/binancetrader /usr/bin/

RUN addgroup -S binancetrader && \
    adduser -S -G binancetrader -s /sbin/nologin binancetrader && \
    chmod +x /usr/bin/binancetrader

USER binancetrader

ENTRYPOINT ["/usr/bin/binancetrader"]

