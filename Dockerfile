# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /out/startup-daemon ./cmd

FROM scratch

COPY --from=build /out/startup-daemon /startup-daemon

EXPOSE 8080
ENTRYPOINT ["/startup-daemon"]
