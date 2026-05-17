import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useLocation, useRoute } from "wouter";
import { MD, MDInline } from "../lib/markdown";
import { FigureView } from "../components/Figure";
import { adminApi, type AdminReviewRow } from "../lib/api";
import type { Figure } from "../lib/types";

type AdminChoice = {
  label: string;
  text_md: string;
  figure?: Figure;
};

type AdminQuestion = {
  id: string;
  type?: "mcq" | "spr";
  passage_md?: string;
  passage_figure?: Figure;
  stem_md: string;
  stem_figure?: Figure;
  choices?: AdminChoice[];
  answer_label?: string;
  answer_values?: string[];
  rationale_md?: string;
};

type AdminModule = {
  id: string;
  title: string;
  section: string;
  questions: AdminQuestion[];
};

type AdminTest = {
  slug: string;
  title: string;
  exam_type?: string;
  subject?: string;
  modules: AdminModule[];
};

type ReviewStatus = "approved" | "flagged" | "pending";

// AdminReview presents one question per page (mirrors the exam UI). The
// admin sees what a student would see (passage + figure + stem + choices
// rendered), plus admin-only fields (rationale, answer key). Switch to
// "Edit" to fix typos / wrong answers / passages; the edits write back
// to the on-disk JSON via PATCH and the loader hot-reloads. Review state
// (approved | flagged | pending) is a separate write to SQLite.
export function AdminReview() {
  const [, paramsBare] = useRoute("/admin/review/:slug");
  const [, paramsIdx] = useRoute("/admin/review/:slug/:moduleIdx/:qIdx");
  const [, setLoc] = useLocation();

  const slug = paramsIdx?.slug ?? paramsBare?.slug ?? "";
  const moduleIdx = paramsIdx ? Number(paramsIdx.moduleIdx) : 0;
  const qIdx = paramsIdx ? Number(paramsIdx.qIdx) : 0;

  const [test, setTest] = useState<AdminTest | null>(null);
  const [reviews, setReviews] = useState<Record<string, AdminReviewRow>>({});
  const [err, setErr] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);

  const load = useCallback(async () => {
    try {
      await adminApi.whoami();
    } catch {
      setLoc("/admin/login");
      return;
    }
    try {
      const r = await adminApi.getTest(slug);
      setTest(r.test as AdminTest);
      setReviews(r.reviews);
    } catch (e) {
      setErr(String(e));
    }
  }, [slug, setLoc]);

  useEffect(() => {
    if (slug) load();
  }, [slug, load]);

  // Bare /admin/review/:slug → redirect to /:slug/0/0 once we know the test
  // exists. This keeps deep-linking + back-button friendly.
  useEffect(() => {
    if (test && !paramsIdx) {
      setLoc(`/admin/review/${slug}/0/0`, { replace: true });
    }
  }, [test, paramsIdx, slug, setLoc]);

  // Flatten into a navigable list of (moduleIdx, qIdx) so prev/next is a
  // single increment instead of two-level arithmetic.
  const flat = useMemo(() => {
    if (!test) return [] as { m: number; q: number; question: AdminQuestion; module: AdminModule }[];
    const out: { m: number; q: number; question: AdminQuestion; module: AdminModule }[] = [];
    test.modules.forEach((mod, m) =>
      mod.questions.forEach((q, qi) => out.push({ m, q: qi, question: q, module: mod })),
    );
    return out;
  }, [test]);

  const pos = useMemo(() => {
    return flat.findIndex((x) => x.m === moduleIdx && x.q === qIdx);
  }, [flat, moduleIdx, qIdx]);

  const goto = useCallback(
    (m: number, q: number) => {
      setEditing(false);
      setLoc(`/admin/review/${slug}/${m}/${q}`);
    },
    [setLoc, slug],
  );

  const gotoFlat = useCallback(
    (i: number) => {
      if (i < 0 || i >= flat.length) return;
      const e = flat[i];
      goto(e.m, e.q);
    },
    [flat, goto],
  );

  // Keyboard nav. Skipped while editing so arrows behave naturally in text fields.
  useEffect(() => {
    if (editing) return;
    const onKey = (ev: KeyboardEvent) => {
      if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
      const target = ev.target as HTMLElement | null;
      const tag = target?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA") return;
      if (ev.key === "ArrowLeft") {
        ev.preventDefault();
        gotoFlat(pos - 1);
      } else if (ev.key === "ArrowRight") {
        ev.preventDefault();
        gotoFlat(pos + 1);
      } else if (ev.key.toLowerCase() === "e") {
        ev.preventDefault();
        setEditing(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [editing, gotoFlat, pos]);

  if (!test || pos < 0) {
    return (
      <main className="min-h-screen px-4 py-8 max-w-4xl mx-auto">
        {err ? (
          <div className="text-warn text-sm">{err}</div>
        ) : (
          <div className="text-ink/60 text-sm">Loading…</div>
        )}
      </main>
    );
  }

  const cur = flat[pos];
  const review = reviews[cur.question.id];

  async function setStatus(s: ReviewStatus) {
    try {
      await adminApi.setReview(slug, cur.question.id, s);
      await load();
    } catch (e) {
      setErr(String(e));
    }
  }

  return (
    <main className="min-h-screen px-4 py-6 max-w-3xl mx-auto">
      <header className="flex items-center justify-between mb-4">
        <Link to="/admin/tests" className="text-sm text-ink/60 hover:text-ink">
          ← All tests
        </Link>
        <div className="text-xs text-ink/60">
          {test.title}
          {test.subject ? ` · ${test.subject}` : ""}
        </div>
      </header>

      <div className="flex items-center justify-between mb-4 text-sm">
        <div className="text-ink/70">
          <span className="font-semibold">{cur.module.title}</span>
          <span className="text-ink/40 mx-2">·</span>
          <span className="font-mono">{cur.module.section}</span>
          <span className="text-ink/40 mx-2">·</span>
          <span>
            Q {pos + 1}/{flat.length}
          </span>
        </div>
        <StatusBadge status={review?.status ?? "pending"} />
      </div>

      {err && <div className="mb-3 text-warn text-sm">{err}</div>}

      {editing ? (
        <EditPanel
          slug={slug}
          question={cur.question}
          onDone={async () => {
            setEditing(false);
            await load();
          }}
          onCancel={() => setEditing(false)}
          onError={setErr}
        />
      ) : (
        <QuestionView question={cur.question} />
      )}

      <div className="flex items-center justify-between mt-6 gap-2 flex-wrap">
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => setStatus("approved")}
            className="text-sm rounded bg-green-600 text-white px-3 py-1.5"
          >
            Approve
          </button>
          <button
            type="button"
            onClick={() => setStatus("flagged")}
            className="text-sm rounded bg-warn text-white px-3 py-1.5"
          >
            Flag
          </button>
          <button
            type="button"
            onClick={() => setStatus("pending")}
            className="text-sm rounded border os-rule text-ink/70 px-3 py-1.5"
          >
            Reset
          </button>
          {!editing && (
            <button
              type="button"
              onClick={() => setEditing(true)}
              className="text-sm rounded border os-rule text-ink/70 px-3 py-1.5"
            >
              Edit
            </button>
          )}
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => gotoFlat(pos - 1)}
            disabled={pos <= 0}
            className="text-sm rounded border os-rule px-3 py-1.5 disabled:opacity-40"
          >
            ← Prev
          </button>
          <button
            type="button"
            onClick={() => gotoFlat(pos + 1)}
            disabled={pos >= flat.length - 1}
            className="text-sm rounded border os-rule px-3 py-1.5 disabled:opacity-40"
          >
            Next →
          </button>
        </div>
      </div>

      <Palette flat={flat} reviews={reviews} active={pos} onPick={gotoFlat} />
    </main>
  );
}

function StatusBadge({ status }: { status: ReviewStatus }) {
  const cls =
    status === "approved"
      ? "bg-green-100 text-green-800"
      : status === "flagged"
        ? "bg-red-100 text-warn"
        : "bg-ink/10 text-ink/60";
  return <span className={`text-xs px-2 py-0.5 rounded ${cls}`}>{status}</span>;
}

function QuestionView({ question }: { question: AdminQuestion }) {
  const isSPR = (question.type ?? "mcq") === "spr";
  return (
    <article className="border os-rule rounded-lg p-5 bg-white space-y-4">
      <div className="text-xs font-mono text-ink/50">{question.id}</div>

      {(question.passage_md || question.passage_figure) && (
        <section className="text-sm text-ink/90 border-b os-rule pb-4">
          {question.passage_md && <MD text={question.passage_md} />}
          {question.passage_figure && <FigureView fig={question.passage_figure} />}
        </section>
      )}

      <section className="text-base leading-relaxed">
        <MDInline text={question.stem_md} />
        {question.stem_figure && <FigureView fig={question.stem_figure} />}
      </section>

      {isSPR ? (
        <section>
          <div className="text-xs uppercase tracking-wide text-ink/60 mb-1">
            Accepted answer values
          </div>
          {question.answer_values && question.answer_values.length > 0 ? (
            <ul className="list-disc list-inside text-sm font-mono">
              {question.answer_values.map((v, i) => (
                <li key={i}>{v}</li>
              ))}
            </ul>
          ) : (
            <div className="text-sm text-warn">(none — students cannot answer this)</div>
          )}
        </section>
      ) : (
        <section className="space-y-2">
          {(question.choices ?? []).map((c) => {
            const correct = question.answer_label === c.label;
            return (
              <div
                key={c.label}
                className={
                  "rounded-lg border os-rule px-3 py-2 flex items-start gap-3 " +
                  (correct ? "border-green-600 bg-green-50" : "")
                }
              >
                <span
                  className={
                    "inline-flex items-center justify-center h-6 w-6 rounded-full border text-xs font-semibold shrink-0 " +
                    (correct
                      ? "bg-green-600 text-white border-green-600"
                      : "border-ink/30 text-ink/70")
                  }
                >
                  {c.label}
                </span>
                <span className="flex-1 text-sm leading-relaxed">
                  <MDInline text={c.text_md} />
                  {c.figure && <FigureView fig={c.figure} />}
                </span>
                {correct && <span className="text-xs text-green-700 font-semibold">correct</span>}
              </div>
            );
          })}
          {(question.choices ?? []).length === 0 && (
            <div className="text-sm text-warn">(no choices — students cannot answer this)</div>
          )}
        </section>
      )}

      {question.rationale_md && (
        <section className="border-t os-rule pt-3">
          <div className="text-xs uppercase tracking-wide text-ink/60 mb-1">
            Rationale (admin-only)
          </div>
          <div className="text-sm text-ink/80">
            <MD text={question.rationale_md} />
          </div>
        </section>
      )}
    </article>
  );
}

function EditPanel({
  slug,
  question,
  onDone,
  onCancel,
  onError,
}: {
  slug: string;
  question: AdminQuestion;
  onDone: () => Promise<void>;
  onCancel: () => void;
  onError: (e: string) => void;
}) {
  const isSPR = (question.type ?? "mcq") === "spr";
  const [passage, setPassage] = useState(question.passage_md ?? "");
  const [stem, setStem] = useState(question.stem_md);
  const [answerLabel, setAnswerLabel] = useState(question.answer_label ?? "");
  const [answerValues, setAnswerValues] = useState(
    (question.answer_values ?? []).join("\n"),
  );
  const [rationale, setRationale] = useState(question.rationale_md ?? "");
  const [choices, setChoices] = useState(question.choices ?? []);
  const [busy, setBusy] = useState(false);

  async function save() {
    if (busy) return;
    setBusy(true);
    try {
      const body: Record<string, unknown> = {};
      if (passage !== (question.passage_md ?? "")) body.passage_md = passage;
      if (stem !== question.stem_md) body.stem_md = stem;
      if (!isSPR && answerLabel !== (question.answer_label ?? "")) {
        body.answer_label = answerLabel;
      }
      if (rationale !== (question.rationale_md ?? "")) body.rationale_md = rationale;
      if (isSPR) {
        const next = answerValues
          .split("\n")
          .map((s) => s.trim())
          .filter(Boolean);
        const prev = question.answer_values ?? [];
        if (next.length !== prev.length || next.some((v, i) => v !== prev[i])) {
          body.answer_values = next;
        }
      }
      const choiceEdits: Record<string, { text_md?: string }> = {};
      (question.choices ?? []).forEach((orig, i) => {
        const cur = choices[i];
        if (cur && cur.text_md !== orig.text_md) {
          choiceEdits[orig.label] = { text_md: cur.text_md };
        }
      });
      if (Object.keys(choiceEdits).length > 0) body.choice_edits = choiceEdits;
      if (Object.keys(body).length === 0) {
        onCancel();
        return;
      }
      await adminApi.patchQuestion(slug, question.id, body);
      await onDone();
    } catch (e) {
      onError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <article className="border os-rule rounded-lg p-5 bg-chrome/30 space-y-4">
      <div className="text-xs font-mono text-ink/50">{question.id}</div>

      {(question.passage_md || question.passage_figure) && (
        <Field label="Passage (Markdown)">
          <textarea
            value={passage}
            onChange={(e) => setPassage(e.target.value)}
            rows={5}
            className="w-full rounded border os-rule px-2 py-1 text-sm font-mono bg-white"
          />
        </Field>
      )}

      <Field label="Stem (Markdown)">
        <textarea
          value={stem}
          onChange={(e) => setStem(e.target.value)}
          rows={3}
          className="w-full rounded border os-rule px-2 py-1 text-sm font-mono bg-white"
        />
      </Field>

      {isSPR ? (
        <Field label="Accepted answer values (one per line)">
          <textarea
            value={answerValues}
            onChange={(e) => setAnswerValues(e.target.value)}
            rows={4}
            className="w-full rounded border os-rule px-2 py-1 text-sm font-mono bg-white"
            placeholder="0.5&#10;1/2&#10;.5"
          />
        </Field>
      ) : (
        <Field label="Choices">
          <div className="space-y-1">
            {choices.map((c, i) => (
              <div key={c.label} className="flex items-center gap-2">
                <span className="font-mono text-xs w-4">{c.label}.</span>
                <input
                  value={c.text_md}
                  onChange={(e) => {
                    const next = choices.slice();
                    next[i] = { ...c, text_md: e.target.value };
                    setChoices(next);
                  }}
                  className="flex-1 rounded border os-rule px-2 py-1 text-sm bg-white"
                />
                <label className="text-xs flex items-center gap-1">
                  <input
                    type="radio"
                    name={"ans-" + question.id}
                    checked={answerLabel === c.label}
                    onChange={() => setAnswerLabel(c.label)}
                  />
                  correct
                </label>
              </div>
            ))}
          </div>
        </Field>
      )}

      <Field label="Rationale (admin-only, Markdown)">
        <textarea
          value={rationale}
          onChange={(e) => setRationale(e.target.value)}
          rows={3}
          className="w-full rounded border os-rule px-2 py-1 text-sm font-mono bg-white"
        />
      </Field>

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={save}
          disabled={busy}
          className="text-sm rounded bg-blue-600 text-white px-3 py-1.5 disabled:opacity-50"
        >
          {busy ? "Saving…" : "Save"}
        </button>
        <button
          type="button"
          onClick={onCancel}
          disabled={busy}
          className="text-sm rounded border os-rule text-ink/70 px-3 py-1.5"
        >
          Cancel
        </button>
      </div>
    </article>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="block text-xs uppercase tracking-wide text-ink/60 mb-1">
        {label}
      </span>
      {children}
    </label>
  );
}

// Compact strip of question pills, color-coded by review status. Click to jump.
function Palette({
  flat,
  reviews,
  active,
  onPick,
}: {
  flat: { m: number; q: number; question: AdminQuestion; module: AdminModule }[];
  reviews: Record<string, AdminReviewRow>;
  active: number;
  onPick: (i: number) => void;
}) {
  return (
    <div className="mt-6 pt-4 border-t os-rule">
      <div className="text-xs text-ink/60 mb-2">Jump to:</div>
      <div className="flex flex-wrap gap-1">
        {flat.map((e, i) => {
          const s = reviews[e.question.id]?.status ?? "pending";
          const base =
            s === "approved"
              ? "bg-green-100 text-green-800 border-green-300"
              : s === "flagged"
                ? "bg-red-100 text-warn border-red-300"
                : "bg-white text-ink/60 border-ink/20";
          const ring = i === active ? "ring-2 ring-blue-500" : "";
          return (
            <button
              key={`${e.m}-${e.q}`}
              type="button"
              onClick={() => onPick(i)}
              className={`text-xs font-mono px-2 py-1 rounded border ${base} ${ring}`}
              title={`${e.module.title} · ${e.question.id}`}
            >
              {i + 1}
            </button>
          );
        })}
      </div>
    </div>
  );
}
