#!/bin/bash

# H3C监控系统精简版启动脚本

echo "==== H3C监控系统精简版 ===="
echo "正在启动监控组件..."

# 确保服务停止
docker-compose down

# 拉取最新镜像
echo "拉取最新的Prometheus, SNMP Exporter 和 Grafana 镜像..."
docker-compose pull prometheus snmp-exporter grafana

# 构建h3c-snmp-sim
echo "构建自定义H3C SNMP模拟器..."
docker-compose build h3c-snmp-sim

# 启动所有服务
echo "启动所有服务..."
docker-compose up -d

# 检查服务状态
echo "检查服务状态..."
docker-compose ps

echo ""
echo "服务已启动，可通过以下地址访问:"
echo "- Prometheus: http://localhost:9090"
echo "- Grafana: http://localhost:3000 (默认用户/密码: admin/admin)"
echo "- H3C SNMP模拟器指标: http://localhost:9117/metrics"
echo "- H3C SNMP模拟器状态: http://localhost:9117/status"
echo ""
echo "如需查看日志，请运行:"
echo "docker-compose logs -f"
echo ""
echo "==== 启动完成 ====" 