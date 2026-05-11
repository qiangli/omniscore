import { useEffect, useState } from "react";
import { Link, useRoute } from "wouter";
import { api } from "../lib/api";
import { MDInline } from "../lib/markdown";
import type { Summary } from "../lib/types";

export function Results() {
  const [, params] = useRoute("/results/:sessionId");
  const sessionId = params?.sessionId ?? "";
  const [summary, setSummary] = useState<Summary | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!sessionId) return;
    api
      .results(sessionId)
      .then(setSummary)
      .catch((e) => setErr(String(e)));
  }, [sessionId]);

  if (err) return <main className="p-8 text-warn">{err}</main>;
  if (!summary) return <main className="p-8 text-ink/60">Scoring…</main>;

  return (
    <main className="min-h-screen px-8 py-10 max-w-4xl mx-auto">
      <Link href="/" className="text-sm text-ink/60 hover:text-ink">
        ← Home
      </Link>
      <h1 className="text-3xl font-semibold mt-4 mb-2">Results</h1>
      <div className="mb-8 bg-white border os-rule rounded-xl p-6">
        <div className="text-sm text-ink/60">Scaled score</div>
        <div className="text-5xl font-semibold tabular-nums">
          {summary.scaled_total}
        </div>
        <div className="mt-4 grid grid-cols-2 gap-4">
          {Object.entries(summary.by_section_scaled).map(([sec, scaled]) => (
            <div key={sec} className="border os-rule rounded-lg p-3">
              <div className="text-xs uppercase tracking-wide text-ink/60">
                {sec === "rw" ? "Reading & Writing" : sec === "math" ? "Math" : sec}
              </div>
              <div className="text-2xl font-semibold tabular-nums">{scaled}</div>
              <div className="text-xs text-ink/60">
                Raw: {summary.by_section_raw[sec] ?? 0}
              </div>
            </div>
          ))}
        </div>
      </div>

      <h2 className="text-xl font-semibold mb-3">Question-by-question</h2>
      <ul className="space-y-3">
        {summary.questions.map((r, i) => (
          <li
            key={r.question_id}
            className={
              "border rounded-lg p-4 " +
              (r.is_correct ? "border-emerald-300 bg-emerald-50" : "border-warn/40 bg-warn/5")
            }
          >
            <div className="flex justify-between items-baseline mb-1">
              <div className="font-medium">
                #{i + 1} · {r.section.toUpperCase()} ·{" "}
                {r.is_correct ? "Correct" : "Incorrect"}
              </div>
              <div className="text-xs text-ink/60 tabular-nums">
                {Math.round(r.time_on_question_ms / 1000)}s
              </div>
            </div>
            <div className="text-sm">
              Your answer: <span className="font-medium">{r.chosen || "—"}</span>
              {"  ·  "}
              Correct: <span className="font-medium">{r.correct}</span>
            </div>
            {r.rationale_md && (
              <div className="mt-2 text-sm text-ink/80">
                <MDInline text={r.rationale_md} />
              </div>
            )}
          </li>
        ))}
      </ul>
    </main>
  );
}
