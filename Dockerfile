# Use the official Golang image to create a build artifact.
FROM golang:1.22.4-alpine3.20

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy everything from the current directory to the PWD (Present Working Directory) inside the container
COPY . .

# Download all the dependencies
RUN go mod tidy

# Build the Go app
RUN go build -o /chat-app

# Command to run the executable
CMD ["/chat-app"]
