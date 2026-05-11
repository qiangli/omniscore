import { useEffect, useMemo, useRef } from "react";
import { MD } from "../lib/markdown";
import {
  PASSAGE_VERSION,
  applyHighlights,
  selectionToAnchor,
} from "../lib/anchors";
import { useExam } from "../store/exam";
import { api } from "../lib/api";
import type { HighlightAnchor } from "../lib/types";

// Passage is memoized on (question_id, passage_version) so React never
// re-renders its inner DOM mid-question — which would break highlight anchors.
// We then re-apply highlights as a side-effect after each render.
export function Passage({
  questionId,
  passageMd,
}: {
  questionId: string;
  passageMd?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const session = useExam((s) => s.session);
  const highlights = useExam((s) => s.highlights);
  const pushHighlight = useExam((s) => s.pushHighlight);
  const removeHighlight = useExam((s) => s.removeHighlight);

  // Filter highlights to this question. Parse JSON anchors here to keep
  // applyHighlights() pure on HighlightAnchor[].
  const myAnchors: { id: number; a: HighlightAnchor }[] = useMemo(() => {
    return highlights
      .filter((h) => h.question_id === questionId)
      .map((h) => {
        try {
          return { id: h.id, a: JSON.parse(h.anchor_json) as HighlightAnchor };
        } catch {
          return null;
        }
      })
      .filter((x): x is { id: number; a: HighlightAnchor } => x !== null);
  }, [highlights, questionId]);

  // Re-apply highlights after each render.
  useEffect(() => {
    if (!ref.current) return;
    applyHighlights(
      ref.current,
      myAnchors.map((x) => x.a),
    );
  }, [myAnchors, passageMd]);

  // Ctrl+H toggle: add new highlight from current selection, or remove the
  // existing one that the selection overlaps.
  useEffect(() => {
    if (!session) return;
    const onKey = async (ev: KeyboardEvent) => {
      const isToggle =
        (ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "h";
      if (!isToggle) return;
      if (!ref.current) return;
      const sel = window.getSelection();
      if (!sel) return;
      const within =
        sel.rangeCount > 0 &&
        ref.current.contains(sel.getRangeAt(0).commonAncestorContainer);
      if (!within) return;
      ev.preventDefault();
      // If selection collapses inside an existing highlight, delete it.
      if (sel.isCollapsed) {
        const node = sel.anchorNode;
        if (node && node.parentElement) {
          const mark = node.parentElement.closest("mark.os-hl");
          if (mark) {
            const start = parseInt(mark.getAttribute("data-start") ?? "", 10);
            const match = myAnchors.find((x) => x.a.start === start);
            if (match) {
              try {
                await api.deleteHighlight(session.id, match.id);
                removeHighlight(match.id);
              } catch {
                /* ignore */
              }
            }
          }
        }
        return;
      }
      const anchor = selectionToAnchor(ref.current);
      if (!anchor) return;
      try {
        const { id } = await api.addHighlight(
          session.id,
          questionId,
          JSON.stringify(anchor),
          "yellow",
        );
        pushHighlight({
          id,
          question_id: questionId,
          anchor_json: JSON.stringify(anchor),
          color: "yellow",
          created_at: Date.now(),
        });
        sel.removeAllRanges();
      } catch {
        /* ignore */
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [session, questionId, myAnchors, pushHighlight, removeHighlight]);

  if (!passageMd) {
    return (
      <div className="prose max-w-none text-ink/60 italic">
        (No passage for this question)
      </div>
    );
  }
  return (
    <div
      ref={ref}
      data-passage-version={PASSAGE_VERSION}
      className="prose max-w-none text-base leading-relaxed select-text"
    >
      <MD text={passageMd} />
    </div>
  );
}
