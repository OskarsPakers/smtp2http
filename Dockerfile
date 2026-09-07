# --platform=$BUILDPLATFORM keeps the toolchain on the native runner arch and
# cross-compiles to $TARGETARCH below. Pure Go (CGO_ENABLED=0) needs no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
WORKDIR /src
# go.mod/go.sum first so the module cache layer survives source edits
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETARCH
ENV CGO_ENABLED=0
RUN GOOS=linux GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /out/smtp2http .

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/smtp2http /usr/bin/smtp2http
# Runs as root because the default -listen is :smtp (port 25, privileged).
# To drop root, bind high inside and publish 25 outside -- MX records carry no
# port, so only the host side has to be 25:
#   docker run -p 25:2525 <image> -listen :2525 ...
# then add: USER nobody
ENTRYPOINT ["smtp2http"]
