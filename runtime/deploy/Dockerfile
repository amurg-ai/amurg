FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /runtime ./runtime/cmd/amurg-runtime

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -S amurg && adduser -S amurg -G amurg

COPY --from=build /runtime /usr/local/bin/amurg-runtime

USER amurg

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD pgrep amurg-runtime || exit 1

ENTRYPOINT ["amurg-runtime"]
CMD ["-config", "/etc/amurg/config.json"]
