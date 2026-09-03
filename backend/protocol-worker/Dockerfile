FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/protocol-worker ./cmd/worker && CGO_ENABLED=0 go build -o /out/protocol-simulate ./cmd/simulate

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/protocol-worker /app/protocol-worker
COPY --from=build /out/protocol-simulate /app/protocol-simulate
ENV API_BASE_URL=http://api:8080
CMD ["/app/protocol-worker"]
