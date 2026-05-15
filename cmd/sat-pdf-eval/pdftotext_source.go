package main

import (
	"context"
	"os/exec"
	"strings"
)

func init() {
	Register(Registration{
		Name: "pdftotext",
		Kind: "shell", // Poppler C library invoked via subprocess
		New:  func() TextSource { return pdftotextSource{} },
	})
}

type pdftotextSource struct{}

func (pdftotextSource) Name() string { return "pdftotext" }

// Uses `pdftotext -layout` (preserves visual columns by inserting whitespace)
// then splits each line at a detected per-page gutter.
func (pdftotextSource) Pages(ctx context.Context, path string) ([]Page, error) {
	out, err := exec.CommandContext(ctx, "pdftotext", "-layout", path, "-").Output()
	if err != nil {
		return nil, err
	}
	rawPages := strings.Split(string(out), "\f")
	pages := make([]Page, 0, len(rawPages))
	for i, raw := range rawPages {
		raw = strings.TrimRight(raw, "\n\r")
		if raw == "" {
			continue
		}
		pages = append(pages, Page{Index: i + 1, Cols: splitTwoColumns(raw)})
	}
	return pages, nil
}

// splitTwoColumns detects a vertical gutter (a column position where most lines
// have a long whitespace run) and splits each line into [left, right]. If no
// gutter is detected (cover / directions page), returns a single column.
func splitTwoColumns(pageText string) []string {
	lines := strings.Split(pageText, "\n")
	const minRun = 8
	colVotes := map[int]int{}
	longLines := 0
	for _, l := range lines {
		if len(l) < 60 {
			continue
		}
		longLines++
		runStart, runLen := -1, 0
		for i, r := range l {
			if r == ' ' {
				if runStart < 0 {
					runStart = i
				}
				runLen = i - runStart + 1
			} else {
				if runLen >= minRun && runStart > 20 {
					colVotes[runStart+runLen/2]++
				}
				runStart, runLen = -1, 0
			}
		}
	}
	gutter, bestVotes := -1, 0
	for c, v := range colVotes {
		if v > bestVotes {
			bestVotes = v
			gutter = c
		}
	}
	if longLines == 0 || bestVotes*100/max(longLines, 1) < 30 {
		return []string{pageText}
	}
	var left, right strings.Builder
	for _, l := range lines {
		if len(l) <= gutter {
			left.WriteString(l)
			left.WriteByte('\n')
			continue
		}
		split := findGap(l, gutter, 15)
		if split < 0 {
			left.WriteString(l)
			left.WriteByte('\n')
			continue
		}
		left.WriteString(strings.TrimRight(l[:split], " "))
		left.WriteByte('\n')
		right.WriteString(strings.TrimLeft(l[split:], " "))
		right.WriteByte('\n')
	}
	return []string{left.String(), right.String()}
}

func findGap(line string, around, wiggle int) int {
	lo := max(0, around-wiggle)
	hi := min(len(line), around+wiggle)
	for i := lo; i < hi; i++ {
		if line[i] == ' ' {
			s := i
			for s > 0 && line[s-1] == ' ' {
				s--
			}
			return s
		}
	}
	return -1
}
