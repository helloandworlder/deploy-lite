# H3C监控系统 - 精简版部署

此文件夹包含了H3C网络设备监控系统的精简版部署，主要基于Docker Compose实现，包含以下核心组件：

- **Prometheus**: 时序数据库，用于存储监控指标
- **SNMP Exporter**: 用于将SNMP数据转换为Prometheus格式的指标
- **H3C SNMP Simulator**: 增强版H3C网络设备SNMP模拟器，带有自监控功能

## 主要特点

- 精简部署，只包含核心监控组件
- 优化的H3C SNMP模拟器，增加了自身指标监控
- 使用Docker Compose实现快速部署
- 适合在云服务器上轻量级部署

## 增强版H3C SNMP模拟器

新版的H3C SNMP模拟器基于Go语言开发，主要特点:

1. **自监控功能**：
   - 内存使用监控
   - GC次数和频率监控
   - Goroutine数量监控
   - 运行时间监控
   - CPU使用率估算

2. **双重监控接口**：
   - 通过SNMP协议暴露自身指标（使用自定义OID）
   - 通过HTTP接口提供Prometheus格式的指标

3. **健康检查API**：
   - `/health` - 健康状态检查
   - `/status` - 详细状态信息

## 部署和使用

### 前提条件

- Docker 与 Docker Compose 已安装
- 可以访问互联网下载Docker镜像或已预先下载相关镜像

### 部署步骤

1. 克隆或下载仓库到服务器

```bash
git clone <repository-url>
cd deploy-lite
```

2. 启动所有服务

```bash
docker-compose up -d
```

3. 验证服务状态

```bash
docker-compose ps
```

### 访问服务

- **Prometheus**：http://your-server-ip:9090
- **H3C SNMP模拟器指标**：http://your-server-ip:9117/metrics
- **SNMP模拟器状态**：http://your-server-ip:9117/status

## 系统架构

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│    Prometheus   │     │   SNMP Exporter  │     │  H3C SNMP Sim   │
│                 │◄────┤                 │◄────┤                 │
│ (指标存储和可视化) │     │ (SNMP to Prometheus)│     │  (设备模拟器)   │
└─────────────────┘     └─────────────────┘     └─────────────────┘
```

## 定制和扩展

- 修改`conf/prometheus/prometheus.yml`可以添加更多监控目标
- 修改`conf/snmp-exporter/snmp.yml`可以调整SNMP监控项
- `h3c-snmp-sim`目录下的代码可以进一步优化和扩展

## 故障排除

如果遇到问题，可以查看各服务的日志：

```bash
docker-compose logs prometheus
docker-compose logs snmp-exporter
docker-compose logs h3c-snmp-sim
``` 