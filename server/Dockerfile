FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ghaccel233 .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates curl
COPY --from=build /out/ghaccel233 /usr/local/bin/ghaccel233
WORKDIR /data
EXPOSE 8233
VOLUME /data
HEALTHCHECK --interval=30s --timeout=5s CMD curl -fsS http://127.0.0.1:8233/healthz || exit 1
ENTRYPOINT ["ghaccel233", "-config", "/data/config.json"]