FROM golang:1.22-alpine AS builder

# Go module 代理。默认官方源；国内网络可覆盖成 goproxy.cn 之类的国内镜像，
# 避免 `go mod download` 走到国外而拖慢构建（或消耗代理流量）：
#   docker compose build --build-arg GOPROXY=https://goproxy.cn,direct
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0 GOOS=linux GOARCH=amd64
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/convo_app .

FROM alpine:3.20
RUN apk add --no-cache bash ca-certificates netcat-openbsd
WORKDIR /app

COPY --from=builder /out/convo_app ./convo_app
COPY wait-for.sh ./wait-for.sh
COPY templates ./templates
COPY static ./static
COPY conf ./conf
RUN chmod 755 ./convo_app ./wait-for.sh

EXPOSE 9090
CMD ["./convo_app"]
