package vision_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/qiangli/omniscore/internal/importer/vision"
)

type mockProvider struct {
	calls atomic.Int32
	resp  string
	err   error
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Generate(_ context.Context, _ vision.Request) (string, error) {
	m.calls.Add(1)
	return m.resp, m.err
}

func TestCachedHitMiss(t *testing.T) {
	dir := t.TempDir()
	mp := &mockProvider{resp: `{"ok":true}`}
	p, err := vision.Cached(mp, dir)
	if err != nil {
		t.Fatal(err)
	}

	req := vision.Request{Image: []byte("png-bytes"), Prompt: "extract", Temperature: 0.2}

	// First call → miss, hits the inner provider.
	out1, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if out1 != `{"ok":true}` || mp.calls.Load() != 1 {
		t.Fatalf("first call: out=%q calls=%d", out1, mp.calls.Load())
	}

	// Second call with identical request → hit, no inner call.
	out2, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if out2 != out1 || mp.calls.Load() != 1 {
		t.Fatalf("cache miss when hit expected: out=%q calls=%d", out2, mp.calls.Load())
	}

	// Different prompt → miss again.
	req2 := req
	req2.Prompt = "extract differently"
	if _, err := p.Generate(context.Background(), req2); err != nil {
		t.Fatal(err)
	}
	if mp.calls.Load() != 2 {
		t.Fatalf("expected 2 inner calls for distinct prompts, got %d", mp.calls.Load())
	}
}
