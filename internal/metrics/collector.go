// Package metrics збирає системні метрики (CPU/RAM/disk) через gopsutil.
package metrics

import (
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

const Gib = 1024 * 1024 * 1024
const Mib = 1024 * 1024

// Metrics — JSON-форма для SSE /events/metrics (відповідає Python-реалізації).
type Metrics struct {
	Cpu         float64 `json:"cpu"`
	MemPercent  float64 `json:"mem_percent"`
	MemUsedMb   uint64  `json:"mem_used_mb"`
	MemTotalMb  uint64  `json:"mem_total_mb"`
	DiskPercent float64 `json:"disk_percent"`
	DiskUsedGb  float64 `json:"disk_used_gb"`
	DiskTotalGb float64 `json:"disk_total_gb"`
}

// Collect повертає поточний зріз метрик (відповідає psutil-викликам Python).
// psutil.cpu_percent(interval=0.5) → CPU 0.5с, потім time.sleep(0.5). Ми
// наближаємо цей таймінг через non-blocking cpu.Percent(0, false) + 0.5с sleep
// на стороні SSE-handler.
func Collect() Metrics {
	// CPU: вимірювання від попереднього виклику (interval=0 ігнорується подвійно,
	// gopsutil поверне значення з моменту попереднього виклику).
	cpuPct := 0.0
	if vs, err := cpu.Percent(0, false); err == nil && len(vs) > 0 {
		cpuPct = vs[0]
	}

	memPct, memUsed, memTotal := 0.0, uint64(0), uint64(0)
	if v, err := mem.VirtualMemory(); err == nil {
		memPct = v.UsedPercent
		memUsed = v.Used / Mib
		memTotal = v.Total / Mib
	}

	diskPct, diskUsed, diskTotal := 0.0, 0.0, 0.0
	if v, err := disk.Usage("/"); err == nil {
		diskPct = v.UsedPercent
		diskUsed = round1(float64(v.Used) / float64(Gib))
		diskTotal = round1(float64(v.Total) / float64(Gib))
	}

	return Metrics{
		Cpu:         round1(cpuPct),
		MemPercent:  round1(memPct),
		MemUsedMb:   memUsed,
		MemTotalMb:  memTotal,
		DiskPercent: round1(diskPct),
		DiskUsedGb:  diskUsed,
		DiskTotalGb: diskTotal,
	}
}

func round1(x float64) float64 {
	if x >= 0 {
		return float64(int(x*10+0.5)) / 10
	}
	return float64(int(x*10-0.5)) / 10
}
