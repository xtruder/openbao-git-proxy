# syntax=docker/dockerfile:1

FROM golang:1.27.1-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN test -n "${TARGETARCH}" && \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
	go build -trimpath -ldflags='-s -w' -o /out/openbao-git-proxy ./cmd/openbao-git-proxy

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 65532 proxy && \
    adduser -S -D -H -u 65532 -G proxy proxy
COPY --from=go-build --chown=65532:65532 /out/openbao-git-proxy /usr/local/bin/openbao-git-proxy
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/openbao-git-proxy"]
