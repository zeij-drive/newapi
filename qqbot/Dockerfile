FROM golang:1.25.1-alpine AS builder

ENV CGO_ENABLED=0 GOWORK=off
WORKDIR /build

COPY qqbot/go.mod qqbot/go.sum ./
RUN go mod download
COPY qqbot/ ./
RUN go build -trimpath -ldflags='-s -w' -o /out/starryapi-qqbot .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/starryapi-qqbot /usr/local/bin/starryapi-qqbot
RUN chmod 0755 /usr/local/bin/starryapi-qqbot
USER 10001:10001
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/starryapi-qqbot"]
