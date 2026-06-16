FROM golang:1.23-alpine AS builder
RUN apk add --no-cache git
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o easy ./main.go

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /build/easy /app/easy
COPY --from=builder /build/internal/server/index.html /app/index.html
EXPOSE 8000
ENV EASY_DB=/var/lib/easy/easy.db
RUN mkdir -p /var/lib/easy && chown -R nobody:nobody /var/lib/easy
USER nobody
ENTRYPOINT ["/app/easy"]
