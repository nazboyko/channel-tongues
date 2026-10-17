package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/translate"
)

// runCost is what one translation run cost on this machine: time, tokens,
// and, when macmon is installed, GPU use and power. `--measure` prints it as
// a table; `--measure-json` writes it to a file.
type runCost struct {
	Machine          machineInfo          `json:"machine"`
	Model            *translate.ModelInfo `json:"model,omitempty"`
	Mode             string               `json:"mode"` // "cold" or "warm"
	Seconds          float64              `json:"seconds"`
	ModelLoadSeconds float64              `json:"modelLoadSeconds,omitempty"` // cold runs only
	Translation      callTotals           `json:"translation"`                // translate calls and the language check
	ReadBack         callTotals           `json:"readBack"`                   // the back-translation and the word check
	Languages        []langCost           `json:"languages"`
	Power            *powerUse            `json:"power,omitempty"`
}

type langCost struct {
	Lang            string     `json:"lang"`
	Seconds         float64    `json:"seconds"`         // translating and checking
	ReadBackSeconds float64    `json:"readBackSeconds"` // runs next to the next language's translation
	Translation     callTotals `json:"translation"`
	ReadBack        callTotals `json:"readBack"`
}

type callTotals struct {
	Calls           int     `json:"calls"`
	InputTokens     int     `json:"inputTokens"`  // prompt_eval_count
	OutputTokens    int     `json:"outputTokens"` // eval_count
	EvalSeconds     float64 `json:"evalSeconds"`  // eval_duration
	TokensPerSecond float64 `json:"tokensPerSecond"`
}

func (t *callTotals) add(st translate.Stats) {
	t.Calls++
	t.InputTokens += st.PromptTokens
	t.OutputTokens += st.OutputTokens
	t.EvalSeconds += st.Eval.Seconds()
	if t.EvalSeconds > 0 {
		t.TokensPerSecond = float64(t.OutputTokens) / t.EvalSeconds
	}
}

func (t *callTotals) merge(o callTotals) {
	t.Calls += o.Calls
	t.InputTokens += o.InputTokens
	t.OutputTokens += o.OutputTokens
	t.EvalSeconds += o.EvalSeconds
	if t.EvalSeconds > 0 {
		t.TokensPerSecond = float64(t.OutputTokens) / t.EvalSeconds
	}
}

// costOf adds up the model calls of every language.
func costOf(results []translate.Result, wall time.Duration, cold bool) runCost {
	c := runCost{Mode: "warm", Seconds: wall.Seconds(), Languages: []langCost{}}
	if cold {
		c.Mode = "cold"
	}
	var load time.Duration
	for _, r := range results {
		lc := langCost{Lang: r.Lang, Seconds: r.Duration.Seconds(), ReadBackSeconds: r.BackDuration.Seconds()}
		for _, call := range r.Calls {
			load = max(load, call.Stats.Load)
			if call.Kind.ReadBack() {
				lc.ReadBack.add(call.Stats)
			} else {
				lc.Translation.add(call.Stats)
			}
		}
		c.Translation.merge(lc.Translation)
		c.ReadBack.merge(lc.ReadBack)
		c.Languages = append(c.Languages, lc)
	}
	if cold {
		c.ModelLoadSeconds = load.Seconds()
	}
	return c
}

// machineInfo is the chip and memory, from sysctl on macOS.
type machineInfo struct {
	Chip     string  `json:"chip,omitempty"`
	MemoryGB float64 `json:"memoryGB,omitempty"`
}

func readMachine() machineInfo {
	if runtime.GOOS != "darwin" {
		return machineInfo{}
	}
	out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string", "hw.memsize").Output()
	if err != nil {
		return machineInfo{}
	}
	return parseSysctl(string(out))
}

// parseSysctl reads the two lines of `sysctl -n machdep.cpu.brand_string hw.memsize`.
func parseSysctl(out string) machineInfo {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var m machineInfo
	if len(lines) > 0 {
		m.Chip = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		if b, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
			m.MemoryGB = float64(b) / (1 << 30)
		}
	}
	return m
}

// powerUse comes from macmon samples taken during the run.
type powerUse struct {
	Samples    int     `json:"samples"`
	GPUAvgPct  float64 `json:"gpuAvgPct"` // share of time the GPU was busy
	GPUPeakPct float64 `json:"gpuPeakPct"`
	WattsAvg   float64 `json:"wattsAvg"` // whole system, macmon's sys_power
	WattsPeak  float64 `json:"wattsPeak"`
	WattHours  float64 `json:"wattHours"` // average watts x run seconds / 3600
}

// macmonSample is the part of a `macmon pipe` line we use.
type macmonSample struct {
	GPUActive *float64  `json:"gpu_active_ratio"`
	GPUUsage  []float64 `json:"gpu_usage"` // [MHz, ratio], for older macmon
	SysPower  float64   `json:"sys_power"`
}

func (s macmonSample) gpu() float64 {
	if s.GPUActive != nil {
		return *s.GPUActive
	}
	if len(s.GPUUsage) == 2 {
		return s.GPUUsage[1]
	}
	return 0
}

// summarizePower turns samples into averages, peaks and energy for a run of
// the given length. Nil without samples.
func summarizePower(samples []macmonSample, seconds float64) *powerUse {
	if len(samples) == 0 {
		return nil
	}
	p := &powerUse{Samples: len(samples)}
	var gpu, watts float64
	for _, s := range samples {
		gpu += s.gpu()
		watts += s.SysPower
		p.GPUPeakPct = max(p.GPUPeakPct, 100*s.gpu())
		p.WattsPeak = max(p.WattsPeak, s.SysPower)
	}
	p.GPUAvgPct = 100 * gpu / float64(len(samples))
	p.WattsAvg = watts / float64(len(samples))
	p.WattHours = p.WattsAvg * seconds / 3600
	return p
}

// powerMeter runs `macmon pipe -i 500` next to the translation. macmon is a
// separate tool; without it the meter does nothing.
type powerMeter struct {
	cmd     *exec.Cmd
	mu      sync.Mutex
	samples []macmonSample
	done    chan struct{}
}

func startPowerMeter(ctx context.Context) *powerMeter {
	path, err := exec.LookPath("macmon")
	if err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, path, "pipe", "-i", "500")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	m := &powerMeter{cmd: cmd, done: make(chan struct{})}
	go m.read(out)
	return m
}

func (m *powerMeter) read(r io.Reader) {
	defer close(m.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var s macmonSample
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			m.mu.Lock()
			m.samples = append(m.samples, s)
			m.mu.Unlock()
		}
	}
}

// stop ends macmon and sums up what it saw over a run of the given length.
func (m *powerMeter) stop(seconds float64) *powerUse {
	if m == nil {
		return nil
	}
	m.cmd.Process.Kill()
	<-m.done
	m.cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	return summarizePower(m.samples, seconds)
}

// printCost writes the summary as a small table.
func printCost(c runCost) {
	fmt.Println()
	fmt.Println("What it cost on this machine")
	if c.Machine.Chip != "" {
		fmt.Printf("  Machine: %s, %.0f GB\n", c.Machine.Chip, c.Machine.MemoryGB)
	}
	if m := c.Model; m != nil {
		line := "  Model: " + m.Name
		if m.Parameters != "" {
			line += fmt.Sprintf(" (%s, %s)", m.Parameters, m.Quantization)
		}
		if m.DiskBytes > 0 {
			line += fmt.Sprintf(", %.1f GB download", gb(m.DiskBytes))
		}
		if m.Loaded {
			line += fmt.Sprintf(", %.1f GB in memory, %.0f%% of it on the GPU (Ollama /api/ps)", gb(m.MemoryBytes), 100*float64(m.GPUBytes)/float64(max(m.MemoryBytes, 1)))
		}
		fmt.Println(line)
	}
	run := fmt.Sprintf("  Run: %s, %d languages, %.1f s", c.Mode, len(c.Languages), c.Seconds)
	if c.Mode == "cold" {
		run += fmt.Sprintf(", of which loading the model %.1f s", c.ModelLoadSeconds)
	}
	fmt.Println(run)
	fmt.Println()
	fmt.Printf("  %-28s %-29s   %s\n", "", "translating and checking", "read-back")
	fmt.Printf("  %-28s %7s %7s %6s %6s   %9s %7s %6s %6s\n", "Language", "s", "in tok", "out", "tok/s", "s", "in tok", "out", "tok/s")
	row := func(name string, secs float64, t callTotals, rbSecs float64, rb callTotals) {
		fmt.Printf("  %-28s %7.1f %7d %6d %6.0f   %9.1f %7d %6d %6.0f\n", truncate(name, 28), secs, t.InputTokens, t.OutputTokens, t.TokensPerSecond, rbSecs, rb.InputTokens, rb.OutputTokens, rb.TokensPerSecond)
	}
	var secs, rbSecs float64
	for _, l := range c.Languages {
		row(langs.EnglishName(l.Lang)+" ("+l.Lang+")", l.Seconds, l.Translation, l.ReadBackSeconds, l.ReadBack)
		secs += l.Seconds
		rbSecs += l.ReadBackSeconds
	}
	row("Sum", secs, c.Translation, rbSecs, c.ReadBack)
	fmt.Printf("  %d model calls for translating and checking, %d for the read-back. Each read-back runs next to the next language's translation, so the run took %.1f s, not the sum.\n", c.Translation.Calls, c.ReadBack.Calls, c.Seconds)
	if p := c.Power; p != nil {
		fmt.Printf("  Power (macmon, %d samples): GPU busy %.0f%% on average, %.0f%% at peak; whole system %.0f W on average, %.0f W at peak; %.2f Wh for the run.\n",
			p.Samples, p.GPUAvgPct, p.GPUPeakPct, p.WattsAvg, p.WattsPeak, p.WattHours)
	}
}

func gb(b int64) float64 { return float64(b) / 1e9 }

func writeCostJSON(path string, c runCost) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
