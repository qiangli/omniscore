package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	ledong "github.com/ledongthuc/pdf"
)

func init() {
	Register(Registration{
		Name: "ledongthuc",
		Kind: "purego",
		New:  func() TextSource { return ledongthucSource{} },
	})
}

type ledongthucSource struct{}

func (ledongthucSource) Name() string { return "ledongthuc" }

func (ledongthucSource) Pages(ctx context.Context, path string) ([]Page, error) {
	f, r, err := ledong.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n := r.NumPage()
	pages := make([]Page, 0, n)
	for i := 1; i <= n; i++ {
		if err := ctx.Err(); err != nil {
			return pages, err
		}
		p := Page{Index: i}
		func() {
			defer func() {
				if rr := recover(); rr != nil {
					p.Cols = []string{fmt.Sprintf("[page %d panic: %v]\n", i, rr)}
				}
			}()
			page := r.Page(i)
			if page.V.IsNull() {
				return
			}
			ts := page.Content().Text
			leftSpans, rightSpans := splitSpansByX(ts, 306)
			leftText := layoutColumn(leftSpans)
			rightText := layoutColumn(rightSpans)
			if rightText == "" {
				p.Cols = []string{leftText}
			} else {
				p.Cols = []string{leftText, rightText}
			}
		}()
		pages = append(pages, p)
	}
	return pages, nil
}

func splitSpansByX(ts []ledong.Text, mid float64) (left, right []ledong.Text) {
	for _, t := range ts {
		if t.X+t.W/2 < mid {
			left = append(left, t)
		} else {
			right = append(right, t)
		}
	}
	return
}

func layoutColumn(ts []ledong.Text) string {
	if len(ts) == 0 {
		return ""
	}
	sort.SliceStable(ts, func(a, b int) bool {
		if ts[a].Y != ts[b].Y {
			return ts[a].Y > ts[b].Y
		}
		return ts[a].X < ts[b].X
	})
	var (
		lines   []string
		curLine []ledong.Text
		curYTop = ts[0].Y
		curBand = ts[0].FontSize * 0.7
	)
	if curBand < 6 {
		curBand = 6
	}
	flush := func() {
		if len(curLine) == 0 {
			return
		}
		sort.SliceStable(curLine, func(a, b int) bool { return curLine[a].X < curLine[b].X })
		var sb strings.Builder
		prevEnd := -1.0
		for _, t := range curLine {
			if prevEnd > 0 && t.X-prevEnd > t.FontSize*0.3 {
				sb.WriteByte(' ')
			}
			sb.WriteString(t.S)
			prevEnd = t.X + t.W
		}
		lines = append(lines, sb.String())
	}
	for _, t := range ts {
		if curYTop-t.Y > curBand {
			flush()
			curLine = curLine[:0]
			curYTop = t.Y
			curBand = t.FontSize * 0.7
			if curBand < 6 {
				curBand = 6
			}
		}
		curLine = append(curLine, t)
	}
	flush()
	return strings.Join(lines, "\n")
}
