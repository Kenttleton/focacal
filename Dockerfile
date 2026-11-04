# ── Stage 1: build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /focacal ./main.go

# ── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata curl

# Non-root user for security.
RUN addgroup -g 1001 focacal && adduser -u 1001 -G focacal -s /bin/sh -D focacal

COPY --from=builder /focacal /usr/local/bin/focacal

# /data holds the SQLite DB and recovery key; /data/config is the config
# directory (bind-mounted from ./config by docker-compose.yml, else created on
# first start). It is deliberately not created here, so an old single-file
# mount at /data/config.yaml is still detected on upgrade.
RUN mkdir -p /data && chown focacal:focacal /data

USER focacal
VOLUME ["/data"]

EXPOSE 5232 5233 5234 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -f http://localhost:5233/health || exit 1

ENTRYPOINT ["focacal"]
CMD ["start"]
