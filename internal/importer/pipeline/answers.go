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

type answerKeyResp struct {
	Answers []struct {
		QuestionNumber int    `json:"question_number"`
		Label          string `json:"label"`
	} `json:"answers"`
}

// ExtractAnswerKey runs the profile's answer-key prompt against every
// classified answer-key page and returns a flat map[questionNumber]label.
// When the same question shows up on multiple pages (rare — keys are usually
// one page) the later page wins.
//
// For exams that print keys per module (profile.Features.PerModuleAnswerKey)
// the caller should split pagePaths by module first and call this function
// once per module.
func ExtractAnswerKey(ctx context.Context, p vision.Provider, prof profile.Profile, mod profile.ModuleSpec, pagePaths []string) (map[int]string, error) {
	prompt := prof.Prompts.AnswerKey(prof, mod)
	valid := map[string]bool{}
	for _, l := range prof.ChoiceLabels {
		valid[l] = true
	}

	out := map[int]string{}
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
			label := normalizeAnswerLabel(a.Label, prof.ChoiceLabels)
			if valid[label] {
				out[a.QuestionNumber] = label
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

func parseAnswerKey(raw string) ([]struct {
	QuestionNumber int    `json:"question_number"`
	Label          string `json:"label"`
}, error) {
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

// ReconcileAnswers fills in AnswerLabel on each ExtractedQuestion from key.
// LLM-vs-key disagreements are noted in ReviewNotes and the key wins.
// Questions missing from the key get NeedsReview=true.
func ReconcileAnswers(qs []ExtractedQuestion, key map[int]string) []ExtractedQuestion {
	out := make([]ExtractedQuestion, len(qs))
	for i, q := range qs {
		out[i] = q
		keyLabel, ok := key[q.QuestionNumber]
		if !ok {
			out[i].NeedsReview = true
			out[i].ReviewNotes = append(out[i].ReviewNotes,
				fmt.Sprintf("answer key does not list question %d", q.QuestionNumber))
			continue
		}
		if q.AnswerLabel != "" && q.AnswerLabel != keyLabel {
			out[i].ReviewNotes = append(out[i].ReviewNotes,
				fmt.Sprintf("answer mismatch: extractor said %s, key says %s (key wins)", q.AnswerLabel, keyLabel))
		}
		out[i].AnswerLabel = keyLabel
	}
	return out
}
