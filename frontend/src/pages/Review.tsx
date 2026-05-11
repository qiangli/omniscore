import { useState } from "react";
import { useLocation, useRoute } from "wouter";
import { api } from "../lib/api";
import { useExam } from "../store/exam";

export function Review() {
  const [, params] = useRoute("/exam/:sessionId/review");
  const sessionId = params?.sessionId ?? "";
  const [, setLoc] = useLocation();
  const module = useExam((s) => s.module);
  const answers = useExam((s) => s.answers);
  const session = useExam((s) => s.session);
  const setEnvelope = useExam((s) => s.setEnvelope);
  const setIndex = useExam((s) => s.setIndex);
  const [err, setErr] = useState<string | null>(null);

  if (!module || !session) {
    return <main className="p-8 text-ink/60">Loading…</main>;
  }

  async function onSubmitModule() {
    try {
      const r = await api.advance(sessionId);
      if ("done" in r && r.done) {
        await api.submit(sessionId);
        setLoc(`/results/${sessionId}`);
        return;
      }
      setEnvelope(r as Parameters<typeof setEnvelope>[0]);
      setLoc(`/exam/${sessionId}`);
    } catch (e) {
      setErr(String(e));
    }
  }

  return (
    <main className="min-h-screen px-8 py-10 max-w-3xl mx-auto">
      <h1 className="text-2xl font-semibold mb-1">Check your work</h1>
      <p className="text-ink/60 mb-6">{module.title}</p>
      <div className="grid grid-cols-6 gap-2 mb-8">
        {module.questions.map((q, i) => {
          const state = answers[q.id] ? "answered" : "unanswered";
          return (
            <button
              key={q.id}
              type="button"
              className="os-tile relative"
              data-state={state}
              onClick={() => {
                setIndex(i);
                setLoc(`/exam/${sessionId}`);
              }}
            >
              {i + 1}
            </button>
          );
        })}
      </div>
      <div className="flex justify-between">
        <button
          type="button"
          className="rounded border os-rule px-4 py-2 text-sm font-medium"
          onClick={() => setLoc(`/exam/${sessionId}`)}
        >
          Back
        </button>
        <button
          type="button"
          className="rounded bg-blue-600 text-white px-4 py-2 text-sm font-medium hover:bg-blue-700"
          onClick={onSubmitModule}
        >
          Submit module
        </button>
      </div>
      {err && <div className="mt-4 text-warn text-sm">{err}</div>}
    </main>
  );
}
