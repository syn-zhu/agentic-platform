FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /assignment ./cmd/assignment

FROM alpine:3.21
COPY --from=build /assignment /usr/local/bin/assignment
ENTRYPOINT ["assignment"]
