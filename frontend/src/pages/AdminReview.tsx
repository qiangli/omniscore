import { useCallback, useEffect, useState } from "react";
import { Link, useLocation, useRoute } from "wouter";
import { adminApi, type AdminReviewRow } from "../lib/api";

type Choice = { label: string; text_md: string };
type Question = {
  id: string;
  stem_md: string;
  choices?: Choice[];
  answer_label?: string;
  passage_figure?: { src: string; alt?: string };
  stem_figure?: { src: string; alt?: string };
};
type Module = { id: string; title: string; section: string; questions: Question[] };
type Test = { slug: string; title: string; modules: Module[] };

// Per-question lightweight fixer. Edits write back to the on-disk JSON
// via PATCH; review state lives in SQLite via PUT/POST. The two surfaces
// are independent: flagging a question doesn't change the JSON, and
// editing a stem doesn't auto-approve it.
export function AdminReview() {
  const [, params] = useRoute("/admin/review/:slug");
  const [, setLoc] = useLocation();
  const slug = params?.slug ?? "";
  const [test, setTest] = useState<Test | null>(null);
  const [reviews, setReviews] = useState<Record<string, AdminReviewRow>>({});
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      await adminApi.whoami();
    } catch {
      setLoc("/admin/login");
      return;
    }
    try {
      const r = await adminApi.getTest(slug);
      setTest(r.test as Test);
      setReviews(r.reviews);
    } catch (e) {
      setErr(String(e));
    }
  }, [slug, setLoc]);

  useEffect(() => {
    if (slug) load();
  }, [slug, load]);

  async function bulk(status: "approved" | "flagged" | "pending") {
    if (busy) return;
    setBusy(true);
    try {
      await adminApi.bulkReview(slug, status);
      await load();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!test) {
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

  return (
    <main className="min-h-screen px-4 py-8 max-w-4xl mx-auto">
      <header className="flex items-center justify-between mb-2">
        <Link to="/admin/tests" className="text-sm text-ink/60 hover:text-ink">
          ← All tests
        </Link>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => bulk("approved")}
            disabled={busy}
            className="text-sm rounded bg-green-600 text-white px-3 py-1.5"
          >
            Approve all
          </button>
          <button
            type="button"
            onClick={() => bulk("flagged")}
            disabled={busy}
            className="text-sm rounded bg-warn text-white px-3 py-1.5"
          >
            Flag all
          </button>
          <button
            type="button"
            onClick={() => bulk("pending")}
            disabled={busy}
            className="text-sm rounded bg-ink/60 text-white px-3 py-1.5"
          >
            Reset
          </button>
        </div>
      </header>
      <h1 className="text-2xl font-semibold mb-1">{test.title}</h1>
      <p className="text-xs text-ink/60 mb-6">{test.slug}</p>
      {err && <div className="mb-4 text-warn text-sm">{err}</div>}

      <div className="space-y-8">
        {test.modules.map((m) => (
          <section key={m.id}>
            <h2 className="text-sm font-semibold text-ink/70 uppercase tracking-wide mb-3">
              {m.title} ({m.section})
            </h2>
            <ul className="space-y-4">
              {m.questions.map((q) => (
                <QuestionCard
                  key={q.id}
                  slug={slug}
                  question={q}
                  review={reviews[q.id]}
                  onChange={load}
                  setErr={setErr}
                />
              ))}
            </ul>
          </section>
        ))}
      </div>
    </main>
  );
}

function QuestionCard({
  slug,
  question,
  review,
  onChange,
  setErr,
}: {
  slug: string;
  question: Question;
  review?: AdminReviewRow;
  onChange: () => void;
  setErr: (e: string) => void;
}) {
  const [stem, setStem] = useState(question.stem_md);
  const [answer, setAnswer] = useState(question.answer_label ?? "");
  const [choices, setChoices] = useState(question.choices ?? []);
  const [busy, setBusy] = useState(false);
  const status = review?.status ?? "pending";

  // Reset local state when the parent re-fetches the test.
  useEffect(() => {
    setStem(question.stem_md);
    setAnswer(question.answer_label ?? "");
    setChoices(question.choices ?? []);
  }, [question]);

  async function save() {
    if (busy) return;
    setBusy(true);
    try {
      const body: Record<string, unknown> = {};
      if (stem !== question.stem_md) body.stem_md = stem;
      if (answer !== (question.answer_label ?? "")) body.answer_label = answer;
      const choiceEdits: Record<string, { text_md?: string }> = {};
      (question.choices ?? []).forEach((orig, i) => {
        const cur = choices[i];
        if (cur && cur.text_md !== orig.text_md) {
          choiceEdits[orig.label] = { text_md: cur.text_md };
        }
      });
      if (Object.keys(choiceEdits).length > 0) body.choice_edits = choiceEdits;
      if (Object.keys(body).length === 0) {
        setBusy(false);
        return;
      }
      await adminApi.patchQuestion(slug, question.id, body);
      onChange();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  async function setReviewStatus(s: "approved" | "flagged" | "pending") {
    setBusy(true);
    try {
      await adminApi.setReview(slug, question.id, s);
      onChange();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="border os-rule rounded-lg p-4 bg-white">
      <div className="flex items-center justify-between mb-2">
        <span className="text-xs font-mono text-ink/60">{question.id}</span>
        <div className="flex items-center gap-2">
          <span
            className={
              "text-xs px-2 py-0.5 rounded " +
              (status === "approved"
                ? "bg-green-100 text-green-800"
                : status === "flagged"
                  ? "bg-red-100 text-warn"
                  : "bg-ink/10 text-ink/60")
            }
          >
            {status}
          </span>
          <button
            type="button"
            onClick={() => setReviewStatus("approved")}
            disabled={busy}
            className="text-xs rounded border border-green-600 text-green-700 px-2 py-0.5"
          >
            Approve
          </button>
          <button
            type="button"
            onClick={() => setReviewStatus("flagged")}
            disabled={busy}
            className="text-xs rounded border border-warn text-warn px-2 py-0.5"
          >
            Flag
          </button>
        </div>
      </div>
      <label className="block text-xs text-ink/60 mb-1">Stem</label>
      <textarea
        value={stem}
        onChange={(e) => setStem(e.target.value)}
        rows={3}
        className="w-full rounded border os-rule px-2 py-1 text-sm mb-3 font-mono"
      />
      {choices.length > 0 && (
        <div className="space-y-1 mb-3">
          <label className="block text-xs text-ink/60">Choices</label>
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
                className="flex-1 rounded border os-rule px-2 py-1 text-sm"
              />
              <label className="text-xs flex items-center gap-1">
                <input
                  type="radio"
                  name={"ans-" + question.id}
                  checked={answer === c.label}
                  onChange={() => setAnswer(c.label)}
                />
                correct
              </label>
            </div>
          ))}
        </div>
      )}
      <button
        type="button"
        onClick={save}
        disabled={busy}
        className="text-sm rounded bg-blue-600 text-white px-3 py-1.5 disabled:opacity-50"
      >
        {busy ? "Saving…" : "Save"}
      </button>
    </li>
  );
}
