FROM node:22-alpine AS ui-build

WORKDIR /ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci
COPY ui/ .
RUN npm run build

FROM golang:1.24-alpine AS go-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /hub ./hub/cmd/amurg-hub

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -S amurg && adduser -S amurg -G amurg

COPY --from=go-build /hub /usr/local/bin/amurg-hub
COPY --from=ui-build /ui/dist /var/lib/amurg/ui

RUN mkdir -p /var/lib/amurg/data && chown -R amurg:amurg /var/lib/amurg

USER amurg
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["amurg-hub"]
CMD ["-config", "/etc/amurg/config.json"]
