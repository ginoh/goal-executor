FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY cmd/demo-api/main.go .
RUN CGO_ENABLED=0 go build -o /demo-api main.go

# psql provides the demo's DB client without a separate Go driver dependency.
FROM postgres:17-alpine
COPY --from=build /demo-api /app/demo-api
USER postgres
ENTRYPOINT ["/app/demo-api"]
