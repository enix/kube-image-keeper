FROM golang:1.16-alpine3.14 AS build

WORKDIR /app

COPY ./go.* ./

RUN go mod download

COPY cmd cmd
COPY internal internal

RUN go build -o docker-cache-registry cmd/main.go

###########################################

FROM alpine:3.14

EXPOSE 8080

COPY --from=build /app/docker-cache-registry /usr/local/bin/

CMD [ "docker-cache-registry" ]
