package analyzer_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

// goldenScenario is one folder under testdata/golden/<name>:
//
//	input_costs.json      — []model.CostRecord
//	input_usage.json      — []analyzer.UsageRecord
//	expected_zombies.json — []zombieGolden (a deterministic projection of the
//	                       fields the detector produces — see goldenZombie)
//
// The harness loads the inputs, runs analyzer.Detect, projects the result,
// and compares it byte-for-byte to expected_zombies.json. Every input row
// is validated first; an invalid fixture fails the test loud (B's job).
//
// Set UPDATE_GOLDEN=1 when running `go test` to overwrite the expected file
// with the current detector output. Use sparingly — review the diff before
// committing.
const goldenDir = "testdata/golden"

// goldenZombie is the trimmed projection of model.ZombieResource the harness
// asserts against. The full struct contains derived fields (ARN) and
// nullable dismissal state we deliberately keep out of the fixture so it
// stays small and stable. Add fields here only when a new detector
// behaviour needs assertion coverage.
type goldenZombie struct {
	Service     string  `json:"service"`
	Region      string  `json:"region"`
	ResourceID  string  `json:"resource_id"`
	MonthlyCost float64 `json:"monthly_cost"`
	UsageMetric string  `json:"usage_metric"`
	UsageAvg    float64 `json:"usage_avg"`
	Reason      string  `json:"reason"`
	Owner       string  `json:"owner"`
}

func projectZombies(zs []model.ZombieResource) []goldenZombie {
	out := make([]goldenZombie, len(zs))
	for i, z := range zs {
		out[i] = goldenZombie{
			Service:     z.Service,
			Region:      z.Region,
			ResourceID:  z.ResourceID,
			MonthlyCost: z.MonthlyCost,
			UsageMetric: z.UsageMetric,
			UsageAvg:    z.UsageAvg,
			Reason:      z.Reason,
			Owner:       z.Owner,
		}
	}
	// Detect() iterates costs in input order, but tests should not depend on
	// that — sort by ResourceID for stable output.
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceID < out[j].ResourceID })
	return out
}

func TestGolden(t *testing.T) {
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatalf("read goldenDir: %v", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			runGoldenScenario(t, filepath.Join(goldenDir, name))
		})
	}
}

func runGoldenScenario(t *testing.T, dir string) {
	t.Helper()

	var costs []model.CostRecord
	mustLoadJSON(t, filepath.Join(dir, "input_costs.json"), &costs)
	for i, c := range costs {
		if err := c.Validate(); err != nil {
			t.Fatalf("input_costs[%d] failed validation: %v", i, err)
		}
	}

	var usage []analyzer.UsageRecord
	mustLoadJSON(t, filepath.Join(dir, "input_usage.json"), &usage)
	for i, u := range usage {
		if err := u.Validate(); err != nil {
			t.Fatalf("input_usage[%d] failed validation: %v", i, err)
		}
	}

	got := projectZombies(analyzer.Detect(costs, usage, ""))
	gotBytes, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	gotBytes = append(gotBytes, '\n')

	expectedPath := filepath.Join(dir, "expected_zombies.json")

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(expectedPath, gotBytes, 0o644); err != nil {
			t.Fatalf("rewrite golden: %v", err)
		}
		t.Logf("UPDATE_GOLDEN=1 wrote %s", expectedPath)
		return
	}

	wantBytes, err := os.ReadFile(expectedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing %s — run with UPDATE_GOLDEN=1 to create it", expectedPath)
		}
		t.Fatalf("read expected: %v", err)
	}

	if string(gotBytes) != string(wantBytes) {
		t.Errorf("zombie set differs from %s\n--- want ---\n%s\n--- got ---\n%s\n--- diff ---\n%s",
			expectedPath, wantBytes, gotBytes, lineDiff(string(wantBytes), string(gotBytes)))
	}
}

func mustLoadJSON(t *testing.T, path string, dst any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// lineDiff produces a minimal first-difference summary so test failures
// point at the changed line without pulling in a diff library.
func lineDiff(want, got string) string {
	wLines := strings.Split(want, "\n")
	gLines := strings.Split(got, "\n")
	for i := 0; i < len(wLines) || i < len(gLines); i++ {
		var w, g string
		if i < len(wLines) {
			w = wLines[i]
		}
		if i < len(gLines) {
			g = gLines[i]
		}
		if w != g {
			return "first diff at line " + itoa(i+1) + ":\n  want: " + w + "\n   got: " + g
		}
	}
	return "(no per-line diff — content equal but byte-mismatch)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
