package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

// AnswerKeyEntry is one row of the answer key. For MCQ items only Label is
// populated; for student-produced response items only Values is populated.
type AnswerKeyEntry struct {
	QuestionNumber int      `json:"question_number"`
	Label          string   `json:"label,omitempty"`
	Values         []string `json:"values,omitempty"`
}

type answerKeyResp struct {
	Answers []AnswerKeyEntry `json:"answers"`
}

// ExtractAnswerKey runs the profile's answer-key prompt against every
// classified answer-key page and returns the flat map of entries keyed by
// question number. Each entry carries either a normalized choice Label
// (MCQ) or a Values slice (SPR / student-produced response).
//
// When the same question shows up on multiple pages (rare — keys are usually
// one page) the later page wins.
//
// For exams that print keys per module (profile.Features.PerModuleAnswerKey)
// the caller should split pagePaths by module first and call this function
// once per module.
func ExtractAnswerKey(ctx context.Context, p vision.Provider, prof profile.Profile, mod profile.ModuleSpec, pagePaths []string) (map[int]AnswerKeyEntry, error) {
	prompt := prof.Prompts.AnswerKey(prof, mod)
	valid := map[string]bool{}
	for _, l := range prof.ChoiceLabels {
		valid[l] = true
	}

	out := map[int]AnswerKeyEntry{}
	for _, page := range pagePaths {
		img, err := os.ReadFile(page)
		if err != nil {
			return nil, fmt.Errorf("answers: read %s: %w", page, err)
		}
		raw, err := p.Generate(ctx, vision.Request{
			Image:       img,
			Prompt:      prompt,
			Temperature: 0.0,
		})
		if err != nil {
			return nil, fmt.Errorf("answers: %s: %w", page, err)
		}
		parsed, perr := parseAnswerKey(raw)
		if perr != nil {
			continue
		}
		for _, a := range parsed {
			if len(a.Values) > 0 {
				cleaned := make([]string, 0, len(a.Values))
				for _, v := range a.Values {
					v = strings.TrimSpace(v)
					if v != "" {
						cleaned = append(cleaned, v)
					}
				}
				if len(cleaned) > 0 {
					out[a.QuestionNumber] = AnswerKeyEntry{
						QuestionNumber: a.QuestionNumber,
						Values:         cleaned,
					}
					continue
				}
			}
			label := normalizeAnswerLabel(a.Label, prof.ChoiceLabels)
			if valid[label] {
				out[a.QuestionNumber] = AnswerKeyEntry{
					QuestionNumber: a.QuestionNumber,
					Label:          label,
				}
			}
		}
	}
	return out, nil
}

// normalizeAnswerLabel accepts either a letter ("A", "b") or a 1-based index
// ("1", "2", "3", "4") and returns the canonical uppercase letter when it
// maps to a known choice slot. Empty string for anything unrecognized.
func normalizeAnswerLabel(raw string, labels []string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	for _, l := range labels {
		if s == l {
			return l
		}
	}
	// Numeric fallback: "1" → labels[0] etc.
	if len(s) <= 2 {
		n := 0
		for _, r := range s {
			if r < '0' || r > '9' {
				return ""
			}
			n = n*10 + int(r-'0')
		}
		if n >= 1 && n <= len(labels) {
			return labels[n-1]
		}
	}
	return ""
}

func parseAnswerKey(raw string) ([]AnswerKeyEntry, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var r answerKeyResp
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, err
	}
	return r.Answers, nil
}

// ReconcileAnswers fills in AnswerLabel or AnswerValues on each
// ExtractedQuestion from key, and aligns each question's Type to match
// what the key says (so an MCQ extractor mistakenly classifying an SPR
// row as MCQ ends up correctly typed). LLM-vs-key disagreements are noted
// in ReviewNotes and the key wins. Questions missing from the key get
// NeedsReview=true.
func ReconcileAnswers(qs []ExtractedQuestion, key map[int]AnswerKeyEntry) []ExtractedQuestion {
	out := make([]ExtractedQuestion, len(qs))
	for i, q := range qs {
		out[i] = q
		entry, ok := key[q.QuestionNumber]
		if !ok {
			out[i].NeedsReview = true
			out[i].ReviewNotes = append(out[i].ReviewNotes,
				fmt.Sprintf("answer key does not list question %d", q.QuestionNumber))
			continue
		}
		switch {
		case len(entry.Values) > 0:
			if q.EffectiveType() != "spr" {
				out[i].ReviewNotes = append(out[i].ReviewNotes,
					fmt.Sprintf("type mismatch: extractor said %q, key shows SPR values (key wins)", q.EffectiveType()))
				out[i].NeedsReview = true
			}
			out[i].Type = "spr"
			out[i].AnswerValues = entry.Values
			out[i].AnswerLabel = ""
		case entry.Label != "":
			if q.EffectiveType() == "spr" {
				out[i].ReviewNotes = append(out[i].ReviewNotes,
					fmt.Sprintf("type mismatch: extractor said spr, key shows label %q (key wins)", entry.Label))
				out[i].NeedsReview = true
				out[i].Type = ""
			}
			if q.AnswerLabel != "" && q.AnswerLabel != entry.Label {
				out[i].ReviewNotes = append(out[i].ReviewNotes,
					fmt.Sprintf("answer mismatch: extractor said %s, key says %s (key wins)", q.AnswerLabel, entry.Label))
			}
			out[i].AnswerLabel = entry.Label
			out[i].AnswerValues = nil
		}
	}
	return out
}
