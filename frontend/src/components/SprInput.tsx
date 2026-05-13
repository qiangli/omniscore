import { useEffect, useRef } from "react";

// SprInput renders the single-line answer entry used by Digital SAT Math
// Student-Produced Response items (and any future short-answer type). The
// real Bluebook UI accepts digits, decimal point, minus, fraction slash;
// here we accept the same plus semicolon/comma so callers can encode
// multi-value answers like "2; -12". The server-side grader does the
// numeric normalization (1/2 ≡ 0.5 ≡ .5).
export function SprInput({
  value,
  onChange,
  placeholder = "Type your answer",
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  const ref = useRef<HTMLInputElement | null>(null);

  // Stop the global A–E shortcut from selecting choices while the user is
  // typing a number. We listen on the input itself so the parent's window
  // keydown handler only fires when this input isn't focused.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const stop = (ev: KeyboardEvent) => ev.stopPropagation();
    el.addEventListener("keydown", stop);
    return () => el.removeEventListener("keydown", stop);
  }, []);

  return (
    <div className="space-y-2 max-w-md">
      <label className="text-sm text-ink/70" htmlFor="spr-input">
        Answer
      </label>
      <input
        id="spr-input"
        ref={ref}
        type="text"
        inputMode="text"
        autoComplete="off"
        spellCheck={false}
        className="w-full rounded-lg border os-rule px-4 py-3 text-lg tabular-nums focus:border-blue-600 focus:ring-2 focus:ring-blue-600/30 outline-none"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
      <p className="text-xs text-ink/60 leading-relaxed">
        Accepted formats: integers (e.g. <span className="tabular-nums">2520</span>),
        decimals (<span className="tabular-nums">0.5</span>), fractions
        (<span className="tabular-nums">1/2</span>), negatives
        (<span className="tabular-nums">-12</span>). For questions with two
        answers, separate them with <code>;</code> — e.g.{" "}
        <span className="tabular-nums">2; -12</span>.
      </p>
    </div>
  );
}
