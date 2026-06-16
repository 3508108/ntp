# ── stage 1: build ─────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS build

WORKDIR /src

# залежності першими (cache layer)
COPY go.mod go.sum* ./
RUN go mod download

# sources (cmd/ntp включає //go:embed dashboard.html, тому копіюємо його туди ж)
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY dashboard.html ./cmd/ntp/

# static binary (pure-Go SQLite через modernc.org/sqlite)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /out/ntp-dashboard ./cmd/ntp

# ── stage 2: minimal runtime image ─────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates curl && \
    adduser -D -u 1000 ntp

WORKDIR /app

COPY --from=build /out/ntp-dashboard /app/ntp-dashboard
RUN mkdir -p /var/lib/ntp && chown -R ntp:ntp /var/lib/ntp /app

USER ntp

EXPOSE 8080

ENV NTP_DB=/var/lib/ntp/ntp.db \
    NTP_ADDR=:8080

HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -sf http://localhost:8080/ntp/status >/dev/null || exit 1

CMD ["/app/ntp-dashboard"]
