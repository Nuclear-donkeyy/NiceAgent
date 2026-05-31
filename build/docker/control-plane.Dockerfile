FROM node:22-alpine AS web

WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.work ./
COPY packages/common packages/common
COPY services/control-plane services/control-plane
WORKDIR /src/services/control-plane
RUN GOWORK=off go build -trimpath -ldflags="-s -w" -o /out/control-plane ./cmd

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && adduser -D -H niceagent
WORKDIR /app
COPY --from=build /out/control-plane /app/control-plane
COPY --from=web /src/frontend/dist /app/frontend/dist
RUN chown -R niceagent:niceagent /app

ENV CONTROL_PLANE_ADDR=:8080
ENV WEB_DIST_DIR=/app/frontend/dist
EXPOSE 8080
USER niceagent

ENTRYPOINT ["/app/control-plane"]
