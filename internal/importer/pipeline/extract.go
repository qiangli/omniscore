package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

// ExtractedChoice is one option as the LLM saw it. Label is whatever the
// model emitted; the caller validates against profile.ChoiceLabels.
type ExtractedChoice struct {
	Label     string `json:"label"`
	TextMD    string `json:"text_md"`
	HasFigure bool   `json:"has_figure"`
}

// ExtractedQuestion is one question pulled from one page image. AnswerLabel
// (MCQ) / AnswerValues (SPR) stay blank until ReconcileAnswers fills them in.
// PassageMD/HasPassageFigure stay empty unless the profile's ModuleSpec sets
// AcceptsPassage. Type defaults to "mcq" when blank.
type ExtractedQuestion struct {
	PageSource       string            `json:"page_source"`
	QuestionNumber   int               `json:"question_number"`
	Type             string            `json:"type,omitempty"` // "" → "mcq"; "spr" for student-produced response
	StemMD           string            `json:"stem_md"`
	HasStemFigure    bool              `json:"has_stem_figure"`
	PassageMD        string            `json:"passage_md,omitempty"`
	HasPassageFigure bool              `json:"has_passage_figure,omitempty"`
	Choices          []ExtractedChoice `json:"choices,omitempty"`
	AnswerLabel      string            `json:"answer_label,omitempty"`
	AnswerValues     []string          `json:"answer_values,omitempty"`
	NeedsReview      bool              `json:"needs_review"`
	ReviewNotes      []string          `json:"review_notes,omitempty"`
	RawResponses     []string          `json:"raw_responses,omitempty"`
}

// EffectiveType returns the question type with the "" → "mcq" default applied.
func (q ExtractedQuestion) EffectiveType() string {
	if q.Type == "" {
		return "mcq"
	}
	return q.Type
}

type extractResp struct {
	Questions []ExtractedQuestion `json:"questions"`
}

// ExtractPage runs N self-consistency rounds against one page image, then
// merges the rounds into a consolidated question list. The prompt comes from
// prof.Prompts.Extract(prof, mod) so it can vary by exam and module.
func ExtractPage(ctx context.Context, p vision.Provider, prof profile.Profile, mod profile.ModuleSpec, pagePath string, n int) ([]ExtractedQuestion, error) {
	if n < 1 {
		n = 1
	}
	prompt := prof.Prompts.Extract(prof, mod)
	img, err := os.ReadFile(pagePath)
	if err != nil {
		return nil, fmt.Errorf("extract: read %s: %w", pagePath, err)
	}

	rounds := make([][]ExtractedQuestion, 0, n)
	rawTexts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw, err := p.Generate(ctx, vision.Request{
			Image:       img,
			Prompt:      prompt,
			Temperature: 0.2,
		})
		if err != nil {
			return nil, fmt.Errorf("extract: round %d: %w", i, err)
		}
		rawTexts = append(rawTexts, raw)
		parsed, perr := parseExtractResp(raw)
		if perr != nil {
			continue
		}
		rounds = append(rounds, parsed)
	}
	if len(rounds) == 0 {
		return []ExtractedQuestion{{
			PageSource:   pagePath,
			NeedsReview:  true,
			ReviewNotes:  []string{"all extraction rounds returned unparseable JSON"},
			RawResponses: rawTexts,
		}}, nil
	}

	expectedChoices := len(prof.ChoiceLabels)
	return mergeRounds(rounds, pagePath, rawTexts, expectedChoices), nil
}

func parseExtractResp(raw string) ([]ExtractedQuestion, error) {
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
func mergeRounds(rounds [][]ExtractedQuestion, pagePath string, raws []string, expectedChoices int) []ExtractedQuestion {
	byNum := map[int][]ExtractedQuestion{}
	for _, round := range rounds {
		for _, q := range round {
			byNum[q.QuestionNumber] = append(byNum[q.QuestionNumber], q)
		}
	}
	nums := make([]int, 0, len(byNum))
	for n := range byNum {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	out := make([]ExtractedQuestion, 0, len(nums))
	for _, n := range nums {
		obs := byNum[n]
		merged := voteOne(obs, expectedChoices)
		merged.QuestionNumber = n
		merged.PageSource = pagePath
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

func voteOne(obs []ExtractedQuestion, expectedChoices int) ExtractedQuestion {
	// Majority vote on question type first; SPR observations skip the choice
	// merge entirely.
	typeVotes := map[string]int{}
	for _, o := range obs {
		typeVotes[o.EffectiveType()]++
	}
	bestType, bestTypeN := "mcq", 0
	for t, n := range typeVotes {
		if n > bestTypeN {
			bestType, bestTypeN = t, n
		}
	}

	counts := map[int]int{}
	for _, o := range obs {
		counts[len(o.Choices)]++
	}
	bestCount, bestN := expectedChoices, 0
	for c, n := range counts {
		if n > bestN {
			bestCount, bestN = c, n
		}
	}
	if bestType == "spr" {
		bestCount = 0
	}

	var seed ExtractedQuestion
	for _, o := range obs {
		if o.EffectiveType() == bestType && len(o.Choices) == bestCount {
			seed = o
			break
		}
	}
	if seed.QuestionNumber == 0 && len(obs) > 0 {
		seed = obs[0]
	}
	seed.Type = bestType
	if bestType == "mcq" {
		seed.Type = ""
	}

	stems := make([]string, 0, len(obs))
	for _, o := range obs {
		stems = append(stems, o.StemMD)
	}
	seed.StemMD = medianString(stems)

	stemFigVotes := 0
	for _, o := range obs {
		if o.HasStemFigure {
			stemFigVotes++
		}
	}
	seed.HasStemFigure = stemFigVotes*2 > len(obs)

	// Passage handling — only relevant when the calling module accepts passages.
	// We always run the vote so empty rounds still produce empty merged fields.
	passages := make([]string, 0, len(obs))
	for _, o := range obs {
		passages = append(passages, o.PassageMD)
	}
	seed.PassageMD = medianString(passages)
	passageFigVotes := 0
	for _, o := range obs {
		if o.HasPassageFigure {
			passageFigVotes++
		}
	}
	seed.HasPassageFigure = passageFigVotes*2 > len(obs)

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

	if len(typeVotes) > 1 {
		seed.NeedsReview = true
		seed.ReviewNotes = append(seed.ReviewNotes,
			fmt.Sprintf("rounds disagree on question type: %v", typeVotes))
	}
	if bestType != "spr" && len(counts) > 1 {
		seed.NeedsReview = true
		seed.ReviewNotes = append(seed.ReviewNotes,
			fmt.Sprintf("rounds disagree on choice count: %v", counts))
	}
	return seed
}

// medianString returns the element of ss with the lowest total Levenshtein
// distance to the others. Returns "" for empty input; the first element for
// 1- or 2-element slices.
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
