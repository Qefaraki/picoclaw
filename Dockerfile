# ============================================================
# Stage 1: Build the picoclaw binary
# ============================================================
FROM golang:1.26.0-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN make build

# ============================================================
# Stage 2: Minimal runtime image
# ============================================================
FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata curl python3 py3-requests

# Copy binary and scripts
COPY --from=builder /src/build/picoclaw /usr/local/bin/picoclaw
COPY scripts/ /usr/local/lib/picoclaw/scripts/

# Create picoclaw home directory and bake in config
RUN /usr/local/bin/picoclaw onboard
COPY config/config.json /root/.picoclaw/config.json

ENTRYPOINT ["picoclaw"]
CMD ["gateway"]
