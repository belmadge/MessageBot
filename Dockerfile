FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=build /out/api ./api
COPY migrations ./migrations
EXPOSE 8080
ENTRYPOINT ["./api"]
