package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/qiangli/omniscore/internal/importer/vision"
)

// ExtractedChoice is one A–E option as the LLM saw it.
type ExtractedChoice struct {
	Label     string `json:"label"`
	TextMD    string `json:"text_md"`
	HasFigure bool   `json:"has_figure"`
}

// ExtractedQuestion is one MCQ pulled from one page image. AnswerLabel is
// blank at this stage; the answer-key reconciliation pass fills it in.
type ExtractedQuestion struct {
	PageSource     string            `json:"page_source"` // path to the page PNG
	QuestionNumber int               `json:"question_number"`
	StemMD         string            `json:"stem_md"`
	HasStemFigure  bool              `json:"has_stem_figure"`
	Choices        []ExtractedChoice `json:"choices"`
	AnswerLabel    string            `json:"answer_label,omitempty"` // filled by ReconcileAnswers
	NeedsReview    bool              `json:"needs_review"`
	ReviewNotes    []string          `json:"review_notes,omitempty"` // why a human should look
	RawResponses   []string          `json:"raw_responses,omitempty"`
}

// extractPrompt asks for strict JSON with no prose. The schema is described
// inline; Ollama vision models honor "respond with JSON" reliably enough that
// we don't bother with format-mode flags.
const extractPrompt = `You are extracting AP Calculus BC multiple-choice questions from a single page image of a College Board released practice exam.

Return ONE JSON object, no prose, no Markdown code fence, with this exact shape:

{
  "questions": [
    {
      "question_number": <int>,
      "stem_md": "<question text in Markdown; math in $...$ KaTeX, e.g. $\\int_0^1 x^2 \\, dx$>",
      "has_stem_figure": <true if the stem references a graph/figure printed on this page, else false>,
      "choices": [
        {"label": "A", "text_md": "<...>", "has_figure": <bool>},
        {"label": "B", "text_md": "<...>", "has_figure": <bool>},
        {"label": "C", "text_md": "<...>", "has_figure": <bool>},
        {"label": "D", "text_md": "<...>", "has_figure": <bool>},
        {"label": "E", "text_md": "<...>", "has_figure": <bool>}
      ]
    }
  ]
}

Rules:
- Exactly 5 choices labeled A–E (some 2014–2017 exams use only A–D; pad with empty E if needed and set "has_figure": false on E).
- All math expressions MUST use KaTeX-compatible $...$ delimiters. Use \\dfrac for prominent fractions.
- Preserve original wording verbatim — do not paraphrase or summarize.
- If a question is illegible or partially cut off, include it anyway with whatever text you can read.
- If the page has NO multiple-choice questions, return {"questions": []}.
- Output ONLY the JSON object. No explanation. No markdown fence.`

type extractResp struct {
	Questions []ExtractedQuestion `json:"questions"`
}

// ExtractPage runs N self-consistency rounds against one page image and
// returns the consolidated set of questions on that page. N >= 1.
func ExtractPage(ctx context.Context, p vision.Provider, pagePath string, n int) ([]ExtractedQuestion, error) {
	if n < 1 {
		n = 1
	}
	img, err := os.ReadFile(pagePath)
	if err != nil {
		return nil, fmt.Errorf("extract: read %s: %w", pagePath, err)
	}

	rounds := make([][]ExtractedQuestion, 0, n)
	rawTexts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw, err := p.Generate(ctx, vision.Request{
			Image:       img,
			Prompt:      extractPrompt,
			Temperature: 0.2,
		})
		if err != nil {
			return nil, fmt.Errorf("extract: round %d: %w", i, err)
		}
		rawTexts = append(rawTexts, raw)
		parsed, perr := parseExtractResp(raw)
		if perr != nil {
			// Skip unparseable rounds; vote works on whatever did parse.
			continue
		}
		rounds = append(rounds, parsed)
	}
	if len(rounds) == 0 {
		// All N rounds failed to parse. Surface ONE entry flagged for human
		// review rather than dropping the page silently.
		return []ExtractedQuestion{{
			PageSource:   pagePath,
			NeedsReview:  true,
			ReviewNotes:  []string{"all extraction rounds returned unparseable JSON"},
			RawResponses: rawTexts,
		}}, nil
	}

	merged := mergeRounds(rounds, pagePath, rawTexts)
	return merged, nil
}

func parseExtractResp(raw string) ([]ExtractedQuestion, error) {
	// Strip a markdown code fence if the model added one despite instructions.
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var er extractResp
	if err := json.Unmarshal([]byte(s), &er); err != nil {
		return nil, err
	}
	return er.Questions, nil
}

// mergeRounds groups extracted questions across rounds by question_number,
// then per question takes:
//   - majority vote on len(choices), choice labels, has_*_figure flags
//   - the median (closest-to-others) text for stem_md and each choice text_md
//
// Discrepancies are recorded into ReviewNotes so a human can spot-check.
func mergeRounds(rounds [][]ExtractedQuestion, pagePath string, raws []string) []ExtractedQuestion {
	// Group all observations by question_number.
	byNum := map[int][]ExtractedQuestion{}
	for _, round := range rounds {
		for _, q := range round {
			byNum[q.QuestionNumber] = append(byNum[q.QuestionNumber], q)
		}
	}
	// Stable order: by question_number ascending.
	nums := make([]int, 0, len(byNum))
	for n := range byNum {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	out := make([]ExtractedQuestion, 0, len(nums))
	for _, n := range nums {
		obs := byNum[n]
		merged := voteOne(obs)
		merged.QuestionNumber = n
		merged.PageSource = pagePath
		// If only some rounds saw this question, that's worth flagging.
		if len(obs) < len(rounds) {
			merged.NeedsReview = true
			merged.ReviewNotes = append(merged.ReviewNotes,
				fmt.Sprintf("question_number %d only seen in %d/%d rounds", n, len(obs), len(rounds)))
		}
		merged.RawResponses = raws
		out = append(out, merged)
	}
	return out
}

func voteOne(obs []ExtractedQuestion) ExtractedQuestion {
	// Pick the majority choice-count (5 expected; some questions in legacy
	// exams use 4). Then pick the round whose count matches as the seed.
	counts := map[int]int{}
	for _, o := range obs {
		counts[len(o.Choices)]++
	}
	bestCount, bestN := 5, 0
	for c, n := range counts {
		if n > bestN {
			bestCount, bestN = c, n
		}
	}

	var seed ExtractedQuestion
	for _, o := range obs {
		if len(o.Choices) == bestCount {
			seed = o
			break
		}
	}

	// Median text for stem_md.
	stems := make([]string, 0, len(obs))
	for _, o := range obs {
		stems = append(stems, o.StemMD)
	}
	seed.StemMD = medianString(stems)

	// has_stem_figure: majority OR (more conservative).
	stemFigVotes := 0
	for _, o := range obs {
		if o.HasStemFigure {
			stemFigVotes++
		}
	}
	seed.HasStemFigure = stemFigVotes*2 > len(obs)

	// For each choice slot up to bestCount, take the median text and OR-vote on figure.
	choices := make([]ExtractedChoice, bestCount)
	for i := 0; i < bestCount; i++ {
		texts := make([]string, 0, len(obs))
		figVotes := 0
		seenLabels := map[string]int{}
		for _, o := range obs {
			if i >= len(o.Choices) {
				continue
			}
			texts = append(texts, o.Choices[i].TextMD)
			if o.Choices[i].HasFigure {
				figVotes++
			}
			seenLabels[o.Choices[i].Label]++
		}
		// Most-common label at this position.
		var bestLabel string
		var bestLabelN int
		for lbl, n := range seenLabels {
			if n > bestLabelN {
				bestLabel, bestLabelN = lbl, n
			}
		}
		if bestLabel == "" {
			bestLabel = string(rune('A' + i))
		}
		choices[i] = ExtractedChoice{
			Label:     bestLabel,
			TextMD:    medianString(texts),
			HasFigure: figVotes*2 > len(obs),
		}
	}
	seed.Choices = choices

	// Disagreement check: if any pair of rounds disagrees on len(choices), flag it.
	if len(counts) > 1 {
		seed.NeedsReview = true
		seed.ReviewNotes = append(seed.ReviewNotes,
			fmt.Sprintf("rounds disagree on choice count: %v", counts))
	}
	return seed
}

// medianString returns the element of ss with the lowest total Levenshtein
// distance to the others — i.e. the most "central" string. Returns "" for an
// empty slice. For 1 or 2 elements just returns the first.
func medianString(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	if len(ss) <= 2 {
		return ss[0]
	}
	bestIdx, bestSum := 0, math.MaxInt
	for i, a := range ss {
		sum := 0
		for j, b := range ss {
			if i == j {
				continue
			}
			sum += editDistance(a, b)
			if sum >= bestSum {
				break
			}
		}
		if sum < bestSum {
			bestSum = sum
			bestIdx = i
		}
	}
	return ss[bestIdx]
}

// editDistance computes the Levenshtein distance between a and b. Iterative,
// O(len(a)*len(b)) time, O(min(len(a), len(b))) space.
func editDistance(a, b string) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
