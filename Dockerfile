# Start from the official Golang base image
# Start from the official Golang base image for building the application
FROM golang:1.23-alpine AS builder

# Set the Current Working Directory inside the builder container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source from the current directory to the Working Directory inside the builder container
COPY . .

# Build the Go app
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o main .

# Start a new stage from scratch
FROM alpine:latest

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy the pre-built binary file from the builder stage
COPY --from=builder /app/main .

# Command to run the executable
CMD ["./main"]
