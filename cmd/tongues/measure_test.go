package main

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/nazboyko/channel-tongues/internal/translate"
)

func call(kind translate.CallKind, in, out int, eval, load time.Duration) translate.Call {
	return translate.Call{Kind: kind, Stats: translate.Stats{PromptTokens: in, OutputTokens: out, Eval: eval, Load: load}}
}

func TestCostAddsTranslationAndReadBackApart(t *testing.T) {
	results := []translate.Result{
		{Lang: "es", Duration: 3 * time.Second, BackDuration: 2 * time.Second, Calls: []translate.Call{
			call(translate.CallTranslate, 800, 150, 2*time.Second, 4*time.Second), // the cold load is on the first call
			call(translate.CallDetect, 100, 10, 200*time.Millisecond, 0),
			call(translate.CallBack, 300, 50, time.Second, 0),
			call(translate.CallNewWords, 400, 30, 500*time.Millisecond, 0),
		}},
		{Lang: "ja", Duration: time.Second, Calls: []translate.Call{
			call(translate.CallTranslate, 800, 140, 1800*time.Millisecond, 0),
		}},
	}
	c := costOf(translate.Source{Title: "Rain · 3 Hours 🌧", Description: "One\r\nTwo"}, results, 5*time.Second, true)
	if c.Mode != "cold" || c.Seconds != 5 || c.ModelLoadSeconds != 4 || len(c.Languages) != 2 {
		t.Fatalf("got %+v", c)
	}
	es := c.Languages[0]
	if es.Translation.Calls != 2 || es.Translation.InputTokens != 900 || es.Translation.OutputTokens != 160 || es.ReadBack.Calls != 2 || es.ReadBack.InputTokens != 700 || es.ReadBack.OutputTokens != 80 || es.ReadBackSeconds != 2 {
		t.Fatalf("es %+v", es)
	}
	// Output tokens per second is eval_count over eval_duration.
	if math.Abs(es.Translation.TokensPerSecond-160/2.2) > 0.01 {
		t.Fatalf("tok/s %v", es.Translation.TokensPerSecond)
	}
	if c.Translation.OutputTokens != 300 || math.Abs(c.Translation.TokensPerSecond-300/4.0) > 0.01 || c.ReadBack.Calls != 2 {
		t.Fatalf("totals %+v %+v", c.Translation, c.ReadBack)
	}
	if c.Source.TitleChars != 16 || c.Source.DescriptionChars != 7 {
		t.Fatalf("lengths in characters, not bytes: %+v", c.Source)
	}
	if warm := costOf(translate.Source{}, results, 5*time.Second, false); warm.Mode != "warm" || warm.ModelLoadSeconds != 0 {
		t.Fatalf("a warm run has no load time: %+v", warm)
	}
}

func TestParseSysctl(t *testing.T) {
	m := parseSysctl("Apple M5 Max\n68719476736\n")
	if m.Chip != "Apple M5 Max" || m.MemoryGB != 64 {
		t.Fatalf("got %+v", m)
	}
	if m := parseSysctl(""); m.Chip != "" || m.MemoryGB != 0 {
		t.Fatalf("empty: %+v", m)
	}
}

func TestSummarizePower(t *testing.T) {
	// Two lines as macmon 0.10 writes them (trimmed), and one from an older
	// macmon without gpu_active_ratio.
	lines := []string{
		`{"all_power":20.1,"gpu_active_ratio":0.9,"gpu_usage":[1400,0.6],"sys_power":40.0}`,
		`{"all_power":30.2,"gpu_active_ratio":0.5,"gpu_usage":[1400,0.3],"sys_power":60.0}`,
		`{"gpu_usage":[1400,0.1],"sys_power":20.0}`,
	}
	var samples []macmonSample
	for _, l := range lines {
		var s macmonSample
		if err := json.Unmarshal([]byte(l), &s); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, s)
	}
	p := summarizePower(samples, 90)
	if p.Samples != 3 || math.Abs(p.GPUAvgPct-50) > 0.01 || p.GPUPeakPct != 90 || p.WattsAvg != 40 || p.WattsPeak != 60 || math.Abs(p.WattHours-1) > 1e-9 {
		t.Fatalf("got %+v", p)
	}
	if summarizePower(nil, 90) != nil {
		t.Fatal("no samples, no power line")
	}
	var none *powerMeter
	if none.stop(10) != nil {
		t.Fatal("without macmon the meter does nothing")
	}
}

func TestCostCountsWhatTheChecksDid(t *testing.T) {
	results := []translate.Result{
		{Lang: "es", Status: translate.StatusReady, Retries: 1, Problems: []string{"the title is 106 characters, YouTube allows 100"}},
		{Lang: "de", Status: translate.StatusReady, Retries: 1, Problems: []string{"a placeholder is missing", "the title is 104 characters, YouTube allows 100"}},
		{Lang: "ja", Status: translate.StatusCheck, AddedTitle: []string{"pleasant"}, Problems: []string{"adds: pleasant"}},
		{Lang: "hi", Status: translate.StatusReady, AddedLine: []string{"through"}, Problems: []string{"adds: through"}},
	}
	o := costOf(translate.Source{}, results, time.Second, false).Outcome
	if o.Ready != 3 || o.ToCheck != 1 || o.Retries != 2 || o.TitleTooLongFirst != 1 || o.ReadBackTitle != 1 || o.ReadBackLineOnly != 1 {
		t.Fatalf("got %+v", o)
	}
}
