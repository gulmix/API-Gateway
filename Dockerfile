FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN  go mod download
COPY . .
RUN go build -o gateway ./cmd/gateway
RUN go build -o mockbackend ./cmd/mockbackend

FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/gateway .
COPY --from=builder /app/mockbackend .
COPY config/ config/
EXPOSE 8080 9090
CMD ["./gateway"]