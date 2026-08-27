FROM golang:1.24-alpine AS builder

ARG GOTOOLCHAIN=auto
ENV GOTOOLCHAIN=${GOTOOLCHAIN}

WORKDIR /app

RUN apk add --no-cache git

# Copy go mod files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY cmd ./cmd
COPY internal ./internal

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o cutmax-server ./cmd/server

# Runtime image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/cutmax-server .
RUN mkdir -p /app/uploads

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://localhost:3000/api/health || exit 1

CMD ["./cutmax-server"]
