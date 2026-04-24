# syntax=docker/dockerfile:1.7

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/gateway \
    ./cmd/gateway

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /usr/local/bin/gateway
USER nonroot
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/gateway"]
