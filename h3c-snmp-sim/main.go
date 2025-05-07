package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// --- 常量定义 ---
const (
	// OID 前缀定义，方便管理和引用
	baseSystemOIDPrefix         = ".1.3.6.1.2.1.1"              // 标准系统组OID前缀
	baseInterfacesOIDPrefix     = ".1.3.6.1.2.1.2"              // 标准接口组OID前缀
	customSimBaseOIDPrefix      = ".1.3.6.1.4.1.2021.13"        // 模拟器自定义指标的根OID (假设2021是私有企业号)
	customSimSystemOIDPrefix    = customSimBaseOIDPrefix + ".1" // 模拟器自身系统状态相关的OID子树
	customSimIfRateOIDPrefix    = customSimBaseOIDPrefix + ".2" // 模拟器接口速率相关的OID子树
	customSimH3CCommonOIDPrefix = customSimBaseOIDPrefix + ".3" // 模拟器H3C常用设备指标相关的OID子树

	// 模拟参数
	simulatedTotalMemoryBytes = 512 * 1024 * 1024 // 假设模拟设备的总内存为 512MB，用于计算内存利用率
	defaultHTTPPort           = "9116"            // HTTP服务监听的默认端口
	metricsUpdateInterval     = 1 * time.Second   // Prometheus指标和SNMP OID值的更新频率
	counterIncrementInterval  = 5 * time.Second   // SNMP计数器类型的值（如流量、错误数）的增长频率
)

// --- 数据结构定义 ---

// interfaceData 存储单个接口的模拟数据
type interfaceData struct {
	index          int32     // 接口索引 (ifIndex)
	descr          string    // 接口描述 (ifDescr)
	adminStatus    int32     // 管理状态 (1: up, 2: down)
	operStatus     int32     // 操作状态 (1: up, 2: down)
	inOctets       uint64    // 入方向累计字节数
	outOctets      uint64    // 出方向累计字节数
	lastInOctets   uint64    // 上次记录的入方向字节数 (用于计算速率)
	lastOutOctets  uint64    // 上次记录的出方向字节数 (用于计算速率)
	lastUpdateTime time.Time // 上次更新这些计数器的时间 (用于计算速率)
	inErrors       uint64    // 入方向累计错误包数
	outDiscards    uint64    // 出方向累计丢包数
}

// fanData 存储单个风扇的模拟数据
type fanData struct {
	index  int32  // 风扇索引
	descr  string // 风扇描述
	status int32  // 风扇状态 (1: normal, 2: failed)
}

// psuData 存储单个电源的模拟数据
type psuData struct {
	index  int32  // 电源索引
	descr  string // 电源描述
	status int32  // 电源状态 (1: normal, 2: failed, 3: not_present)
}

// --- 全局变量 ---
var (
	startTime = time.Now() // 模拟器启动时间，用于计算uptime
	version   = "1.1.0"    // 模拟器版本号 (增加版本号以反映更改)

	// --- Prometheus 监控指标定义 ---
	// gcCount 记录模拟器自身的垃圾回收总次数
	gcCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "snmp_sim_app_gc_count_total", // 添加 "app" 以区分是模拟器自身指标
		Help: "SNMP模拟器应用自身的垃圾回收总次数 (Prometheus 指标)",
	})
	// memoryUsage 记录模拟器自身当前的内存使用量 (字节)
	memoryUsage = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_app_memory_usage_bytes",
		Help: "SNMP模拟器应用自身的内存使用量(字节) (Prometheus 指标)",
	})
	// goroutineCount 记录模拟器自身当前的goroutine数量
	goroutineCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_app_goroutine_count",
		Help: "SNMP模拟器应用自身的goroutine数量 (Prometheus 指标)",
	})
	// cpuUsage 记录模拟的设备CPU使用率 (%)
	cpuUsage = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_device_cpu_usage_percent", // 更明确的指标名称
		Help: "SNMP模拟器模拟的设备CPU使用率(%) (Prometheus 指标)",
	})
	// uptime 记录模拟器自身的运行时间 (秒)
	uptime = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "snmp_sim_app_uptime_seconds_total",
		Help: "SNMP模拟器应用自身的运行时间(秒) (Prometheus 指标)",
	})

	// --- 模拟设备指标 (通过 Prometheus 直接暴露) ---
	// ifInRate 记录模拟接口的输入速率 (字节/秒)
	ifInRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "snmp_sim_device_if_in_rate_bytes_per_second",
		Help: "SNMP模拟器模拟的设备接口输入速率 (字节/秒) (Prometheus 指标)",
	}, []string{"ifIndex", "ifDescr"})
	// ifOutRate 记录模拟接口的输出速率 (字节/秒)
	ifOutRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "snmp_sim_device_if_out_rate_bytes_per_second",
		Help: "SNMP模拟器模拟的设备接口输出速率 (字节/秒) (Prometheus 指标)",
	}, []string{"ifIndex", "ifDescr"})
	// simMemoryUtilPercent 记录模拟的设备内存利用率 (%)
	simMemoryUtilPercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_device_memory_utilization_percent",
		Help: "SNMP模拟器模拟的设备内存利用率 (%) (Prometheus 指标)",
	})
	// simDeviceTemperatureCelsius 记录模拟的设备温度 (摄氏度)
	simDeviceTemperatureCelsius = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_device_temperature_celsius",
		Help: "SNMP模拟器模拟的设备温度 (摄氏度) (Prometheus 指标)",
	})
	// simFanStatus 记录模拟风扇的状态
	simFanStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "snmp_sim_device_fan_status",
		Help: "SNMP模拟器模拟的设备风扇状态 (1=normal, 2=failed) (Prometheus 指标)",
	}, []string{"fanIndex", "fanDescr"})
	// simPsuStatus 记录模拟电源的状态
	simPsuStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "snmp_sim_device_psu_status",
		Help: "SNMP模拟器模拟的设备电源状态 (1=normal, 2=failed, 3=not_present) (Prometheus 指标)",
	}, []string{"psuIndex", "psuDescr"})
	// simIfInErrorsTotal 记录模拟接口入方向错误包总数
	simIfInErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "snmp_sim_device_if_in_errors_total",
		Help: "SNMP模拟器模拟的设备接口入方向错误包总数 (Prometheus 指标)",
	}, []string{"ifIndex", "ifDescr"})
	// simIfOutDiscardsTotal 记录模拟接口出方向丢包总数
	simIfOutDiscardsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "snmp_sim_device_if_out_discards_total",
		Help: "SNMP模拟器模拟的设备接口出方向丢包总数 (Prometheus 指标)",
	}, []string{"ifIndex", "ifDescr"})
	// simArpCacheEntries 记录模拟的ARP缓存条目数
	simArpCacheEntries = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_device_arp_cache_entries",
		Help: "SNMP模拟器模拟的设备ARP缓存条目数 (Prometheus 指标)",
	})
	// simMacTableEntries 记录模拟的MAC地址表条目数
	simMacTableEntries = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_device_mac_table_entries",
		Help: "SNMP模拟器模拟的设备MAC地址表条目数 (Prometheus 指标)",
	})
	// simActiveTcpConnections 记录模拟的活动TCP连接数
	simActiveTcpConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snmp_sim_device_active_tcp_connections",
		Help: "SNMP模拟器模拟的设备活动TCP连接数 (Prometheus 指标)",
	})

	// --- SNMP OID 模拟数据存储 ---
	oidMapMutex sync.RWMutex // oidMap 的读写锁
	// oidMap 存储 SNMP OID 到其对应 PDU (协议数据单元) 的映射
	// 这里主要存放会被SNMP GET请求查询的OID值
	oidMap = map[string]gosnmp.SnmpPDU{
		// 标准系统信息 OIDs
		fmt.Sprintf("%s.1.0", baseSystemOIDPrefix): { // sysDescr
			Name:  fmt.Sprintf("%s.1.0", baseSystemOIDPrefix),
			Type:  gosnmp.OctetString,
			Value: []byte("H3C S5120-52P-SI Switch Software Version 5.20, Release 1115P01 (Simulated)"),
		},
		fmt.Sprintf("%s.3.0", baseSystemOIDPrefix): { // sysUpTime
			Name:  fmt.Sprintf("%s.3.0", baseSystemOIDPrefix),
			Type:  gosnmp.TimeTicks,
			Value: uint32(rand.Intn(1000000) + 12345000), // 随机化初始运行时间
		},
		fmt.Sprintf("%s.5.0", baseSystemOIDPrefix): { // sysName
			Name:  fmt.Sprintf("%s.5.0", baseSystemOIDPrefix),
			Type:  gosnmp.OctetString,
			Value: []byte("H3C-Simulated-Switch-01"),
		},
		// 标准接口数量 OID
		fmt.Sprintf("%s.1.0", baseInterfacesOIDPrefix): { // ifNumber
			Name:  fmt.Sprintf("%s.1.0", baseInterfacesOIDPrefix),
			Type:  gosnmp.Integer,
			Value: int32(len(interfaces)), // 根据定义的接口数量动态设置
		},
		// 模拟器自身状态相关的自定义 OIDs (子树 .1.3.6.1.4.1.2021.13.1)
		fmt.Sprintf("%s.1.0", customSimSystemOIDPrefix): { // simVersion
			Name:  fmt.Sprintf("%s.1.0", customSimSystemOIDPrefix),
			Type:  gosnmp.OctetString,
			Value: []byte(version),
		},
		fmt.Sprintf("%s.2.0", customSimSystemOIDPrefix): { // simGoroutines
			Name:  fmt.Sprintf("%s.2.0", customSimSystemOIDPrefix),
			Type:  gosnmp.Gauge32, // Gauge32 更适合表示当前数量
			Value: uint32(runtime.NumGoroutine()),
		},
		fmt.Sprintf("%s.3.0", customSimSystemOIDPrefix): { // simGcCount
			Name:  fmt.Sprintf("%s.3.0", customSimSystemOIDPrefix),
			Type:  gosnmp.Counter32,
			Value: uint32(0), // 初始化为0, monitorSystemMetrics中更新
		},
		fmt.Sprintf("%s.4.0", customSimSystemOIDPrefix): { // simMemoryMB (模拟器自身内存)
			Name:  fmt.Sprintf("%s.4.0", customSimSystemOIDPrefix),
			Type:  gosnmp.Gauge32,
			Value: uint32(0), // 初始化为0, monitorSystemMetrics中更新
		},
		fmt.Sprintf("%s.5.0", customSimSystemOIDPrefix): { // simUptimeSec (模拟器自身运行时间)
			Name:  fmt.Sprintf("%s.5.0", customSimSystemOIDPrefix),
			Type:  gosnmp.TimeTicks, // TimeTicks in 1/100ths of a second
			Value: uint32(0),        // 初始化为0, monitorSystemMetrics中更新
		},
		// 模拟设备CPU利用率 (复用原OID，但归类到H3C常用指标子树下，实际OID为.1.3.6.1.4.1.2021.13.1.6.0)
		// 这个OID在snmp.yml中会被映射到 simCpuUtilPercent
		fmt.Sprintf("%s.6.0", customSimSystemOIDPrefix): { // simDeviceCpuUsagePercent
			Name:  fmt.Sprintf("%s.6.0", customSimSystemOIDPrefix),
			Type:  gosnmp.Gauge32,
			Value: uint32(0), // 初始化为0, monitorSystemMetrics中更新
		},

		// H3C常用模拟指标的标量部分 (子树 .1.3.6.1.4.1.2021.13.3)
		// 注意：simCpuUtilPercent 的 SNMP OID 使用的是上面 customSimSystemOIDPrefix + ".6.0"
		fmt.Sprintf("%s.2.0", customSimH3CCommonOIDPrefix): { // simMemoryUtilPercent
			Name:  fmt.Sprintf("%s.2.0", customSimH3CCommonOIDPrefix),
			Type:  gosnmp.Gauge32, // 通常百分比用Gauge
			Value: uint32(0),
		},
		fmt.Sprintf("%s.3.0", customSimH3CCommonOIDPrefix): { // simDeviceTemperatureCelsius
			Name:  fmt.Sprintf("%s.3.0", customSimH3CCommonOIDPrefix),
			Type:  gosnmp.Integer,            // 温度可以是Integer
			Value: int32(rand.Intn(40) + 25), // 初始温度 25-64
		},
		// Fan, PSU, IfErrors, IfDiscards 的 OID 是表格型, 会在 monitorSystemMetrics 中动态填充和更新
		// 例如 .1.3.6.1.4.1.2021.13.3.4.1.fanIndex
		// 例如 .1.3.6.1.4.1.2021.13.3.6.1.ifIndex
		fmt.Sprintf("%s.8.0", customSimH3CCommonOIDPrefix): { // simArpCacheEntries
			Name:  fmt.Sprintf("%s.8.0", customSimH3CCommonOIDPrefix),
			Type:  gosnmp.Gauge32,
			Value: uint32(rand.Intn(200) + 50), // 初始 50-249
		},
		fmt.Sprintf("%s.9.0", customSimH3CCommonOIDPrefix): { // simMacTableEntries
			Name:  fmt.Sprintf("%s.9.0", customSimH3CCommonOIDPrefix),
			Type:  gosnmp.Gauge32,
			Value: uint32(rand.Intn(500) + 100), // 初始 100-599
		},
		fmt.Sprintf("%s.10.0", customSimH3CCommonOIDPrefix): { // simActiveTcpConnections
			Name:  fmt.Sprintf("%s.10.0", customSimH3CCommonOIDPrefix),
			Type:  gosnmp.Gauge32,
			Value: uint32(rand.Intn(100) + 20), // 初始 20-119
		},
	}

	// --- 模拟设备状态数据 ---
	// interfaces 存储模拟接口的信息
	interfacesMutex sync.RWMutex // interfaces 的读写锁
	interfaces      = []interfaceData{
		{index: 1, descr: "GigabitEthernet1/0/1", adminStatus: 1, operStatus: 1, inOctets: uint64(rand.Intn(100000)), outOctets: uint64(rand.Intn(200000)), lastUpdateTime: time.Now()},
		{index: 2, descr: "GigabitEthernet1/0/2", adminStatus: 1, operStatus: 1, inOctets: uint64(rand.Intn(150000)), outOctets: uint64(rand.Intn(250000)), lastUpdateTime: time.Now()},
		{index: 3, descr: "GigabitEthernet1/0/3", adminStatus: 1, operStatus: 2, lastUpdateTime: time.Now()}, // 模拟 operStatus down
		{index: 4, descr: "GigabitEthernet1/0/4", adminStatus: 2, operStatus: 2, lastUpdateTime: time.Now()}, // 模拟 adminStatus down
		{index: 5, descr: "Ten-GigabitEthernet1/0/1", adminStatus: 1, operStatus: 1, inOctets: uint64(rand.Intn(1000000)), outOctets: uint64(rand.Intn(2000000)), lastUpdateTime: time.Now()},
	}

	// fans 存储模拟风扇的信息
	fanMutex sync.RWMutex // fans 的读写锁
	fans     = []fanData{
		{index: 1, descr: "FAN1_SLOT1", status: 1}, // normal
		{index: 2, descr: "FAN2_SLOT1", status: 1}, // normal
	}

	// psus 存储模拟电源的信息
	psuMutex sync.RWMutex // psus 的读写锁
	psus     = []psuData{
		{index: 1, descr: "PSU1_SLOT1", status: 1}, // normal
		{index: 2, descr: "PSU2_SLOT1", status: 2}, // 模拟一个电源故障
	}
)

// init 函数在 main 函数执行前被调用，用于初始化操作，例如注册Prometheus指标和设置随机数种子
func init() {
	// 注册 Prometheus 指标
	prometheus.MustRegister(gcCount)
	prometheus.MustRegister(memoryUsage)
	prometheus.MustRegister(goroutineCount)
	prometheus.MustRegister(cpuUsage) // 用于 snmp_sim_device_cpu_usage_percent
	prometheus.MustRegister(uptime)

	prometheus.MustRegister(ifInRate)
	prometheus.MustRegister(ifOutRate)
	prometheus.MustRegister(simMemoryUtilPercent)
	prometheus.MustRegister(simDeviceTemperatureCelsius)
	prometheus.MustRegister(simFanStatus)
	prometheus.MustRegister(simPsuStatus)
	prometheus.MustRegister(simIfInErrorsTotal)
	prometheus.MustRegister(simIfOutDiscardsTotal)
	prometheus.MustRegister(simArpCacheEntries)
	prometheus.MustRegister(simMacTableEntries)
	prometheus.MustRegister(simActiveTcpConnections)

	rand.Seed(time.Now().UnixNano()) // 初始化随机数种子，确保每次运行模拟值有所不同
	log.Println("SNMP模拟器初始化完成，Prometheus指标已注册。")
}

// main 函数是程序的入口点
func main() {
	log.Printf("H3C SNMP模拟器启动中... 版本: %s\n", version)

	// 启动一个goroutine来定期监控和更新模拟器的自身指标以及SNMP OID映射值
	go monitorSystemMetrics()

	// 启动一个goroutine来定期增加各种计数器的值 (例如接口流量、错误数)
	go incrementCounters()

	// 从环境变量获取HTTP服务端口，如果未设置则使用默认端口
	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = defaultHTTPPort
	}

	// 设置HTTP路由
	// /metrics 端点暴露Prometheus格式的指标
	http.Handle("/metrics", promhttp.Handler())
	// /health 端点用于健康检查
	http.HandleFunc("/health", healthHandler)
	// /status 端点提供模拟器的简要状态信息
	http.HandleFunc("/status", statusHandler)

	// 启动HTTP服务在一个单独的goroutine中，以避免阻塞主线程
	go func() {
		log.Printf("HTTP指标服务已启动，监听端口: %s，访问路径: /metrics, /health, /status\n", httpPort)
		if err := http.ListenAndServe(":"+httpPort, nil); err != nil {
			log.Fatalf("错误：无法启动HTTP服务: %v\n", err)
		}
	}()

	log.Println("SNMP模拟器核心服务已准备就绪。等待SNMP请求或Prometheus抓取...")

	// 使用一个空的select语句来永久阻塞主goroutine，保持程序运行
	// 直到程序被外部信号中断 (例如 Ctrl+C)
	select {}
}

// monitorSystemMetrics 定期更新模拟器自身的监控指标和SNMP OID映射中的值
// 这个函数在一个单独的goroutine中运行
func monitorSystemMetrics() {
	ticker := time.NewTicker(metricsUpdateInterval) // 定义更新频率
	defer ticker.Stop()                             // 确保ticker在函数退出时停止

	var memStats runtime.MemStats // 用于获取内存统计信息
	var lastGCCount uint32 = 0    // 上一次记录的GC次数，用于计算增量

	// 初始化模拟的CPU使用率 (10-39%)
	simulatedDeviceCPU := float64(rand.Intn(30) + 10)
	// 初始化模拟的设备温度 (25-65 C)
	simulatedDeviceTemp := float64(rand.Intn(41) + 25)

	for currentTime := range ticker.C { // 每隔 metricsUpdateInterval 执行一次
		// --- 1. 更新模拟器应用自身的监控指标 ---
		runtime.ReadMemStats(&memStats) // 获取当前内存统计

		currentAppGCCount := memStats.NumGC
		if currentAppGCCount > lastGCCount {
			gcCount.Add(float64(currentAppGCCount - lastGCCount)) // 更新GC次数Prometheus指标
			lastGCCount = currentAppGCCount
		}

		memoryUsage.Set(float64(memStats.Alloc))            // 更新内存使用量Prometheus指标
		goroutineCount.Set(float64(runtime.NumGoroutine())) // 更新Goroutine数量Prometheus指标
		uptime.Inc()                                        // uptime Prometheus计数器自增 (因为ticker是1秒，所以每次加1秒)

		// --- 2. 更新模拟的设备指标 (Prometheus Gauge/Counter 和 SNMP OID) ---

		// 模拟设备CPU使用率波动
		cpuChange := float64(rand.Intn(11) - 5) // 随机增减 -5% 到 +5%
		simulatedDeviceCPU += cpuChange
		if simulatedDeviceCPU < 5 {
			simulatedDeviceCPU = 5 // 最低5%
		} else if simulatedDeviceCPU > 90 { // 最高90%
			simulatedDeviceCPU = 90
		}
		cpuUsage.Set(simulatedDeviceCPU) // 更新CPU使用率Prometheus指标

		// 模拟设备内存利用率
		currentDeviceMemoryUtil := (float64(memStats.Alloc) / simulatedTotalMemoryBytes) * 100 // 简单用模拟器内存模拟设备内存利用率
		if currentDeviceMemoryUtil > 95 {
			currentDeviceMemoryUtil = 95
		} else if currentDeviceMemoryUtil < 10 {
			currentDeviceMemoryUtil = 10
		}
		simMemoryUtilPercent.Set(currentDeviceMemoryUtil)

		// 模拟设备温度波动
		tempChange := float64(rand.Intn(5) - 2) // 随机增减 -2 到 +2 度
		simulatedDeviceTemp += tempChange
		if simulatedDeviceTemp < 20 {
			simulatedDeviceTemp = 20 // 最低20度
		} else if simulatedDeviceTemp > 75 { // 最高75度
			simulatedDeviceTemp = 75
		}
		simDeviceTemperatureCelsius.Set(simulatedDeviceTemp)

		// 更新模拟接口的速率、错误和丢包 (Prometheus指标已在incrementCounters中处理或通过GaugeSet更新)
		interfacesMutex.RLock()
		for i, iface := range interfaces {
			if iface.operStatus == 1 { // 只处理操作状态为up的接口
				timeDiffSeconds := currentTime.Sub(iface.lastUpdateTime).Seconds()
				if timeDiffSeconds > 0 {
					// 读取当前累计值 (这些值在incrementCounters中更新)
					currentInOctets := interfaces[i].inOctets
					currentOutOctets := interfaces[i].outOctets
					// currentInErrors := interfaces[i].inErrors       // Prometheus CounterVec在incrementCounters中更新
					// currentOutDiscards := interfaces[i].outDiscards // Prometheus CounterVec在incrementCounters中更新

					inRateBps := float64(currentInOctets-iface.lastInOctets) / timeDiffSeconds
					outRateBps := float64(currentOutOctets-iface.lastOutOctets) / timeDiffSeconds

					ifInRate.WithLabelValues(fmt.Sprintf("%d", iface.index), iface.descr).Set(inRateBps)
					ifOutRate.WithLabelValues(fmt.Sprintf("%d", iface.index), iface.descr).Set(outRateBps)
					// simIfInErrorsTotal 和 simIfOutDiscardsTotal 是 Counter, 在 incrementCounters 中 .Add()

					// 更新SNMP OID映射 (接口速率、错误、丢包)
					oidMapMutex.Lock()
					oidMap[fmt.Sprintf("%s.%d.1", customSimIfRateOIDPrefix, iface.index)] = gosnmp.SnmpPDU{ // ifInRate
						Name:  fmt.Sprintf("%s.%d.1", customSimIfRateOIDPrefix, iface.index),
						Type:  gosnmp.Gauge32,
						Value: uint32(inRateBps),
					}
					oidMap[fmt.Sprintf("%s.%d.2", customSimIfRateOIDPrefix, iface.index)] = gosnmp.SnmpPDU{ // ifOutRate
						Name:  fmt.Sprintf("%s.%d.2", customSimIfRateOIDPrefix, iface.index),
						Type:  gosnmp.Gauge32,
						Value: uint32(outRateBps),
					}
					// OID for ifInErrors: .1.3.6.1.4.1.2021.13.3.6.1.ifIndex
					oidMap[fmt.Sprintf("%s.6.1.%d", customSimH3CCommonOIDPrefix, iface.index)] = gosnmp.SnmpPDU{
						Name:  fmt.Sprintf("%s.6.1.%d", customSimH3CCommonOIDPrefix, iface.index),
						Type:  gosnmp.Counter32,
						Value: uint32(interfaces[i].inErrors), // 使用当前累计值
					}
					// OID for ifOutDiscards: .1.3.6.1.4.1.2021.13.3.7.1.ifIndex
					oidMap[fmt.Sprintf("%s.7.1.%d", customSimH3CCommonOIDPrefix, iface.index)] = gosnmp.SnmpPDU{
						Name:  fmt.Sprintf("%s.7.1.%d", customSimH3CCommonOIDPrefix, iface.index),
						Type:  gosnmp.Counter32,
						Value: uint32(interfaces[i].outDiscards), // 使用当前累计值
					}
					oidMapMutex.Unlock()
				}
			}
		}
		interfacesMutex.RUnlock()

		// 更新模拟风扇状态 (Prometheus指标和SNMP OID)
		fanMutex.Lock() // 使用写锁，因为可能会修改状态
		for i := range fans {
			// 随机模拟风扇状态变化 (小概率)
			if rand.Intn(200) < 1 { // 0.5% 的概率改变状态
				if fans[i].status == 1 {
					fans[i].status = 2 // normal -> failed
					log.Printf("模拟器事件: 风扇 %s (索引 %d) 状态变更为: failed\n", fans[i].descr, fans[i].index)
				} else {
					fans[i].status = 1 // failed -> normal
					log.Printf("模拟器事件: 风扇 %s (索引 %d) 状态变更为: normal\n", fans[i].descr, fans[i].index)
				}
			}
			simFanStatus.WithLabelValues(fmt.Sprintf("%d", fans[i].index), fans[i].descr).Set(float64(fans[i].status))
			// OID for fanStatus: .1.3.6.1.4.1.2021.13.3.4.1.fanIndex
			oidMapMutex.Lock()
			oidMap[fmt.Sprintf("%s.4.1.%d", customSimH3CCommonOIDPrefix, fans[i].index)] = gosnmp.SnmpPDU{
				Name:  fmt.Sprintf("%s.4.1.%d", customSimH3CCommonOIDPrefix, fans[i].index),
				Type:  gosnmp.Integer, // 状态通常是Integer
				Value: fans[i].status,
			}
			oidMapMutex.Unlock()
		}
		fanMutex.Unlock()

		// 更新模拟电源状态 (Prometheus指标和SNMP OID) - 电源状态变化概率更低
		psuMutex.Lock()
		for i := range psus {
			if psus[i].descr != "PSU2_SLOT1" && rand.Intn(500) < 1 { // 0.2% 概率改变状态 (排除预设故障的PSU2)
				if psus[i].status == 1 {
					psus[i].status = 2
					log.Printf("模拟器事件: 电源 %s (索引 %d) 状态变更为: failed\n", psus[i].descr, psus[i].index)
				} else if psus[i].status == 2 {
					psus[i].status = 1
					log.Printf("模拟器事件: 电源 %s (索引 %d) 状态变更为: normal\n", psus[i].descr, psus[i].index)
				}
			}
			simPsuStatus.WithLabelValues(fmt.Sprintf("%d", psus[i].index), psus[i].descr).Set(float64(psus[i].status))
			// OID for psuStatus: .1.3.6.1.4.1.2021.13.3.5.1.psuIndex
			oidMapMutex.Lock()
			oidMap[fmt.Sprintf("%s.5.1.%d", customSimH3CCommonOIDPrefix, psus[i].index)] = gosnmp.SnmpPDU{
				Name:  fmt.Sprintf("%s.5.1.%d", customSimH3CCommonOIDPrefix, psus[i].index),
				Type:  gosnmp.Integer,
				Value: psus[i].status,
			}
			oidMapMutex.Unlock()
		}
		psuMutex.Unlock()

		// 模拟其他标量设备指标 (ARP, MAC, TCP连接数)
		arpEntriesVal := uint32(rand.Intn(450) + 50)     // 50-499
		macEntriesVal := uint32(rand.Intn(900) + 100)    // 100-999
		tcpConnectionsVal := uint32(rand.Intn(280) + 20) // 20-299

		simArpCacheEntries.Set(float64(arpEntriesVal))
		simMacTableEntries.Set(float64(macEntriesVal))
		simActiveTcpConnections.Set(float64(tcpConnectionsVal))

		// --- 3. 更新SNMP OID映射表中的值 ---
		oidMapMutex.Lock() // 加锁以安全更新oidMap

		// 更新模拟器自身状态相关的SNMP OIDs (使用 get-modify-put 模式)
		updateOidValue := func(oid string, value interface{}) {
			if pdu, ok := oidMap[oid]; ok {
				// 注意: 需要根据 PDU 定义的类型进行类型断言或转换
				switch pdu.Type {
				case gosnmp.Gauge32, gosnmp.Counter32, gosnmp.TimeTicks:
					if v, ok := value.(uint32); ok {
						pdu.Value = v
						oidMap[oid] = pdu
					} else {
						log.Printf("警告: 更新 OID %s 时类型不匹配 (期望 uint32, 得到 %T)\n", oid, value)
					}
				case gosnmp.Integer:
					if v, ok := value.(int32); ok {
						pdu.Value = v
						oidMap[oid] = pdu
					} else {
						log.Printf("警告: 更新 OID %s 时类型不匹配 (期望 int32, 得到 %T)\n", oid, value)
					}
				// 可以根据需要添加其他类型处理
				default:
					log.Printf("警告: 更新 OID %s 时遇到未处理的类型 %v\n", oid, pdu.Type)
				}
			} else {
				// log.Printf("警告: 尝试更新不存在的 OID: %s\n", oid)
				// 对于动态生成的OID（如接口、风扇、电源），它们在首次计算时已添加到map中，
				// 这里主要处理在oidMap中预定义的标量OID。
			}
		}

		updateOidValue(fmt.Sprintf("%s.2.0", customSimSystemOIDPrefix), uint32(runtime.NumGoroutine()))
		updateOidValue(fmt.Sprintf("%s.3.0", customSimSystemOIDPrefix), uint32(currentAppGCCount))
		updateOidValue(fmt.Sprintf("%s.4.0", customSimSystemOIDPrefix), uint32(memStats.Alloc/1024/1024)) // MB
		currentSimUptimeTicks := uint32(time.Since(startTime).Seconds() * 100)                            // TimeTicks (1/100s)
		updateOidValue(fmt.Sprintf("%s.5.0", customSimSystemOIDPrefix), currentSimUptimeTicks)

		// 更新模拟设备状态相关的SNMP OIDs (标量部分)
		updateOidValue(fmt.Sprintf("%s.6.0", customSimSystemOIDPrefix), uint32(simulatedDeviceCPU)) // simDeviceCpuUsagePercent
		updateOidValue(fmt.Sprintf("%s.2.0", customSimH3CCommonOIDPrefix), uint32(currentDeviceMemoryUtil))
		updateOidValue(fmt.Sprintf("%s.3.0", customSimH3CCommonOIDPrefix), int32(simulatedDeviceTemp)) // 温度是Integer
		updateOidValue(fmt.Sprintf("%s.8.0", customSimH3CCommonOIDPrefix), arpEntriesVal)
		updateOidValue(fmt.Sprintf("%s.9.0", customSimH3CCommonOIDPrefix), macEntriesVal)
		updateOidValue(fmt.Sprintf("%s.10.0", customSimH3CCommonOIDPrefix), tcpConnectionsVal)

		oidMapMutex.Unlock() // 解锁
	}
}

// incrementCounters 定期增加模拟的计数器值 (如接口流量、错误数等)
// 这个函数在一个单独的goroutine中运行
func incrementCounters() {
	ticker := time.NewTicker(counterIncrementInterval) // 定义计数器增长频率
	defer ticker.Stop()

	for range ticker.C {
		currentTime := time.Now()
		interfacesMutex.Lock() // 加锁以安全更新接口数据
		for i := range interfaces {
			if interfaces[i].operStatus == 1 { // 只更新操作状态为up的接口
				// 更新上一次记录的字节数和时间，用于下一次速率计算
				// 这些值在 monitorSystemMetrics 中用于计算速率，所以在这里更新它们作为“当前”值
				interfaces[i].lastInOctets = interfaces[i].inOctets
				interfaces[i].lastOutOctets = interfaces[i].outOctets
				interfaces[i].lastUpdateTime = currentTime

				// 模拟增加接口流量 (随机增加)
				interfaces[i].inOctets += uint64(rand.Intn(20000) + 10000 + i*1000) // 增加更多流量使速率明显
				interfaces[i].outOctets += uint64(rand.Intn(30000) + 15000 + i*2000)

				// 模拟随机增加错误和丢包
				if rand.Intn(100) < 2 { // 2% 的概率在每个周期为活动接口增加错误/丢包
					newInErrors := uint64(rand.Intn(3) + 1) // 每次增加1-3个错误
					interfaces[i].inErrors += newInErrors
					simIfInErrorsTotal.WithLabelValues(fmt.Sprintf("%d", interfaces[i].index), interfaces[i].descr).Add(float64(newInErrors))
					// log.Printf("模拟器: 接口 %s (索引 %d) 增加 %d 个入方向错误, 总计: %d\n", interfaces[i].descr, interfaces[i].index, newInErrors, interfaces[i].inErrors)

					newOutDiscards := uint64(rand.Intn(2) + 1) // 每次增加1-2个丢包
					interfaces[i].outDiscards += newOutDiscards
					simIfOutDiscardsTotal.WithLabelValues(fmt.Sprintf("%d", interfaces[i].index), interfaces[i].descr).Add(float64(newOutDiscards))
					// log.Printf("模拟器: 接口 %s (索引 %d) 增加 %d 个出方向丢包, 总计: %d\n", interfaces[i].descr, interfaces[i].index, newOutDiscards, interfaces[i].outDiscards)
				}
			}
		}
		interfacesMutex.Unlock() // 解锁

		// 更新SNMP sysUpTime (标准OID)
		oidMapMutex.Lock()
		sysUpTimeOID := fmt.Sprintf("%s.3.0", baseSystemOIDPrefix)
		if pdu, ok := oidMap[sysUpTimeOID]; ok {
			currentSysUpTimeTicks := pdu.Value.(uint32)
			// counterIncrementInterval是time.Duration, 需要转换为秒再乘以100得到TimeTicks的增量
			incrementTicks := uint32(counterIncrementInterval.Seconds() * 100)
			oidMap[sysUpTimeOID] = gosnmp.SnmpPDU{
				Name:  pdu.Name,
				Type:  pdu.Type,
				Value: currentSysUpTimeTicks + incrementTicks,
			}
		}
		oidMapMutex.Unlock()
	}
}

// healthHandler 提供HTTP健康检查端点
// 返回HTTP 200 OK表示服务健康
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok")) // 忽略写入错误，简单返回"ok"
}

// statusHandler 提供模拟器自身状态信息的HTTP端点 (JSON格式)
func statusHandler(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats) // 获取当前内存统计

	// 构建状态信息map
	status := map[string]interface{}{
		"simulator_version":          version,
		"application_uptime_seconds": time.Since(startTime).Seconds(),
		"active_goroutines":          runtime.NumGoroutine(),
		"total_gc_runs":              memStats.NumGC,
		"memory_allocated_mb":        fmt.Sprintf("%.2f", float64(memStats.Alloc)/1024/1024),
		"total_memory_obtained_mb":   fmt.Sprintf("%.2f", float64(memStats.Sys)/1024/1024),
		"last_gc_timestamp":          time.Unix(0, int64(memStats.LastGC)).Format(time.RFC3339),
		"simulated_interfaces_count": len(interfaces),
		"simulated_fans_count":       len(fans),
		"simulated_psus_count":       len(psus),
	}

	w.Header().Set("Content-Type", "application/json") // 设置响应头为JSON
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("错误: 无法将状态信息编码为JSON: %v\n", err)
		// 如果编码失败，返回HTTP 500错误
		http.Error(w, "无法生成状态信息", http.StatusInternalServerError)
	}
}
