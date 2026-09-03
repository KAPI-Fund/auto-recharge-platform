FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/recharge-api ./cmd/server

FROM alpine:3.21
RUN addgroup -S app && adduser -S -G app app && apk add --no-cache ca-certificates
USER app
WORKDIR /app
COPY --from=build /out/recharge-api /app/recharge-api
EXPOSE 8080
ENTRYPOINT ["/app/recharge-api"]
