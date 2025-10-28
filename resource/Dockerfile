FROM golang:1.24.3-alpine AS builder

WORKDIR /app
COPY . .

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o resource-controller main.go

FROM alpine:3.18

RUN apk add --no-cache jq

COPY --from=builder /app/resource-controller /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/resource-controller"]