FROM --platform=$BUILDPLATFORM golang:1.23.4-alpine3.21 AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /app

COPY go.mod go.sum .
RUN go mod download

COPY . .

ENV GOCACHE=/root/.cache/go-build

RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build ./cmd/docker-backup-maestro/

FROM alpine:3.21

WORKDIR /app

COPY --from=builder /app/docker-backup-maestro /app
RUN ln -s docker-backup-maestro maestro

ENV PATH="/app:$PATH"

ENTRYPOINT ["docker-backup-maestro"]
