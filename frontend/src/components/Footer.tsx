import { useState } from "react";
import { useExam } from "../store/exam";

export function Footer({
  onPrev,
  onNext,
  onReview,
}: {
  onPrev: () => void;
  onNext: () => void;
  onReview: () => void;
}) {
  const module = useExam((s) => s.module);
  const currentIndex = useExam((s) => s.currentIndex);
  const answers = useExam((s) => s.answers);
  const setIndex = useExam((s) => s.setIndex);
  const [open, setOpen] = useState(false);

  if (!module) return null;

  const total = module.questions.length;
  const onLast = currentIndex >= total - 1;

  return (
    <footer className="relative border-t os-rule bg-white px-6 py-3 flex items-center justify-between">
      <button
        type="button"
        onClick={onPrev}
        disabled={currentIndex === 0}
        className="rounded border os-rule px-4 py-2 text-sm font-medium disabled:opacity-40"
      >
        Back
      </button>

      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="rounded bg-ink text-white px-4 py-2 text-sm font-medium"
        aria-expanded={open}
      >
        Question {currentIndex + 1} of {total} ▾
      </button>

      <button
        type="button"
        onClick={onLast ? onReview : onNext}
        className="rounded bg-blue-600 text-white px-4 py-2 text-sm font-medium hover:bg-blue-700"
      >
        {onLast ? "Review" : "Next"}
      </button>

      {open && (
        <div
          role="dialog"
          aria-label="Question navigator"
          className="absolute bottom-16 left-1/2 -translate-x-1/2 bg-white border os-rule rounded-lg shadow-lg p-4 min-w-[280px]"
          onMouseLeave={() => setOpen(false)}
        >
          <div className="text-xs text-ink/60 mb-2">Jump to question</div>
          <div className="flex flex-wrap gap-1.5">
            {module.questions.map((q, i) => {
              const state =
                i === currentIndex
                  ? "current"
                  : answers[q.id]
                    ? "answered"
                    : "unanswered";
              return (
                <button
                  key={q.id}
                  type="button"
                  className="os-tile relative"
                  data-state={state}
                  onClick={() => {
                    setIndex(i);
                    setOpen(false);
                  }}
                  aria-label={`Question ${i + 1} ${state}`}
                >
                  {i + 1}
                </button>
              );
            })}
          </div>
          <div className="mt-3 text-[11px] text-ink/50 flex gap-4">
            <span>● answered</span>
            <span>○ unanswered</span>
            <span>▢ current</span>
          </div>
        </div>
      )}
    </footer>
  );
}
