package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

type curveRange struct {
	RawMin int `json:"raw_min"`
	RawMax int `json:"raw_max"`
	Scaled int `json:"scaled"`
}

type curveResp struct {
	// SAT shape: per-section ranges.
	Sections map[string][]curveRange `json:"sections,omitempty"`
	// AP shape: single composite range list.
	Ranges []curveRange `json:"ranges,omitempty"`
}

// ExtractCurve runs the profile's curve prompt against scoring-worksheet
// pages and returns a content.Curve. The prompt response may be either:
//
//   - {"ranges":[{raw_min,raw_max,scaled},...]} — single-section curve
//     (AP composite). Stored under the first entry of prof.CurveSections.
//   - {"sections":{"<section>":[{raw_min,raw_max,scaled},...]}} —
//     multi-section curve (SAT rw + math).
//
// Range entries are expanded into per-raw-value CurvePoints so the runtime
// scoring lookup never has to interpolate.
func ExtractCurve(ctx context.Context, p vision.Provider, prof profile.Profile, slug string, pagePaths []string) (content.Curve, error) {
	out := content.Curve{
		TestSlug: slug,
		Sections: map[string][]content.CurvePoint{},
	}
	if len(pagePaths) == 0 {
		return out, nil
	}
	prompt := prof.Prompts.Curve(prof)
	for _, page := range pagePaths {
		img, err := os.ReadFile(page)
		if err != nil {
			return out, fmt.Errorf("curve: read %s: %w", page, err)
		}
		raw, err := p.Generate(ctx, vision.Request{
			Image:       img,
			Prompt:      prompt,
			Temperature: 0.0,
		})
		if err != nil {
			return out, fmt.Errorf("curve: %s: %w", page, err)
		}
		parsed, perr := parseCurve(raw)
		if perr != nil {
			continue
		}
		if len(parsed.Sections) > 0 {
			for section, ranges := range parsed.Sections {
				if pts := expandRanges(ranges); len(pts) > 0 {
					out.Sections[section] = pts
				}
			}
			if len(out.Sections) > 0 {
				return out, nil
			}
			continue
		}
		if len(parsed.Ranges) > 0 {
			if pts := expandRanges(parsed.Ranges); len(pts) > 0 {
				section := "mcq_total"
				if len(prof.CurveSections) > 0 {
					section = prof.CurveSections[0]
				}
				out.Sections[section] = pts
				return out, nil
			}
		}
	}
	return out, fmt.Errorf("curve: no scoring-worksheet page parsed")
}

func parseCurve(raw string) (curveResp, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var r curveResp
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return curveResp{}, err
	}
	return r, nil
}

// expandRanges turns sparse range entries into a per-raw-value point list. If
// ranges overlap, the higher scaled value wins. The runtime scorer clamps to
// the nearest neighbor for raw values outside the supplied range.
func expandRanges(ranges []curveRange) []content.CurvePoint {
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
	maxRaw := 0
	for raw := range scoreFor {
		if raw > maxRaw {
			maxRaw = raw
		}
	}
	out := make([]content.CurvePoint, 0, len(scoreFor))
	for raw := 0; raw <= maxRaw; raw++ {
		if scaled, ok := scoreFor[raw]; ok {
			out = append(out, content.CurvePoint{Raw: raw, Scaled: scaled})
		}
	}
	return out
}
