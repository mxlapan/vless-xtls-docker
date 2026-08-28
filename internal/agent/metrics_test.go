package agent

import "testing"

func TestParseCPUStat(t *testing.T) {
	const stat = `cpu  100 20 30 700 40 0 10 0 0 0
cpu0 50 10 15 350 20 0 5 0 0 0
intr 12345
`
	total, idle, ok := parseCPUStat(stat)
	if !ok {
		t.Fatal("aggregate cpu line not parsed")
	}
	// The per-cpu lines must not be folded in: only the "cpu" line counts.
	if want := uint64(900); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	// idle + iowait
	if want := uint64(740); idle != want {
		t.Errorf("idle = %d, want %d", idle, want)
	}

	if _, _, ok := parseCPUStat("intr 1\nctxt 2\n"); ok {
		t.Error("parsed a /proc/stat with no cpu line")
	}
	if _, _, ok := parseCPUStat("cpu  1 2 x 4 5\n"); ok {
		t.Error("parsed a cpu line with a non-numeric field")
	}
}

func TestCPUUsedPctNeedsTwoSamples(t *testing.T) {
	// procfs exposes counters, so the first sample can only seed the baseline.
	if got := cpuUsedPct(); got != 0 {
		t.Errorf("first sample = %d%%, want 0 (baseline only)", got)
	}
	if got := cpuUsedPct(); got < 0 || got > 100 {
		t.Errorf("second sample = %d%%, want 0-100", got)
	}
}

func TestParseCPUInfoX86(t *testing.T) {
	const info = `processor	: 0
model name	: Intel(R) Xeon(R) CPU E5-2680 v4 @ 2.40GHz
processor	: 1
model name	: Intel(R) Xeon(R) CPU E5-2680 v4 @ 2.40GHz
`
	cores, model := parseCPUInfo(info)
	if cores != 2 {
		t.Errorf("cores = %d, want 2", cores)
	}
	if want := "Intel(R) Xeon(R) CPU E5-2680 v4 @ 2.40GHz"; model != want {
		t.Errorf("model = %q, want %q", model, want)
	}
}

func TestParseCPUInfoARM(t *testing.T) {
	// arm64 has no "model name"; the core is identified by its MIDR fields.
	const info = `processor	: 0
BogoMIPS	: 50.00
CPU implementer	: 0x41
CPU architecture: 8
CPU part	: 0xd0c
processor	: 1
CPU implementer	: 0x41
CPU part	: 0xd0c
`
	cores, model := parseCPUInfo(info)
	if cores != 2 {
		t.Errorf("cores = %d, want 2", cores)
	}
	if model != "Neoverse-N1" {
		t.Errorf("model = %q, want Neoverse-N1", model)
	}
}

func TestArmModelUnknown(t *testing.T) {
	if got := armModel("0x50", "0x000"); got != "" {
		t.Errorf("non-Arm implementer = %q, want empty", got)
	}
	if got := armModel("0x41", "0xffff"); got != "" {
		t.Errorf("unknown part = %q, want empty", got)
	}
}

func TestParseMemInfo(t *testing.T) {
	const info = `MemTotal:       12190552 kB
MemFree:         1000000 kB
MemAvailable:    8533386 kB
Buffers:          100000 kB
`
	total, avail := parseMemInfo(info)
	if want := int64(12190552 * 1024); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	if want := int64(8533386 * 1024); avail != want {
		t.Errorf("avail = %d, want %d", avail, want)
	}
	// Used excludes reclaimable cache, so it is derived from MemAvailable.
	if got := pct(total-avail, total); got != 30 {
		t.Errorf("used = %d%%, want 30%%", got)
	}
}

func TestParseMemInfoMissingFields(t *testing.T) {
	total, avail := parseMemInfo("SwapTotal: 0 kB\n")
	if total != 0 || avail != 0 {
		t.Errorf("got %d/%d, want 0/0 for meminfo without MemTotal", total, avail)
	}
	if got := pct(0, 0); got != 0 {
		t.Errorf("pct with zero total = %d, want 0", got)
	}
}

func TestHostReadings(t *testing.T) {
	// These read the live procfs/statfs of whatever runs the tests; assert only
	// that they are sane, not their values.
	if got := hostUptime(); got <= 0 {
		t.Errorf("host uptime = %d, want > 0", got)
	}
	total, used := diskInfo()
	if total <= 0 {
		t.Fatalf("disk total = %d, want > 0", total)
	}
	if used < 0 || used > total {
		t.Errorf("disk used = %d, want 0..%d", used, total)
	}
	if cores, _ := cpuInfo(); cores <= 0 {
		t.Errorf("cpu cores = %d, want > 0", cores)
	}
}
