import { useEffect, useState } from "react";
import { useLocation } from "wouter";
import { api } from "../lib/api";
import { useExam } from "../store/exam";
import type { TestListing } from "../lib/types";

export function Home() {
  const [, setLoc] = useLocation();
  const [name, setName] = useState(
    () => localStorage.getItem("omniscore.display_name") ?? "",
  );
  const [joined, setJoined] = useState<boolean>(() =>
    Boolean(localStorage.getItem("omniscore.display_name")),
  );
  const [tests, setTests] = useState<TestListing[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const setEnvelope = useExam((s) => s.setEnvelope);

  useEffect(() => {
    api
      .listTests()
      .then((r) => setTests(r.tests))
      .catch((e) => setErr(String(e)));
  }, []);

  async function onJoin(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim()) return;
    try {
      await api.joinAs(name.trim());
      localStorage.setItem("omniscore.display_name", name.trim());
      setJoined(true);
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

        {!joined ? (
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
            {err && <div className="mt-3 text-warn text-sm">{err}</div>}
          </form>
        ) : (
          <section className="bg-white border os-rule rounded-xl p-6">
            <div className="flex items-center justify-between mb-4">
              <div className="text-sm text-ink/60">
                Signed in as <span className="font-medium text-ink">{name}</span>
              </div>
              <button
                type="button"
                className="text-sm text-ink/50 hover:text-ink"
                onClick={() => {
                  localStorage.removeItem("omniscore.display_name");
                  document.cookie =
                    "omniscore_sid=; Path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
                  setJoined(false);
                }}
              >
                Sign out
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
