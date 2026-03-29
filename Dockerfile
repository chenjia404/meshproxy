FROM golang:1.26-alpine AS builder

WORKDIR /app

# 安裝編譯所需工具（含 protoc，可視需要移除）
RUN apk add --no-cache build-base protobuf

COPY go.mod go.sum ./ 
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o meshproxy ./cmd/node

FROM alpine:3

WORKDIR /app

RUN addgroup -S mesh && adduser -S mesh -G mesh

COPY --from=builder /app/meshproxy /usr/local/bin/meshproxy
COPY configs/config.docker.yaml /app/config.docker.yaml

# 為非 root 用戶預先建立並授權數據目錄
RUN mkdir -p /app/data && chown -R mesh:mesh /app

USER mesh

EXPOSE 1080 4001 19080

ENTRYPOINT ["/usr/local/bin/meshproxy"]
CMD ["-config", "/app/config.docker.yaml"]

