package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qiangli/omniscore/internal/importer/vision"
)

const answerKeyPrompt = `You are looking at the multiple-choice answer key page of an AP Calculus BC released exam.

Return ONE JSON object, no prose, no Markdown code fence:

{
  "answers": [
    {"question_number": 1, "label": "A"},
    {"question_number": 2, "label": "C"},
    ...
  ]
}

Rules:
- "label" must be one of A, B, C, D, E.
- Include every numbered question listed on the page.
- If a number is unreadable, omit that entry.
- Output ONLY the JSON object. No prose.`

type answerKeyResp struct {
	Answers []struct {
		QuestionNumber int    `json:"question_number"`
		Label          string `json:"label"`
	} `json:"answers"`
}

// ExtractAnswerKey runs the answer-key prompt against every classified
// answer-key page and returns the merged map[questionNumber]label. If two
// pages disagree on a question (rare — answer keys are usually one page)
// the later page wins.
func ExtractAnswerKey(ctx context.Context, p vision.Provider, pagePaths []string) (map[int]string, error) {
	out := map[int]string{}
	for _, page := range pagePaths {
		img, err := os.ReadFile(page)
		if err != nil {
			return nil, fmt.Errorf("answers: read %s: %w", page, err)
		}
		raw, err := p.Generate(ctx, vision.Request{
			Image:       img,
			Prompt:      answerKeyPrompt,
			Temperature: 0.0,
		})
		if err != nil {
			return nil, fmt.Errorf("answers: %s: %w", page, err)
		}
		parsed, perr := parseAnswerKey(raw)
		if perr != nil {
			// One bad page shouldn't kill the whole import; skip and let the
			// review log flag it.
			continue
		}
		for _, a := range parsed {
			label := strings.ToUpper(strings.TrimSpace(a.Label))
			if len(label) == 1 && label[0] >= 'A' && label[0] <= 'E' {
				out[a.QuestionNumber] = label
			}
		}
	}
	return out, nil
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
// Discrepancies (LLM said A, key says B) are noted in ReviewNotes and the
// key wins. Questions missing from the key get NeedsReview=true.
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
