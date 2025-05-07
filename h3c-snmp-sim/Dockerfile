FROM golang:1.19 AS builder

WORKDIR /app

# 复制go模块文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建应用
RUN CGO_ENABLED=0 GOOS=linux go build -o h3c-snmp-sim -a -ldflags="-w -s" .

# 使用更小的基础镜像
FROM alpine:latest

WORKDIR /app

# 从builder阶段复制编译好的可执行文件
COPY --from=builder /app/h3c-snmp-sim .

# 暴露SNMP端口和指标端口
EXPOSE 161/udp
EXPOSE 9116

# 设置可执行权限并设置健康检查
RUN chmod +x /app/h3c-snmp-sim
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 CMD wget -q -O- http://localhost:9116/health || exit 1

# 运行应用
CMD ["./h3c-snmp-sim"] 