FROM golang:1.25.6-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /runtime ./runtime/cmd/amurg-runtime

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tmux \
    && addgroup -S amurg \
    && adduser -S amurg -G amurg -h /home/amurg \
    && mkdir -p /home/amurg/.amurg /var/lib/amurg/files \
    && chown -R amurg:amurg /home/amurg /var/lib/amurg

COPY --from=build /runtime /usr/local/bin/amurg-runtime

USER amurg

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD pgrep amurg-runtime || exit 1

ENTRYPOINT ["amurg-runtime"]
CMD ["run", "--config", "/etc/amurg/config.json"]
