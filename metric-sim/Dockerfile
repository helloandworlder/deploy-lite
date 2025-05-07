# Stage 1: Build the Go application
FROM golang:1.20-alpine AS builder

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the Go app
# -o metric-sim: output file name
# -ldflags="-w -s": strip debug information to reduce binary size
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o metric-sim .

# Stage 2: Create a lightweight image
FROM alpine:latest

# Set the Current Working Directory inside the container
WORKDIR /root/

# Copy the Pre-built binary file from the previous stage
COPY --from=builder /app/metric-sim .

# Expose port 8080 to the outside world
EXPOSE 8080

# Command to run the executable
CMD ["./metric-sim"]