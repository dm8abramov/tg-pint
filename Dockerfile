FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tg-pint .

FROM alpine:3.22

RUN adduser -D -H -s /sbin/nologin app
USER app

COPY --from=builder /out/tg-pint /usr/local/bin/tg-pint

ENTRYPOINT ["/usr/local/bin/tg-pint"]
