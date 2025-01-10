FROM golang:1.23.4-alpine3.21 AS builder

WORKDIR /app

COPY . .

RUN go build ./cmd/docker-backup-maestro/

FROM alpine:3.21

WORKDIR /app

COPY --from=builder /app/docker-backup-maestro /app
RUN ln -s docker-backup-maestro maestro

ENV PATH="/app:$PATH"

ENTRYPOINT ["docker-backup-maestro"]
