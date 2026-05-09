FROM node:22-alpine AS builder

WORKDIR /build
ENV PNPM_VERSION=11.0.9
RUN apk add --no-cache python3 make g++ \
    && corepack enable \
    && corepack prepare pnpm@${PNPM_VERSION} --activate
COPY web/package.json .
COPY web/pnpm-lock.yaml .
RUN pnpm install --frozen-lockfile --ignore-scripts
COPY ./web .
COPY ./VERSION .
RUN DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION=$(cat VERSION) pnpm run build

FROM golang:1.23-alpine AS builder2

ENV GO111MODULE=on \
    CGO_ENABLED=0 \
    GOOS=linux

WORKDIR /build

ADD go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=builder /build/dist ./web/dist
RUN go build -ldflags "-s -w -X 'one-api/common.Version=$(cat VERSION)'" -o one-api

FROM alpine:latest

RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates tzdata ffmpeg \
    && update-ca-certificates

COPY --from=builder2 /build/one-api /
EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/one-api"]
