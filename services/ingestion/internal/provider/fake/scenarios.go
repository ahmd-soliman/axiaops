package fake

import (
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

//go:embed testdata/*.json
var scenarioFS embed.FS

type scenarioData struct {
	Costs []model.CostRecord    `json:"costs"`
	Usage []analyzer.UsageRecord `json:"usage"`
}

// scenarios is loaded once at init time from the embedded JSON files.
// Each file in testdata/ becomes a scenario keyed by its base name (without .json).
var scenarios map[string]scenarioData

func init() {
	scenarios = make(map[string]scenarioData)
	entries, err := scenarioFS.ReadDir("testdata")
	if err != nil {
		panic(fmt.Sprintf("fake: read testdata dir: %v", err))
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := scenarioFS.ReadFile("testdata/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("fake: read %s: %v", e.Name(), err))
		}
		var s scenarioData
		if err := json.Unmarshal(data, &s); err != nil {
			panic(fmt.Sprintf("fake: parse %s: %v", e.Name(), err))
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		scenarios[name] = s
	}
}
