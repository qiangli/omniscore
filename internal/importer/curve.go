package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

const curvePrompt = `You are looking at the scoring worksheet of an AP Calculus BC released exam.

Find the table that maps composite raw score → AP grade (1–5). Return ONE JSON object, no prose, no Markdown code fence:

{
  "ranges": [
    {"raw_min": <int>, "raw_max": <int>, "scaled": <int 1..5>},
    ...
  ]
}

Rules:
- One entry per AP grade (so usually exactly 5: one each for grades 1, 2, 3, 4, 5).
- raw_min and raw_max are inclusive endpoints of the raw composite score range.
- "scaled" must be an integer between 1 and 5.
- If only the multiple-choice raw → AP grade table is shown, use it.
- Output ONLY the JSON object. No prose.`

type curveResp struct {
	Ranges []struct {
		RawMin int `json:"raw_min"`
		RawMax int `json:"raw_max"`
		Scaled int `json:"scaled"`
	} `json:"ranges"`
}

// ExtractCurve runs the curve prompt against scoring-worksheet pages and
// returns a content.Curve with a single "mcq_total" section. The range
// entries are expanded into per-raw-value CurvePoints so the runtime
// scoring lookup never has to interpolate.
func ExtractCurve(ctx context.Context, p vision.Provider, slug string, pagePaths []string) (content.Curve, error) {
	out := content.Curve{
		TestSlug: slug,
		Sections: map[string][]content.CurvePoint{},
	}
	if len(pagePaths) == 0 {
		return out, nil
	}
	// Try each candidate page; first that parses with at least one range wins.
	for _, page := range pagePaths {
		img, err := os.ReadFile(page)
		if err != nil {
			return out, fmt.Errorf("curve: read %s: %w", page, err)
		}
		raw, err := p.Generate(ctx, vision.Request{
			Image:       img,
			Prompt:      curvePrompt,
			Temperature: 0.0,
		})
		if err != nil {
			return out, fmt.Errorf("curve: %s: %w", page, err)
		}
		ranges, perr := parseCurve(raw)
		if perr != nil || len(ranges) == 0 {
			continue
		}
		points := expandRanges(ranges)
		out.Sections["mcq_total"] = points
		return out, nil
	}
	return out, fmt.Errorf("curve: no scoring-worksheet page parsed")
}

func parseCurve(raw string) ([]struct {
	RawMin int `json:"raw_min"`
	RawMax int `json:"raw_max"`
	Scaled int `json:"scaled"`
}, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var r curveResp
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, err
	}
	return r.Ranges, nil
}

// expandRanges turns sparse range entries into a per-raw-value point list. If
// ranges overlap, the higher scaled value wins. Out-of-bound rawMin/Max are
// trusted as-is — the runtime scorer clamps to nearest neighbor anyway.
func expandRanges(ranges []struct {
	RawMin int `json:"raw_min"`
	RawMax int `json:"raw_max"`
	Scaled int `json:"scaled"`
}) []content.CurvePoint {
	scoreFor := map[int]int{}
	for _, r := range ranges {
		if r.RawMax < r.RawMin {
			r.RawMin, r.RawMax = r.RawMax, r.RawMin
		}
		for i := r.RawMin; i <= r.RawMax; i++ {
			if existing, ok := scoreFor[i]; !ok || r.Scaled > existing {
				scoreFor[i] = r.Scaled
			}
		}
	}
	// Order by raw ascending for stable JSON output.
	out := make([]content.CurvePoint, 0, len(scoreFor))
	maxRaw := 0
	for raw := range scoreFor {
		if raw > maxRaw {
			maxRaw = raw
		}
	}
	for raw := 0; raw <= maxRaw; raw++ {
		if scaled, ok := scoreFor[raw]; ok {
			out = append(out, content.CurvePoint{Raw: raw, Scaled: scaled})
		}
	}
	return out
}
