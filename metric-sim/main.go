package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	minUpdateInterval = 30 * time.Second
	maxUpdateInterval = 120 * time.Second
)

var (
	// 定义指标
	cpuUsagePercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metric_sim_cpu_usage_percent",
		Help: "Simulated CPU usage percentage.",
	})
	memoryUsageBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metric_sim_memory_usage_bytes",
		Help: "Simulated memory usage in bytes.",
	})
	memoryUsagePercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metric_sim_memory_usage_percent",
		Help: "Simulated memory usage percentage.",
	})
	diskIOReadBytesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metric_sim_disk_io_read_bytes_total",
		Help: "Simulated total disk IO read in bytes.",
	})
	diskIOWriteBytesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metric_sim_disk_io_write_bytes_total",
		Help: "Simulated total disk IO write in bytes.",
	})
	networkReceiveBytesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metric_sim_network_receive_bytes_total",
		Help: "Simulated total network received bytes.",
	})
	networkTransmitBytesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metric_sim_network_transmit_bytes_total",
		Help: "Simulated total network transmitted bytes.",
	})
	activeConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metric_sim_active_connections",
		Help: "Simulated number of active connections.",
	})
	goroutinesCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metric_sim_goroutines_count",
		Help: "Simulated number of goroutines.",
	})
	processUptimeSeconds = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metric_sim_process_uptime_seconds_total",
		Help: "Simulated process uptime in seconds.",
	})
	httpRequestDurationSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "metric_sim_http_request_duration_seconds",
		Help:    "Simulated HTTP request latencies in seconds.",
		Buckets: prometheus.DefBuckets,
	})
	databaseQueryErrorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metric_sim_database_query_errors_total",
		Help: "Simulated total number of database query errors.",
	})

	// 存储所有指标的列表，方便注册
	metrics []prometheus.Collector
	// 随机数生成器
	rng *rand.Rand
	// 互斥锁保护指标更新
	mu sync.Mutex
)

func init() {
	// 初始化随机数生成器
	source := rand.NewSource(time.Now().UnixNano())
	rng = rand.New(source)

	// 将所有指标添加到列表中
	metrics = []prometheus.Collector{
		cpuUsagePercent,
		memoryUsageBytes,
		memoryUsagePercent,
		diskIOReadBytesTotal,
		diskIOWriteBytesTotal,
		networkReceiveBytesTotal,
		networkTransmitBytesTotal,
		activeConnections,
		goroutinesCount,
		processUptimeSeconds,
		httpRequestDurationSeconds,
		databaseQueryErrorsTotal,
	}

	// 注册所有指标
	for _, metric := range metrics {
		prometheus.MustRegister(metric)
	}
	log.Println("All metrics registered.")
}

// updateMetrics 模拟更新指标值
func updateMetrics() {
	mu.Lock()
	defer mu.Unlock()

	// CPU 使用率 (0-100%)
	cpuUsagePercent.Set(rng.Float64() * 100)

	// 内存使用 (假设总内存 16GB, 0-16GB)
	totalMemoryBytes := 16 * 1024 * 1024 * 1024 // 16GB
	currentMemoryBytes := rng.Float64() * float64(totalMemoryBytes)
	memoryUsageBytes.Set(currentMemoryBytes)
	memoryUsagePercent.Set((currentMemoryBytes / float64(totalMemoryBytes)) * 100)

	// 磁盘 IO (每次增加 0-1MB)
	diskIOReadBytesTotal.Add(rng.Float64() * 1024 * 1024)
	diskIOWriteBytesTotal.Add(rng.Float64() * 1024 * 1024)

	// 网络流量 (每次增加 0-5MB)
	networkReceiveBytesTotal.Add(rng.Float64() * 5 * 1024 * 1024)
	networkTransmitBytesTotal.Add(rng.Float64() * 5 * 1024 * 1024)

	// 活跃连接数 (0-1000)
	activeConnections.Set(rng.Float64() * 1000)

	// Goroutine 数量 (10-500)
	goroutinesCount.Set(10 + rng.Float64()*(500-10))

	// 进程正常运行时间 (每次增加更新间隔)
	// 这个指标由 Prometheus 自动处理或通过其他方式获取更准确，这里仅模拟增加
	// processUptimeSeconds.Add(updateInterval.Seconds()) // 实际应基于真实启动时间

	// HTTP 请求延迟 (模拟一个请求)
	httpRequestDurationSeconds.Observe(rng.Float64() * 0.5) // 0-0.5s

	// 数据库查询错误 (随机增加)
	if rng.Intn(10) == 0 { // 10% 概率出错
		databaseQueryErrorsTotal.Inc()
	}

	log.Println("Metrics updated.")
}

func main() {
	// 启动一个 goroutine 定期更新指标
	go func() {
		// 初始时先更新一次 uptime
		processUptimeSeconds.Add(0) // 标记启动
		startTime := time.Now()

		for {
			// 更新 uptime
			processUptimeSeconds.Add(time.Since(startTime).Seconds())
			startTime = time.Now() // 重置 startTime 以便下次计算增量

			updateMetrics()
			// 计算下一次更新的随机间隔
			interval := minUpdateInterval + time.Duration(rng.Int63n(int64(maxUpdateInterval-minUpdateInterval)))
			log.Printf("Next metrics update in %s", interval)
			time.Sleep(interval)
		}
	}()

	// 设置 HTTP 处理函数
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "<h1>Metric Simulator</h1><p><a href='/metrics'>Metrics</a></p>")
	})

	port := "8080" // 服务监听端口
	log.Printf("Starting metric simulator on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
