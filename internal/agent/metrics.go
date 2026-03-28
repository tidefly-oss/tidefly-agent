package agent

import (
	"fmt"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type Metrics struct {
	CPUPercent  float64
	MemUsedMB   float64
	MemTotalMB  float64
	DiskUsedGB  float64
	DiskTotalGB float64
	NetRxMB     float64
	NetTxMB     float64
}

func CollectMetrics() (*Metrics, error) {
	cpuPcts, err := cpu.Percent(0, false)
	if err != nil {
		return nil, fmt.Errorf("cpu: %w", err)
	}
	cpuPct := 0.0
	if len(cpuPcts) > 0 {
		cpuPct = cpuPcts[0]
	}

	memStat, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("mem: %w", err)
	}

	diskStat, err := disk.Usage("/")
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}

	netStats, err := net.IOCounters(false)
	if err != nil {
		return nil, fmt.Errorf("net: %w", err)
	}
	var rxBytes, txBytes uint64
	for _, s := range netStats {
		rxBytes += s.BytesRecv
		txBytes += s.BytesSent
	}

	return &Metrics{
		CPUPercent:  cpuPct,
		MemUsedMB:   float64(memStat.Used) / 1024 / 1024,
		MemTotalMB:  float64(memStat.Total) / 1024 / 1024,
		DiskUsedGB:  float64(diskStat.Used) / 1024 / 1024 / 1024,
		DiskTotalGB: float64(diskStat.Total) / 1024 / 1024 / 1024,
		NetRxMB:     float64(rxBytes) / 1024 / 1024,
		NetTxMB:     float64(txBytes) / 1024 / 1024,
	}, nil
}
