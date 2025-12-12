ARG GO_VERSION=1
FROM golang:1.24-alpine as builder

RUN apk update && apk upgrade && apk add git
WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN go build -v -o /run-app .

FROM alpine:latest
#RUN apt-get update && apt-get install -y --reinstall ca-certificates
COPY --from=builder /run-app /usr/local/bin/
CMD ["run-app"]
