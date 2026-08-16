# syntax=docker/dockerfile:1

# Build stage: cross-compile a static binary for the target arch.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS builder
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/microgrid ./cmd/microgrid

# Runtime stage: minimal image with only the binary and config.
FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache tzdata
ENV TZ=Asia/Shanghai
COPY --from=builder /out/microgrid /app/microgrid
COPY config.json /app/config.json
EXPOSE 51326
ENTRYPOINT ["/app/microgrid"]
