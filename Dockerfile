FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY exchange/ exchange/
COPY service/ service/
COPY storage/ storage/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bin/binancetrader ./cmd/binancetrader

FROM alpine:3.24
RUN apk add --no-cache ca-certificates && \
    addgroup -S binancetrader && \
    adduser -S -G binancetrader binancetrader
COPY --from=builder /bin/binancetrader /usr/local/bin/binancetrader
USER binancetrader
ENV LOG_FILE="-"
EXPOSE 8123
ARG APP_VERSION=dev
ARG GIT_COMMIT=unknown
LABEL org.opencontainers.image.title="binancetrader" \
      org.opencontainers.image.description="Binance spot trading bot and prediction-market archive utilities" \
      org.opencontainers.image.source="https://github.com/foae/binancetrader" \
      org.opencontainers.image.version="$APP_VERSION" \
      org.opencontainers.image.revision="$GIT_COMMIT"
ENTRYPOINT ["/usr/local/bin/binancetrader"]
