FROM golang:1.22-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/easy .

FROM alpine:3.20

RUN addgroup -S easy \
	&& adduser -S -G easy easy \
	&& mkdir -p /data \
	&& chown -R easy:easy /data

USER easy
WORKDIR /app

COPY --from=build /out/easy /app/easy

ENV EASY_DB=/data/easy.db
ENV EASY_PORT=8080
ENV EASY_INTERVAL=10s

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
	CMD wget -qO- "http://127.0.0.1:${EASY_PORT}/api/interval" >/dev/null || exit 1

ENTRYPOINT ["/app/easy"]
