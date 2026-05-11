import { useEffect, useRef, useState } from "react";
import { formatMMSS, remainingMs } from "../lib/timer";
import { useExam } from "../store/exam";

const FIVE_MIN_MS = 5 * 60 * 1000;

export function Header({
  title,
  onExpire,
}: {
  title: string;
  onExpire: () => void;
}) {
  const sync = useExam((s) => s.sync);
  const [now, setNow] = useState(Date.now());
  const [hidden, setHidden] = useState(false);
  const expiredFired = useRef(false);

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  if (!sync) {
    return (
      <header className="flex items-center justify-between border-b os-rule bg-white px-6 py-3">
        <div className="font-medium">{title}</div>
      </header>
    );
  }

  const remaining = remainingMs(sync, now);
  const underFive = remaining <= FIVE_MIN_MS;

  if (remaining <= 0 && !expiredFired.current) {
    expiredFired.current = true;
    onExpire();
  }

  return (
    <header className="flex items-center justify-between border-b os-rule bg-white px-6 py-3">
      <div className="font-medium">{title}</div>
      <div className="flex items-center gap-3">
        <div
          className={
            "font-mono text-lg tabular-nums " + (underFive ? "text-warn font-semibold" : "")
          }
          aria-live="polite"
          aria-atomic="true"
        >
          {hidden && !underFive ? "—" : formatMMSS(remaining)}
        </div>
        <button
          type="button"
          onClick={() => !underFive && setHidden((h) => !h)}
          disabled={underFive}
          className="text-sm text-ink/60 hover:text-ink disabled:opacity-30"
          title={underFive ? "Timer is locked in the final 5 minutes" : "Hide timer"}
        >
          {hidden ? "Show" : "Hide"}
        </button>
      </div>
    </header>
  );
}
