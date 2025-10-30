FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY main.go ./

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o cpu-agent .

# Final stage
FROM alpine:latest

# Install ca-certificates and kubectl
RUN apk --no-cache add ca-certificates curl && \
    curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/bin/kubectl

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/cpu-agent .

# Run the binary
CMD ["./cpu-agent"]