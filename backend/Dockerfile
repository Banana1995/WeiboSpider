FROM golang:1.26.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 1000 --create-home app \
    && mkdir -p /app/data && chown app:app /app/data
WORKDIR /app
COPY --from=build /out/server /app/server
USER app
ENV BACKEND_ADDR=0.0.0.0:5051 BACKEND_DATA_DIR=/app/data
EXPOSE 5051
ENTRYPOINT ["/app/server"]
