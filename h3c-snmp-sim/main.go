package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"math/rand" // 新增导入

	"github.com/gosnmp/gosnmp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	startTime = time.Now()
	version   = "1.0.0"

	// Prometheus 监控指标
	gcCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "snmp_sim_gc_count_total",
		Help: "SNMP模拟器的垃圾回收总次数",
	})

	memoryUsage = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_memory_usage_bytes",
		Help: "SNMP模拟器的内存使用量(字节)",
	})

	goroutineCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_goroutine_count",
		Help: "SNMP模拟器的goroutine数量",
	})

	cpuUsage = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_cpu_usage_percent",
		Help: "SNMP模拟器的CPU使用率(%) - 模拟值", // 强调是模拟值
	})

	uptime = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "snmp_sim_uptime_seconds",
		Help: "SNMP模拟器的运行时间(秒)",
	})

	ifInRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{ // 新增接口输入速率指标
		Name: "snmp_sim_if_in_rate_bytes_per_second",
		Help: "SNMP模拟器接口的输入速率 (字节/秒)",
	}, []string{"ifIndex", "ifDescr"})

	ifOutRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{ // 新增接口输出速率指标
		Name: "snmp_sim_if_out_rate_bytes_per_second",
		Help: "SNMP模拟器接口的输出速率 (字节/秒)",
	}, []string{"ifIndex", "ifDescr"})

	// SNMP OID 和值的映射关系
	oidMapMutex sync.RWMutex
	oidMap      = map[string]gosnmp.SnmpPDU{
		// 系统信息
		".1.3.6.1.2.1.1.1.0": { // sysDescr
			Name:  ".1.3.6.1.2.1.1.1.0",
			Type:  gosnmp.OctetString,
			Value: []byte("H3C S5120-52P-SI Switch Software Version 5.20, Release 1115P01"),
		},
		".1.3.6.1.2.1.1.3.0": { // sysUpTime
			Name:  ".1.3.6.1.2.1.1.3.0",
			Type:  gosnmp.TimeTicks,
			Value: uint32(12345678), // 初始系统运行时间
		},
		".1.3.6.1.2.1.1.5.0": { // sysName
			Name:  ".1.3.6.1.2.1.1.5.0",
			Type:  gosnmp.OctetString,
			Value: []byte("H3C-Switch-01"),
		},
		// 接口数量
		".1.3.6.1.2.1.2.1.0": { // ifNumber
			Name:  ".1.3.6.1.2.1.2.1.0",
			Type:  gosnmp.Integer,
			Value: int32(52),
		},
		// 自定义 OID 用于暴露H3C-SNMP-SIM系统指标
		".1.3.6.1.4.1.2021.13.1.1.0": { // 自定义OID：模拟器版本
			Name:  ".1.3.6.1.4.1.2021.13.1.1.0",
			Type:  gosnmp.OctetString,
			Value: []byte(version),
		},
		".1.3.6.1.4.1.2021.13.1.2.0": { // 自定义OID：Goroutine数量
			Name:  ".1.3.6.1.4.1.2021.13.1.2.0",
			Type:  gosnmp.Integer,
			Value: int32(runtime.NumGoroutine()),
		},
		".1.3.6.1.4.1.2021.13.1.3.0": { // 自定义OID：GC次数
			Name:  ".1.3.6.1.4.1.2021.13.1.3.0",
			Type:  gosnmp.Counter32,
			Value: uint32(0),
		},
		".1.3.6.1.4.1.2021.13.1.4.0": { // 自定义OID：内存使用量(MB)
			Name:  ".1.3.6.1.4.1.2021.13.1.4.0",
			Type:  gosnmp.Integer,
			Value: int32(0),
		},
		".1.3.6.1.4.1.2021.13.1.5.0": { // 自定义OID：运行时间(秒)
			Name:  ".1.3.6.1.4.1.2021.13.1.5.0",
			Type:  gosnmp.TimeTicks,
			Value: uint32(0),
		},
		".1.3.6.1.4.1.2021.13.1.6.0": { // 自定义OID：模拟CPU使用率 (%)
			Name:  ".1.3.6.1.4.1.2021.13.1.6.0",
			Type:  gosnmp.Integer,
			Value: int32(0),
		},
		// 接口速率相关的OID会动态生成，例如：
		// .1.3.6.1.4.1.2021.13.2.ifIndex.1 (ifInRate)
		// .1.3.6.1.4.1.2021.13.2.ifIndex.2 (ifOutRate)
	}

	// 接口表
	interfacesMutex sync.RWMutex
	interfaces      = []struct {
		index          int32
		descr          string
		adminStatus    int32
		operStatus     int32
		inOctets       uint64
		outOctets      uint64
		lastInOctets   uint64    // 用于计算速率
		lastOutOctets  uint64    // 用于计算速率
		lastUpdateTime time.Time // 用于计算速率
	}{
		{1, "GigabitEthernet1/0/1", 1, 1, 10000, 20000, 0, 0, time.Now()},
		{2, "GigabitEthernet1/0/2", 1, 1, 15000, 25000, 0, 0, time.Now()},
		{3, "GigabitEthernet1/0/3", 1, 2, 0, 0, 0, 0, time.Now()},
		{4, "GigabitEthernet1/0/4", 2, 2, 0, 0, 0, 0, time.Now()},
		{5, "Ten-GigabitEthernet1/0/1", 1, 1, 100000, 200000, 0, 0, time.Now()},
	}
)

func init() {
	// 注册prometheus指标
	prometheus.MustRegister(gcCount)
	prometheus.MustRegister(memoryUsage)
	prometheus.MustRegister(goroutineCount)
	prometheus.MustRegister(cpuUsage)
	prometheus.MustRegister(uptime)
	prometheus.MustRegister(ifInRate)  // 注册新指标
	prometheus.MustRegister(ifOutRate) // 注册新指标
	rand.Seed(time.Now().UnixNano())   // 初始化随机数种子
}

func main() {
	log.Println("H3C SNMP模拟器启动，版本:", version)

	// 监控自身指标并更新OID映射
	go monitorSystemMetrics()

	// 增加计数器自增的 goroutine
	go incrementCounters()

	// 获取端口配置，默认为9116
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "9116"
	}

	// 提供HTTP服务以支持Prometheus指标采集
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/status", statusHandler)

	go func() {
		log.Printf("指标服务启动在 :%s/metrics\n", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Fatalf("无法启动HTTP服务: %v", err)
		}
	}()

	log.Println("SNMP 模拟器就绪，现在可以通过 snmp-exporter 收集数据")

	// 保持程序运行
	select {}
}

// 监控自身指标
func monitorSystemMetrics() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var memStats runtime.MemStats
	var lastGCCount uint32 = 0
	// 用于模拟CPU使用率
	simulatedCPU := float64(rand.Intn(30) + 10) // 初始CPU在10-40%

	for currentTime := range ticker.C {
		// 更新GC统计信息
		runtime.ReadMemStats(&memStats)

		// 更新指标
		currentGCCount := memStats.NumGC
		if currentGCCount > lastGCCount {
			gcCount.Add(float64(currentGCCount - lastGCCount))
			lastGCCount = currentGCCount
		}

		memoryUsage.Set(float64(memStats.Alloc))
		goroutineCount.Set(float64(runtime.NumGoroutine()))

		// 模拟CPU使用率波动
		change := float64(rand.Intn(11) - 5) // -5 to +5 % change
		simulatedCPU += change
		if simulatedCPU < 5 {
			simulatedCPU = 5
		} else if simulatedCPU > 85 {
			simulatedCPU = 85
		}
		cpuUsage.Set(simulatedCPU)

		// 运行时间
		uptimeDuration := time.Since(startTime).Seconds()
		uptime.Add(1) // 假设每秒调用一次

		// 更新接口速率
		interfacesMutex.RLock()
		for i, iface := range interfaces {
			if iface.operStatus == 1 { // 只计算启用接口的速率
				timeDiff := currentTime.Sub(iface.lastUpdateTime).Seconds()
				if timeDiff > 0 {
					currentInOctets := interfaces[i].inOctets // 读取最新的累计值
					currentOutOctets := interfaces[i].outOctets

					inRateVal := float64(currentInOctets-iface.lastInOctets) / timeDiff
					outRateVal := float64(currentOutOctets-iface.lastOutOctets) / timeDiff

					ifInRate.WithLabelValues(fmt.Sprintf("%d", iface.index), iface.descr).Set(inRateVal)
					ifOutRate.WithLabelValues(fmt.Sprintf("%d", iface.index), iface.descr).Set(outRateVal)

					// 更新SNMP OID的值 (接口速率)
					// OID 格式: .1.3.6.1.4.1.2021.13.2.<ifIndex>.<metricType>
					// metricType: 1 for inRate, 2 for outRate
					oidMapMutex.Lock()
					oidMap[fmt.Sprintf(".1.3.6.1.4.1.2021.13.2.%d.1", iface.index)] = gosnmp.SnmpPDU{
						Name:  fmt.Sprintf(".1.3.6.1.4.1.2021.13.2.%d.1", iface.index),
						Type:  gosnmp.Gauge32, // 使用Gauge32表示速率
						Value: uint32(inRateVal),
					}
					oidMap[fmt.Sprintf(".1.3.6.1.4.1.2021.13.2.%d.2", iface.index)] = gosnmp.SnmpPDU{
						Name:  fmt.Sprintf(".1.3.6.1.4.1.2021.13.2.%d.2", iface.index),
						Type:  gosnmp.Gauge32,
						Value: uint32(outRateVal),
					}
					oidMapMutex.Unlock()
				}
			}
		}
		interfacesMutex.RUnlock()

		// 更新SNMP OID的值 (系统指标)
		oidMapMutex.Lock()
		oidMap[".1.3.6.1.4.1.2021.13.1.2.0"] = gosnmp.SnmpPDU{
			Name:  ".1.3.6.1.4.1.2021.13.1.2.0",
			Type:  gosnmp.Integer,
			Value: int32(runtime.NumGoroutine()),
		}
		oidMap[".1.3.6.1.4.1.2021.13.1.3.0"] = gosnmp.SnmpPDU{
			Name:  ".1.3.6.1.4.1.2021.13.1.3.0",
			Type:  gosnmp.Counter32,
			Value: uint32(currentGCCount),
		}
		oidMap[".1.3.6.1.4.1.2021.13.1.4.0"] = gosnmp.SnmpPDU{
			Name:  ".1.3.6.1.4.1.2021.13.1.4.0",
			Type:  gosnmp.Integer,
			Value: int32(memStats.Alloc / 1024 / 1024), // 转换为MB
		}
		oidMap[".1.3.6.1.4.1.2021.13.1.5.0"] = gosnmp.SnmpPDU{
			Name:  ".1.3.6.1.4.1.2021.13.1.5.0",
			Type:  gosnmp.TimeTicks,
			Value: uint32(uptimeDuration * 100), // 转换为时间刻度
		}
		oidMap[".1.3.6.1.4.1.2021.13.1.6.0"] = gosnmp.SnmpPDU{ // CPU使用率 OID
			Name:  ".1.3.6.1.4.1.2021.13.1.6.0",
			Type:  gosnmp.Integer,
			Value: int32(simulatedCPU),
		}
		oidMapMutex.Unlock()
	}
}

// 定期增加计数器值
func incrementCounters() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// 更新接口计数器
		currentTime := time.Now()
		interfacesMutex.Lock()
		for i := range interfaces {
			if interfaces[i].operStatus == 1 { // 只有启用的接口才增加计数器
				// 更新上一次的计数值和时间，用于速率计算
				interfaces[i].lastInOctets = interfaces[i].inOctets
				interfaces[i].lastOutOctets = interfaces[i].outOctets
				interfaces[i].lastUpdateTime = currentTime

				interfaces[i].inOctets += uint64(rand.Intn(1000) + 500 + i*100)   // 增加随机性
				interfaces[i].outOctets += uint64(rand.Intn(2000) + 1000 + i*200) // 增加随机性
			}
		}
		interfacesMutex.Unlock()

		// 更新系统运行时间
		oidMapMutex.Lock()
		if pdu, ok := oidMap[".1.3.6.1.2.1.1.3.0"]; ok {
			oidMap[".1.3.6.1.2.1.1.3.0"] = gosnmp.SnmpPDU{
				Name:  pdu.Name,
				Type:  pdu.Type,
				Value: pdu.Value.(uint32) + 500, // 增加系统运行时间
			}
		}
		oidMapMutex.Unlock()
	}
}

// 健康检查接口
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// 状态信息接口
func statusHandler(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	status := map[string]interface{}{
		"version":     version,
		"uptime":      time.Since(startTime).String(),
		"goroutines":  runtime.NumGoroutine(),
		"gc_count":    memStats.NumGC,
		"memory_used": fmt.Sprintf("%.2f MB", float64(memStats.Alloc)/1024/1024),
		"memory_sys":  fmt.Sprintf("%.2f MB", float64(memStats.Sys)/1024/1024),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
