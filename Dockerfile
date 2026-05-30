# syntax=docker/dockerfile:1

# Stage 1: Build frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# Stage 2: Build Go backend
FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/kubesage ./cmd/server

# Stage 3: Final image
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/kubesage /app/kubesage
COPY --from=builder /src/web/dist /app/web/dist
COPY configs /app/configs
COPY migrations /app/migrations
COPY runbooks /app/runbooks
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/kubesage"]
