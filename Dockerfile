FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/testagram-edge ./cmd/edge
FROM alpine:3.20
RUN addgroup -S edge && adduser -S -G edge edge
WORKDIR /app
COPY --from=build /out/testagram-edge /app/testagram-edge
RUN mkdir -p /app/cache && chown -R edge:edge /app
USER edge
ENV LISTEN_ADDR=:8080 CACHE_DIR=/app/cache
EXPOSE 8080
ENTRYPOINT ["/app/testagram-edge"]
