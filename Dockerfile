FROM node:18 as builder

# 安装 pnpm
RUN npm install -g pnpm@latest

WORKDIR /build
COPY web/package.json .
# 如果有 pnpm-lock.yaml，也需要复制
COPY web/pnpm-lock.yaml* .
RUN pnpm install
COPY ./web .
COPY ./VERSION .
RUN DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION=$(cat VERSION) pnpm run build

FROM golang AS builder2

ENV GO111MODULE=on \
    CGO_ENABLED=1 \
    GOOS=linux

WORKDIR /build
ADD go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=builder /build/dist ./web/dist
RUN go build -ldflags "-s -w -X 'one-api/common.Version=$(cat VERSION)' -extldflags '-static'" -o one-api

FROM alpine:3.18

RUN apk update \
    && apk upgrade \
    && apk add --no-cache ca-certificates tzdata ffmpeg \
    && update-ca-certificates

COPY --from=builder2 /build/one-api /
EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/one-api"]