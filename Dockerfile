FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /relay .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /relay /relay
EXPOSE 8080
ENTRYPOINT ["/relay"]
