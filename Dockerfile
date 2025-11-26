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
RUN CGO_ENABLED=0 GOOS=linux go build -o bnb-fetcher .

# Runtime stage
FROM alpine:latest

# Install Chromium and minimal required dependencies only
RUN apk add --no-cache \
    chromium \
    nss \
    freetype \
    harfbuzz \
    ca-certificates \
    ttf-freefont \
    && rm -rf /var/cache/apk/* \
    && rm -rf /usr/share/chromium/locales \
    && rm -rf /usr/share/chromium/resources \
    && find /usr/share/chromium -name "*.pak" ! -name "chrome_100_percent.pak" ! -name "chrome_200_percent.pak" -delete || true

# Set Chromium path and memory-optimized flags
ENV CHROME_BIN=/usr/bin/chromium-browser
ENV CHROMIUM_FLAGS="--no-sandbox --headless --disable-gpu --disable-dev-shm-usage --single-process --disable-setuid-sandbox"

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/bnb-fetcher .

# Run the application
CMD ["./bnb-fetcher"]

