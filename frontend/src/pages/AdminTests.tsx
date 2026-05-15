import { useEffect, useState } from "react";
import { Link, useLocation } from "wouter";
import { adminApi, type AdminTestListing } from "../lib/api";

// Admin test listing: one row per published test, with review counts.
// Auth gate is cosmetic (whoami probe) — the cookie is the real check.
export function AdminTests() {
  const [, setLoc] = useLocation();
  const [tests, setTests] = useState<AdminTestListing[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    adminApi
      .whoami()
      .then(() => adminApi.listTests())
      .then((r) => setTests(r.tests))
      .catch((e) => {
        if (String(e).includes("401")) {
          setLoc("/admin/login");
          return;
        }
        setErr(String(e));
      });
  }, [setLoc]);

  async function logout() {
    await adminApi.logout().catch(() => {});
    setLoc("/admin/login");
  }

  return (
    <main className="min-h-screen px-4 py-8 max-w-4xl mx-auto">
      <header className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold">Admin · Tests</h1>
        <button
          type="button"
          onClick={logout}
          className="text-sm text-ink/60 hover:text-ink"
        >
          Sign out
        </button>
      </header>
      {err && <div className="mb-4 text-warn text-sm">{err}</div>}
      {tests.length === 0 ? (
        <div className="text-ink/60 text-sm">No tests loaded.</div>
      ) : (
        <ul className="space-y-2">
          {tests.map((t) => (
            <li
              key={t.slug}
              className="flex items-center justify-between border os-rule rounded-lg px-4 py-3 bg-white"
            >
              <div>
                <div className="font-medium">{t.title}</div>
                <div className="text-xs text-ink/60">
                  {t.exam_type.toUpperCase()} · {t.total} questions ·{" "}
                  <span className="text-green-700">{t.approved} approved</span> ·{" "}
                  <span className="text-warn">{t.flagged} flagged</span> ·{" "}
                  {t.pending} pending
                </div>
              </div>
              <Link
                to={`/admin/review/${t.slug}`}
                className="rounded bg-ink text-white px-4 py-2 text-sm font-medium"
              >
                Review
              </Link>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}
