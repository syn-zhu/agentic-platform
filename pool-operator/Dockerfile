# pool-operator/Dockerfile
FROM golang:1.24 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o pool-operator ./cmd/operator

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /app/pool-operator /pool-operator
USER nonroot:nonroot
ENTRYPOINT ["/pool-operator"]
