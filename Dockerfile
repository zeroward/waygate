# Build a static binary for Fedora CoreOS / Docker next to AzerothCore.
FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/waygate ./cmd/waygate

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -H -u 65532 app
WORKDIR /app
COPY --from=build /out/waygate /usr/local/bin/waygate
COPY content /app/content
USER 65532:65532
EXPOSE 3080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD wget -qO- http://127.0.0.1:3080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/waygate"]
