package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// schemaVersion is bumped whenever the emitted output structure changes in
// a way that should invalidate an existing on-disk import. Bump for any
// content/types.go field change, figure-layout change, etc.
const schemaVersion = 2

// ImportManifest is the per-test cache fingerprint written next to test.json.
// A subsequent Run with byte-identical inputs and matching parameters skips
// the whole pipeline (no LLM calls, no rasterization, no copying).
//
// Inputs is keyed by the role tag ("test", "scoring", "explanation") with
// the value carrying the source path's basename plus the SHA-256 of its
// bytes. We use basename rather than absolute path so the cache survives
// moving the raw PDFs between checkouts.
type ImportManifest struct {
	SchemaVersion int                      `json:"schema_version"`
	ExamType      string                   `json:"exam_type"`
	Slug          string                   `json:"slug"`
	Title         string                   `json:"title"`
	Inputs        map[string]ManifestInput `json:"inputs"`
	Fingerprint   string                   `json:"fingerprint,omitempty"` // free-form: caller's choice (typically the model spec)
	DPIClassify   int                      `json:"dpi_classify"`
	DPIExtract    int                      `json:"dpi_extract"`
	ConsistencyN  int                      `json:"consistency_n"`
	CompletedAtMS int64                    `json:"completed_at_ms"`
}

// ManifestInput is one source PDF's identifier — basename plus content hash.
type ManifestInput struct {
	Basename string `json:"basename"`
	SHA256   string `json:"sha256"`
}

// ManifestPath returns the canonical .import-manifest.json path for a slug.
func ManifestPath(outRoot, examType, slug string) string {
	return filepath.Join(outRoot, examType, slug, ".import-manifest.json")
}

// RawDir returns the per-slug raw/ subdirectory that holds copied source PDFs.
func RawDir(outRoot, examType, slug string) string {
	return filepath.Join(outRoot, examType, slug, "raw")
}

// LoadManifest reads an existing manifest from disk. (m, true, nil) on hit;
// (zero, false, nil) when the file doesn't exist; (zero, false, err) on
// malformed contents.
func LoadManifest(path string) (ImportManifest, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ImportManifest{}, false, nil
		}
		return ImportManifest{}, false, err
	}
	var m ImportManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return ImportManifest{}, false, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return m, true, nil
}

// WriteManifest serializes m into path (creating parent dirs).
func WriteManifest(path string, m ImportManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// computeManifest gathers a manifest for the current Input. It hashes every
// non-empty PDF path; the slug/title and the run-parameter fields fill from
// in.
func computeManifest(in Input) (ImportManifest, error) {
	m := ImportManifest{
		SchemaVersion: schemaVersion,
		ExamType:      in.Profile.ExamType,
		Slug:          in.Slug,
		Title:         in.Title,
		Inputs:        map[string]ManifestInput{},
		Fingerprint:   in.Fingerprint,
		DPIClassify:   in.DPIClassify,
		DPIExtract:    in.DPIExtract,
		ConsistencyN:  in.ConsistencyN,
	}
	for role, path := range map[string]string{
		"test":        in.PDFTest,
		"scoring":     in.PDFScoring,
		"explanation": in.PDFExplanation,
	} {
		if path == "" {
			continue
		}
		sum, err := hashFile(path)
		if err != nil {
			return ImportManifest{}, fmt.Errorf("hash %s: %w", path, err)
		}
		m.Inputs[role] = ManifestInput{Basename: filepath.Base(path), SHA256: sum}
	}
	return m, nil
}

// manifestMatches reports whether the on-disk prior manifest is equivalent to
// what we'd produce now — same schema version, same inputs (hash + role),
// same DPI/consistency/fingerprint. completed_at_ms is ignored.
func manifestMatches(prev, cur ImportManifest) bool {
	if prev.SchemaVersion != cur.SchemaVersion {
		return false
	}
	if prev.ExamType != cur.ExamType || prev.Slug != cur.Slug {
		return false
	}
	if prev.Fingerprint != cur.Fingerprint {
		return false
	}
	if prev.DPIClassify != cur.DPIClassify || prev.DPIExtract != cur.DPIExtract {
		return false
	}
	if prev.ConsistencyN != cur.ConsistencyN {
		return false
	}
	if len(prev.Inputs) != len(cur.Inputs) {
		return false
	}
	roles := make([]string, 0, len(cur.Inputs))
	for r := range cur.Inputs {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		p, ok := prev.Inputs[r]
		if !ok {
			return false
		}
		c := cur.Inputs[r]
		if p.SHA256 != c.SHA256 {
			return false
		}
	}
	return true
}

// CopyRawPDFs copies every non-empty input PDF into <outRoot>/<exam>/<slug>/raw/.
// Copies are skipped when the destination already exists with a matching
// SHA-256 (so re-runs are cheap and don't churn mtimes). Returns the list of
// destination paths in role order (test, scoring, explanation).
func CopyRawPDFs(outRoot, examType, slug string, paths map[string]string) ([]string, error) {
	dstDir := RawDir(outRoot, examType, slug)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	var out []string
	for _, role := range []string{"test", "scoring", "explanation"} {
		src := paths[role]
		if src == "" {
			continue
		}
		dst := filepath.Join(dstDir, filepath.Base(src))
		if existsMatch, _ := filesIdentical(src, dst); existsMatch {
			out = append(out, dst)
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return nil, fmt.Errorf("copy raw %s: %w", src, err)
		}
		out = append(out, dst)
	}
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// filesIdentical returns true when both files exist and have the same
// SHA-256. False on any error (including dst missing) — caller treats those
// as "need to copy".
func filesIdentical(a, b string) (bool, error) {
	sa, errA := hashFile(a)
	if errA != nil {
		return false, errA
	}
	sb, errB := hashFile(b)
	if errB != nil {
		return false, errB
	}
	return sa == sb, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
