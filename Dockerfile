# syntax=docker/dockerfile:1

FROM golang:1.26 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/kubesage ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=builder /out/kubesage /app/kubesage
COPY configs /app/configs
COPY migrations /app/migrations
COPY runbooks /app/runbooks

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/kubesage"]
