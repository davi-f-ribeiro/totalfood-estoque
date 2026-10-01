# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/estoque ./cmd/server

FROM debian:12-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 65532 --no-create-home --shell /usr/sbin/nologin estoque
WORKDIR /app

COPY --from=build /out/estoque /app/estoque
COPY migrations ./migrations

ENV PORT=8080
ENV MIGRATIONS_DIR=/app/migrations
EXPOSE 8080

USER 65532:65532
ENTRYPOINT ["/app/estoque"]
