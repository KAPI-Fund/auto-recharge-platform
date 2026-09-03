package httpapi

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

const systemMetricsCacheTTL = 3 * time.Second

var systemMetricsCache struct {
	sync.Mutex
	at   time.Time
	data map[string]any
}

// collectSystemMetrics follows the original Node dashboard's host-level
// metric definitions and keeps the same three-second refresh window.
func collectSystemMetrics() map[string]any {
	systemMetricsCache.Lock()
	defer systemMetricsCache.Unlock()
	if systemMetricsCache.data != nil && time.Since(systemMetricsCache.at) < systemMetricsCacheTTL {
		return systemMetricsCache.data
	}

	cpuCount := runtime.NumCPU()
	// Node's os.cpus().length reports logical CPUs, so use the same gopsutil mode.
	if count, err := cpu.Counts(true); err == nil && count > 0 {
		cpuCount = count
	}
	if cpuCount < 1 {
		cpuCount = 1
	}
	load1 := 0.0
	if average, err := load.Avg(); err == nil && average != nil {
		load1 = math.Max(0, average.Load1)
	}
	cpuPercent := clampSystemPercent(math.Round(load1 / float64(cpuCount) * 100))

	memoryPercent := 0
	memoryText := "0.0G/0.0G"
	if memory, err := mem.VirtualMemory(); err == nil && memory.Total > 0 {
		used := memory.Total - minUint64(memory.Free, memory.Total)
		memoryPercent = clampSystemPercent(math.Round(float64(used) / float64(memory.Total) * 100))
		memoryText = fmt.Sprintf("%s/%s", formatSystemBytes(used), formatSystemBytes(memory.Total))
	}

	diskPercent := 0
	diskUsedText := "0.0G"
	diskTotalText := "0.0G"
	if usage, err := disk.Usage("/"); err == nil && usage.Total > 0 {
		diskPercent = clampSystemPercent(math.Round(usage.UsedPercent))
		diskUsedText = formatSystemBytes(usage.Used)
		diskTotalText = formatSystemBytes(usage.Total)
	}

	uptime := uint64(0)
	if value, err := host.Uptime(); err == nil {
		uptime = value
	}
	data := map[string]any{
		"cpu": map[string]any{
			"percent": cpuPercent,
			"text":    fmt.Sprintf("负载 %.2f / %d 核", load1, cpuCount),
		},
		"memory": map[string]any{
			"percent": memoryPercent,
			"text":    memoryText,
		},
		"disk": map[string]any{
			"percent":   diskPercent,
			"usedText":  diskUsedText,
			"totalText": diskTotalText,
			"drive":     "/",
		},
		"uptime": map[string]any{
			"seconds": uptime,
		},
	}
	systemMetricsCache.data = data
	systemMetricsCache.at = time.Now()
	return data
}

func clampSystemPercent(value float64) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return int(value)
}

func formatSystemBytes(value uint64) string {
	return fmt.Sprintf("%.1fG", float64(value)/(1024*1024*1024))
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}
