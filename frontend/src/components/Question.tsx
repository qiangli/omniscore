import { useEffect } from "react";
import { MDInline } from "../lib/markdown";
import { useExam } from "../store/exam";
import type { Question as Q } from "../lib/types";

export function Question({
  q,
  onChoose,
}: {
  q: Q;
  onChoose: (choice: string) => void;
}) {
  const answers = useExam((s) => s.answers);
  const chosen = answers[q.id] ?? "";

  // Keyboard A/B/C/D selects choices when this pane is in focus.
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
      const k = ev.key.toUpperCase();
      if (!"ABCD".includes(k)) return;
      const exists = q.choices.find((c) => c.label === k);
      if (exists) {
        ev.preventDefault();
        onChoose(k);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [q, onChoose]);

  return (
    <div className="os-choices space-y-4 max-w-2xl">
      <div className="text-base leading-relaxed">
        <MDInline text={q.stem_md} />
      </div>
      <div className="space-y-2">
        {q.choices.map((c) => {
          const active = chosen === c.label;
          return (
            <button
              key={c.label}
              type="button"
              onClick={() => onChoose(c.label)}
              className={
                "w-full text-left rounded-lg border os-rule px-4 py-3 flex items-start gap-3 transition " +
                (active
                  ? "border-blue-600 ring-2 ring-blue-600/30 bg-blue-50"
                  : "hover:bg-chrome")
              }
              aria-pressed={active}
            >
              <span
                className={
                  "inline-flex items-center justify-center h-7 w-7 rounded-full border text-sm font-semibold shrink-0 " +
                  (active
                    ? "bg-blue-600 text-white border-blue-600"
                    : "border-ink/30 text-ink")
                }
              >
                {c.label}
              </span>
              <span className="flex-1 text-base leading-relaxed">
                <MDInline text={c.text_md} />
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
