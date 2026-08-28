package agent

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"xuanwu/internal/wire"
)

var startTime = time.Now()

// collectMetrics gathers a lightweight node health snapshot for a heartbeat.
//
// Every host reading comes from a procfs global (/proc/stat, /proc/meminfo,
// /proc/uptime, /proc/cpuinfo) or from statfs on the node's data bind mount.
// None of those are namespaced, so they describe the host even though the agent
// runs in an unprivileged container — no host pid/net namespace and no extra
// mounts. Namespaced sources (/proc/net/*, /proc/<pid>) would only describe the
// agent's own container, so connection and process counts are deliberately not
// reported rather than reported wrong.
func (c *Config) collectMetrics() *wire.NodeMetrics {
	memTotal, memUsed := memInfo()
	diskTotal, diskUsed := diskInfo()
	cores, model := cpuInfo()
	return &wire.NodeMetrics{
		LoadAvg:     loadAvg1(),
		MemUsedPct:  pct(memUsed, memTotal),
		XrayVersion: c.xrayVersion(),
		CertExpiry:  certExpiry(c.CertPath),
		Uptime:      int64(time.Since(startTime).Seconds()),
		CPUUsedPct:  cpuUsedPct(),
		CPUCores:    cores,
		CPUModel:    model,
		MemTotal:    memTotal,
		MemUsed:     memUsed,
		DiskTotal:   diskTotal,
		DiskUsed:    diskUsed,
		HostUptime:  hostUptime(),
	}
}

// pct is used out of total as a 0-100 percentage.
func pct(used, total int64) int {
	if total <= 0 || used <= 0 {
		return 0
	}
	return int(float64(used) / float64(total) * 100)
}

func loadAvg1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	if f := strings.Fields(string(b)); len(f) > 0 {
		v, _ := strconv.ParseFloat(f[0], 64)
		return v
	}
	return 0
}

// hostUptime returns seconds since the host booted.
func hostUptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	if v < 0 {
		return 0
	}
	return int64(v)
}

// cpuPrev holds the previous /proc/stat counters. procfs exposes cumulative
// jiffies, not a rate, so utilisation needs two samples: the first call only
// seeds the baseline and reports 0.
var cpuPrev struct {
	sync.Mutex
	total, idle uint64
	seeded      bool
}

// cpuUsedPct returns host CPU utilisation since the previous call, 0-100.
func cpuUsedPct() int {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	total, idle, ok := parseCPUStat(string(b))
	if !ok {
		return 0
	}
	cpuPrev.Lock()
	defer cpuPrev.Unlock()
	prevTotal, prevIdle, seeded := cpuPrev.total, cpuPrev.idle, cpuPrev.seeded
	cpuPrev.total, cpuPrev.idle, cpuPrev.seeded = total, idle, true
	if !seeded || total <= prevTotal || idle < prevIdle {
		return 0
	}
	dTotal, dIdle := total-prevTotal, idle-prevIdle
	if dIdle >= dTotal {
		return 0
	}
	return int(float64(dTotal-dIdle) / float64(dTotal) * 100)
}

// parseCPUStat sums the aggregate "cpu" line of /proc/stat into total and idle
// jiffies, counting iowait as idle (the CPU is not running work during it).
func parseCPUStat(s string) (total, idle uint64, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		for i, v := range f[1:] {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			total += n
			if i == 3 || i == 4 { // idle, iowait
				idle += n
			}
		}
		return total, idle, true
	}
	return 0, 0, false
}

// cpuInfo returns the host's logical core count and CPU model. Both are static
// for the life of the host, so they are read once and cached.
var (
	cpuInfoOnce sync.Once
	cpuCoresVal int
	cpuModelVal string
)

func cpuInfo() (int, string) {
	cpuInfoOnce.Do(func() {
		b, err := os.ReadFile("/proc/cpuinfo")
		if err != nil {
			return
		}
		cpuCoresVal, cpuModelVal = parseCPUInfo(string(b))
	})
	return cpuCoresVal, cpuModelVal
}

// parseCPUInfo counts logical CPUs and extracts a model name. x86 carries
// "model name" directly; arm64 has no such field and only reports the numeric
// MIDR implementer/part, which armModel resolves.
func parseCPUInfo(s string) (cores int, model string) {
	var impl, part string
	for _, line := range strings.Split(s, "\n") {
		k, v, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			cores++
		case "model name":
			if model == "" {
				model = v
			}
		case "CPU implementer":
			impl = v
		case "CPU part":
			part = v
		}
	}
	if model == "" {
		model = armModel(impl, part)
	}
	return cores, model
}

// armParts maps arm64 MIDR part IDs (implementer 0x41, Arm Ltd) to core names.
var armParts = map[string]string{
	"0xd03": "Cortex-A53",
	"0xd07": "Cortex-A57",
	"0xd08": "Cortex-A72",
	"0xd09": "Cortex-A73",
	"0xd0a": "Cortex-A75",
	"0xd0b": "Cortex-A76",
	"0xd0c": "Neoverse-N1",
	"0xd40": "Neoverse-V1",
	"0xd49": "Neoverse-N2",
	"0xd4f": "Neoverse-V2",
}

func armModel(impl, part string) string {
	if !strings.EqualFold(impl, "0x41") {
		return ""
	}
	return armParts[strings.ToLower(part)]
}

// memInfo returns host memory total and used in bytes. "Used" is total minus
// MemAvailable, so reclaimable page cache does not count as used.
func memInfo() (total, used int64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	total, avail := parseMemInfo(string(b))
	if total <= 0 {
		return 0, 0
	}
	if avail > total {
		avail = total
	}
	return total, total - avail
}

func parseMemInfo(s string) (total, avail int64) {
	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = meminfoBytes(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			avail = meminfoBytes(line)
		}
	}
	return total, avail
}

// meminfoBytes converts the kB value of a /proc/meminfo line to bytes.
func meminfoBytes(line string) int64 {
	f := strings.Fields(line)
	if len(f) < 2 {
		return 0
	}
	v, _ := strconv.ParseInt(f[1], 10, 64)
	return v * 1024
}

// diskPaths are probed in order for the filesystem to report. /data is the
// node's bind mount from the host, so statfs on it describes the disk the node
// actually writes to without mounting the host root; / only falls back to the
// container's overlay, which reflects wherever Docker's data-root lives.
var diskPaths = []string{"/data", "/"}

// diskInfo returns total and used bytes of the host filesystem backing the
// node's data directory.
func diskInfo() (total, used int64) {
	for _, p := range diskPaths {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil {
			continue
		}
		bs := int64(st.Bsize)
		if bs <= 0 || st.Blocks == 0 {
			continue
		}
		total = int64(st.Blocks) * bs
		return total, total - int64(st.Bfree)*bs
	}
	return 0, 0
}

// xrayVersion caches the Xray version (it only changes on image upgrade) and
// refreshes it at most hourly. It reads the container's image reference via
// `docker inspect` and takes the tag, so it needs only inspect access — no
// `docker exec` into the container (see the docker proxy).
var (
	xrayVerMu  sync.Mutex
	xrayVerVal string
	xrayVerAt  time.Time
	xrayVerTTL = time.Hour
)

func (c *Config) xrayVersion() string {
	xrayVerMu.Lock()
	defer xrayVerMu.Unlock()
	if xrayVerVal != "" && time.Since(xrayVerAt) < xrayVerTTL {
		return xrayVerVal
	}
	out, err := exec.Command("docker", "inspect", "-f", "{{.Config.Image}}", c.XrayContainer).Output()
	if err == nil {
		if v := imageTag(string(out)); v != "" {
			xrayVerVal = v
			xrayVerAt = time.Now()
		}
	}
	return xrayVerVal
}

// imageTag extracts the tag from a container image reference, e.g.
// "ghcr.io/xtls/xray-core:26.6.27" -> "26.6.27". It returns "" for an untagged
// reference, a "latest" tag, or a digest pin (nothing meaningful to show).
func imageTag(ref string) string {
	ref = strings.TrimSpace(ref)
	if i := strings.LastIndexByte(ref, '@'); i >= 0 {
		ref = ref[:i] // drop a digest suffix
	}
	slash := strings.LastIndexByte(ref, '/')
	colon := strings.LastIndexByte(ref, ':')
	if colon <= slash { // no tag (a ':' in the host part is a port, not a tag)
		return ""
	}
	tag := ref[colon+1:]
	if tag == "latest" {
		return ""
	}
	return tag
}

// certExpiry returns the notAfter of the first cert in the PEM file, or 0.
func certExpiry(path string) int64 {
	if path == "" {
		return 0
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for len(b) > 0 {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			return cert.NotAfter.Unix()
		}
	}
	return 0
}
