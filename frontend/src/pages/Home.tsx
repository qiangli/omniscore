import { useEffect, useState } from "react";
import { useLocation } from "wouter";
import { api } from "../lib/api";
import { useExam } from "../store/exam";
import type { TestListing } from "../lib/types";

// Each visit to the landing page starts a fresh student session. This keeps
// per-user tracking clean when multiple students share a kiosk laptop or
// when the same student returns for another attempt — every Join click
// creates a new students row server-side.
export function Home() {
  const [, setLoc] = useLocation();
  const [name, setName] = useState("");
  const [joinedName, setJoinedName] = useState<string | null>(null);
  const [tests, setTests] = useState<TestListing[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const resetStore = useExam((s) => s.reset);
  const setEnvelope = useExam((s) => s.setEnvelope);

  // Always land on the name prompt — clear any prior session cookie so the
  // server issues a fresh student id on Join.
  useEffect(() => {
    document.cookie =
      "omniscore_sid=; Path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
    resetStore();
  }, [resetStore]);

  useEffect(() => {
    api
      .listTests()
      .then((r) => setTests(r.tests))
      .catch((e) => setErr(String(e)));
  }, []);

  async function onJoin(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    try {
      const s = await api.joinAs(trimmed);
      setJoinedName(s.display_name);
      setErr(null);
    } catch (e) {
      setErr(String(e));
    }
  }

  async function start(slug: string) {
    try {
      const env = await api.createSession(slug);
      setEnvelope(env);
      setLoc(`/exam/${env.session.id}`);
    } catch (e) {
      setErr(String(e));
    }
  }

  return (
    <main className="min-h-screen flex items-start justify-center pt-24 px-4">
      <div className="w-full max-w-2xl">
        <h1 className="text-4xl font-semibold tracking-tight mb-2">OmniScore</h1>
        <p className="text-ink/60 mb-8">Digital SAT &amp; AP practice — local-first.</p>

        {!joinedName ? (
          <form onSubmit={onJoin} className="bg-white border os-rule rounded-xl p-6">
            <label className="block text-sm font-medium mb-2" htmlFor="name">
              What's your name?
            </label>
            <input
              id="name"
              type="text"
              value={name}
              autoFocus
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Alex Chen"
              className="w-full rounded-lg border os-rule px-3 py-2 mb-3"
            />
            <button
              type="submit"
              className="rounded-lg bg-blue-600 text-white px-4 py-2 font-medium hover:bg-blue-700"
            >
              Join
            </button>
            <p className="mt-3 text-xs text-ink/50">
              A fresh session is created on every join. Multiple students can
              share this device — each visit is tracked separately.
            </p>
            {err && <div className="mt-3 text-warn text-sm">{err}</div>}
          </form>
        ) : (
          <section className="bg-white border os-rule rounded-xl p-6">
            <div className="flex items-center justify-between mb-4">
              <div className="text-sm text-ink/60">
                Signed in as{" "}
                <span className="font-medium text-ink">{joinedName}</span>
              </div>
              <button
                type="button"
                className="text-sm text-ink/50 hover:text-ink"
                onClick={() => {
                  document.cookie =
                    "omniscore_sid=; Path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
                  setJoinedName(null);
                  setName("");
                }}
              >
                Switch user
              </button>
            </div>
            <h2 className="text-xl font-semibold mb-3">Available practice tests</h2>
            {tests.length === 0 ? (
              <div className="text-ink/60 text-sm">
                No tests published yet. Drop a JSON into <code>content/tests/</code>.
              </div>
            ) : (
              <ul className="space-y-2">
                {tests.map((t) => (
                  <li
                    key={t.slug}
                    className="flex items-center justify-between border os-rule rounded-lg px-4 py-3"
                  >
                    <div>
                      <div className="font-medium">{t.title}</div>
                      <div className="text-xs text-ink/60">
                        {t.exam_type.toUpperCase()} · {t.modules} module
                        {t.modules === 1 ? "" : "s"}
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={() => start(t.slug)}
                      className="rounded bg-ink text-white px-4 py-2 text-sm font-medium"
                    >
                      Start
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {err && <div className="mt-3 text-warn text-sm">{err}</div>}
          </section>
        )}
      </div>
    </main>
  );
}
