FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY cmd/demo-api/main.go .
RUN CGO_ENABLED=0 go build -o /demo-api main.go

FROM scratch
COPY --from=build /demo-api /demo-api
ENTRYPOINT ["/demo-api"]
