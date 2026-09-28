# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/estoque ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/estoque /app/estoque
COPY migrations ./migrations

ENV PORT=8080
ENV MIGRATIONS_DIR=/app/migrations
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/app/estoque"]
