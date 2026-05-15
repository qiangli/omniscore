import { useEffect, useState } from "react";
import { useLocation } from "wouter";
import { adminApi } from "../lib/api";

// LAN-trust + passphrase: the operator types the passphrase printed once
// in the server's startup banner. Real auth deferred to a future change;
// the cookie is the gate, the SPA route guard is cosmetic.
export function AdminLogin() {
  const [, setLoc] = useLocation();
  const [pass, setPass] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  // If already signed in, skip the form.
  useEffect(() => {
    adminApi
      .whoami()
      .then(() => setLoc("/admin/tests"))
      .catch(() => {
        /* not signed in */
      });
  }, [setLoc]);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    try {
      await adminApi.login(pass);
      setLoc("/admin/tests");
    } catch (e) {
      setErr(String(e));
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="min-h-screen flex items-start justify-center pt-24 px-4">
      <div className="w-full max-w-md">
        <h1 className="text-2xl font-semibold mb-2">Admin sign-in</h1>
        <p className="text-ink/60 mb-6 text-sm">
          Enter the passphrase printed in the server startup banner.
        </p>
        <form onSubmit={onSubmit} className="bg-white border os-rule rounded-xl p-6">
          <label className="block text-sm font-medium mb-2" htmlFor="pass">
            Passphrase
          </label>
          <input
            id="pass"
            type="password"
            value={pass}
            autoFocus
            onChange={(e) => setPass(e.target.value)}
            className="w-full rounded-lg border os-rule px-3 py-2 mb-3 font-mono"
          />
          <button
            type="submit"
            disabled={loading || !pass}
            className="rounded-lg bg-blue-600 text-white px-4 py-2 font-medium hover:bg-blue-700 disabled:opacity-50"
          >
            {loading ? "Signing in…" : "Sign in"}
          </button>
          {err && <div className="mt-3 text-warn text-sm">{err}</div>}
        </form>
      </div>
    </main>
  );
}
