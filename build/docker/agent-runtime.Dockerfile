FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.work ./
COPY packages/common packages/common
COPY services/agent-runtime services/agent-runtime
WORKDIR /src/services/agent-runtime
RUN GOWORK=off go build -trimpath -ldflags="-s -w" -o /out/agent-runtime ./cmd

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && adduser -D -H niceagent
WORKDIR /app
COPY --from=build /out/agent-runtime /app/agent-runtime
RUN chown -R niceagent:niceagent /app

ENV AGENT_RUNTIME_ADDR=:8081
EXPOSE 8081
USER niceagent

ENTRYPOINT ["/app/agent-runtime"]
