# Build stage
FROM golang:1.22-alpine AS builder

# Set GOTOOLCHAIN to allow automatic toolchain downloads if needed
ENV GOTOOLCHAIN=auto

# Install build dependencies
RUN apk add --no-cache git

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o airbnb-scraper .

# Runtime stage
FROM alpine:latest

# Install Chromium and required dependencies
RUN apk add --no-cache \
    chromium \
    chromium-chromedriver \
    nss \
    freetype \
    freetype-dev \
    harfbuzz \
    ca-certificates \
    ttf-freefont \
    ttf-liberation \
    && rm -rf /var/cache/apk/*

# Set Chromium path
ENV CHROME_BIN=/usr/bin/chromium-browser
ENV CHROMIUM_FLAGS="--no-sandbox --headless --disable-gpu"

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/airbnb-scraper .

# Run the application
CMD ["./airbnb-scraper"]

