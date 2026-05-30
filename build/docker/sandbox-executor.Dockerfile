FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.work ./
COPY packages/common packages/common
COPY services/sandbox-executor services/sandbox-executor
WORKDIR /src/services/sandbox-executor
RUN GOWORK=off go build -trimpath -ldflags="-s -w" -o /out/sandbox-executor ./cmd

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && adduser -D -H niceagent
WORKDIR /app
COPY --from=build /out/sandbox-executor /app/sandbox-executor
RUN mkdir -p /app/workspaces && chown -R niceagent:niceagent /app

ENV SANDBOX_EXECUTOR_ADDR=:8082
EXPOSE 8082
USER niceagent

ENTRYPOINT ["/app/sandbox-executor"]
