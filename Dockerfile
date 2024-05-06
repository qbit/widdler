FROM golang:1.17-alpine AS builder

RUN go install suah.dev/widdler@latest

FROM alpine:3

RUN mkdir -p /app/data/html

WORKDIR /app

EXPOSE 8080

VOLUME /app/data

COPY --from=builder /go/bin/* /app/

CMD [ "./widdler", "-auth", "false", "-wikis", "/app/data/html", "-http", "0.0.0.0:8080" ]