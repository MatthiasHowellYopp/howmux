package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// baselinePath is the committed reference the working tree is compared against.
var baselinePath = filepath.Join(".howmux", "evals", "baseline.json")

// regressionTolerance is how far an agent average may drop below baseline before
// it counts as a regression. Small tolerance absorbs LLM-judge run-to-run noise.
const regressionTolerance = 0.05

// Baseline is the committed eval reference (see .howmux/evals/baseline.json).
type Baseline struct {
	Description string                   `json:"description"`
	GitHash     string                   `json:"git_hash"`
	Recorded    string                   `json:"recorded"`
	Transport   string                   `json:"transport"`
	Timeout     string                   `json:"timeout"`
	Agents      map[string]BaselineAgent `json:"agents"`
}

type BaselineAgent struct {
	Average float64                 `json:"average"`
	Cases   map[string]BaselineCase `json:"cases"`
}

type BaselineCase struct {
	Score float64 `json:"score"`
	Raw   string  `json:"raw"`
}

func loadBaseline(path string) (Baseline, error) {
	var b Baseline
	data, err := os.ReadFile(path)
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	return b, nil
}

// caseScores reduces an AgentResult to case_name -> normalized score (0..1),
// using the same pooled, skip-aware formula the runner uses for AgentScores
// (sum(score)/sum(max) over non-skipped criteria) so baseline and run values
// are computed identically.
func caseScores(result AgentResult) map[string]float64 {
	out := map[string]float64{}
	for _, c := range result.Cases {
		var tot, max float64
		for _, s := range c.Scores {
			if s.Skipped {
				continue
			}
			tot += float64(s.Score)
			max += float64(s.MaxScore)
		}
		if max > 0 {
			out[c.CaseName] = tot / max
		}
	}
	return out
}

// CompareToBaseline compares a completed run against the committed baseline and
// prints per-agent / per-case deltas. It returns an error if any agent present
// in the baseline regressed beyond regressionTolerance, so callers (and CI /
// prompt-change workflows) can gate on the exit status.
func CompareToBaseline(runName string) error {
	base, err := loadBaseline(baselinePath)
	if err != nil {
		return fmt.Errorf("no committed baseline at %s (%v) — record one with `howmux eval baseline --record`", baselinePath, err)
	}

	resultsDir := filepath.Join(".howmux", "evals", "results")
	resolved, err := resolveRunDirectory(runName)
	if err != nil {
		return fmt.Errorf("resolving run %s: %w", runName, err)
	}
	summary, err := loadSummary(filepath.Join(resultsDir, resolved, "summary.json"))
	if err != nil {
		return fmt.Errorf("loading run %s: %w", runName, err)
	}

	fmt.Printf("Baseline comparison: %s (baseline @ %s, %s)\n", resolved, base.GitHash, base.Recorded)
	fmt.Println("────────────────────────────────────────────────────────────")

	agents := make([]string, 0, len(base.Agents))
	for a := range base.Agents {
		agents = append(agents, a)
	}
	sort.Strings(agents)

	var regressed []string
	for _, agent := range agents {
		baseAgent := base.Agents[agent]
		runScore, inRun := summary.AgentScores[agent]
		if !inRun {
			fmt.Printf("\n%s: baseline %.3f → (not in this run)\n", agent, baseAgent.Average)
			continue
		}
		delta := runScore - baseAgent.Average
		ind := indicatorFor(delta)
		fmt.Printf("\n%s: %.3f → %.3f  %s %+.3f\n", agent, baseAgent.Average, runScore, ind, delta)
		if delta < -regressionTolerance {
			regressed = append(regressed, agent)
		}

		// Per-case deltas (surface which case moved).
		if result, err := loadAgentResult(filepath.Join(resultsDir, resolved, agent+".json")); err == nil {
			runCases := caseScores(result)
			names := mergeKeys(baselineCaseScores(baseAgent), runCases)
			for _, name := range names {
				bScore, hasB := baselineCaseScores(baseAgent)[name]
				rScore, hasR := runCases[name]
				switch {
				case hasB && hasR:
					cd := rScore - bScore
					fmt.Printf("  %-32s %.3f → %.3f  %s %+.3f\n", name, bScore, rScore, indicatorFor(cd), cd)
				case hasR:
					fmt.Printf("  %-32s [new]  %.3f\n", name, rScore)
				case hasB:
					fmt.Printf("  %-32s %.3f  [not run]\n", name, bScore)
				}
			}
		}
	}

	fmt.Println("\n────────────────────────────────────────────────────────────")
	if len(regressed) > 0 {
		return fmt.Errorf("regression: %v dropped more than %.0f%% below baseline", regressed, regressionTolerance*100)
	}
	fmt.Println("✅ No regression vs baseline")
	return nil
}

// CompareToBaselineLatest compares the most recent run against the baseline.
func CompareToBaselineLatest() error {
	latest, err := latestRunDir()
	if err != nil {
		return err
	}
	return CompareToBaseline(latest)
}

func baselineCaseScores(a BaselineAgent) map[string]float64 {
	out := map[string]float64{}
	for name, c := range a.Cases {
		out[name] = c.Score
	}
	return out
}

func indicatorFor(delta float64) string {
	switch {
	case delta > 0.001:
		return "↑"
	case delta < -0.001:
		return "↓"
	default:
		return "→"
	}
}

// latestRunDir returns the most recent run directory name under results/,
// or an error if there are none. Run dirs are timestamp-prefixed, so lexical
// max is chronological max.
func latestRunDir() (string, error) {
	resultsDir := filepath.Join(".howmux", "evals", "results")
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return "", fmt.Errorf("reading results dir: %w", err)
	}
	var latest string
	for _, e := range entries {
		if e.IsDir() && e.Name() > latest {
			latest = e.Name()
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no eval runs found under %s", resultsDir)
	}
	return latest, nil
}
