FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/testagram-edge ./cmd/edge && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck

FROM alpine:3.20
RUN addgroup -S edge && adduser -S -G edge edge
WORKDIR /app
COPY --from=build /out/testagram-edge /app/testagram-edge
COPY --from=build /out/healthcheck /healthcheck
RUN mkdir -p /app/cache && chown -R edge:edge /app
USER edge
ENV LISTEN_ADDR=:8080
ENV INTERNAL_LISTEN_ADDR=:8082
ENV HEALTHCHECK_URL=http://127.0.0.1:8082/readyz
ENV CACHE_DIR=/app/cache
EXPOSE 8080 8082
HEALTHCHECK --interval=15s --timeout=5s --retries=6 --start-period=10s CMD ["/healthcheck"]
ENTRYPOINT ["/app/testagram-edge"]
