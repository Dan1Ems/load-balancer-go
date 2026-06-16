FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY backend.go .
RUN go build -o backend backend.go

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/backend .
EXPOSE 8080
CMD ["./backend"]