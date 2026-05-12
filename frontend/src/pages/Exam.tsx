import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useRoute } from "wouter";
import { Panel, PanelGroup, PanelResizeHandle } from "react-resizable-panels";
import { Header } from "../components/Header";
import { Footer } from "../components/Footer";
import { Passage } from "../components/Passage";
import { Question } from "../components/Question";
import { api } from "../lib/api";
import { useExam } from "../store/exam";

export function Exam() {
  const [, params] = useRoute("/exam/:sessionId");
  const sessionId = params?.sessionId ?? "";
  const [, setLoc] = useLocation();
  const session = useExam((s) => s.session);
  const module = useExam((s) => s.module);
  const currentIndex = useExam((s) => s.currentIndex);
  const setIndex = useExam((s) => s.setIndex);
  const setAnswer = useExam((s) => s.setAnswer);
  const recordTime = useExam((s) => s.recordTime);
  const noteServer = useExam((s) => s.noteServer);
  const setEnvelope = useExam((s) => s.setEnvelope);
  const questionFocusAtMs = useExam((s) => s.questionFocusAtMs);
  const [err, setErr] = useState<string | null>(null);
  const debounceRef = useRef<number | undefined>(undefined);

  // Resume on first mount (or after page refresh).
  useEffect(() => {
    if (!sessionId) return;
    if (session && session.id === sessionId && module) return;
    api
      .loadSession(sessionId)
      .then(setEnvelope)
      .catch((e) => setErr(String(e)));
  }, [sessionId, session, module, setEnvelope]);

  const flushAnswer = useCallback(
    async (questionId: string, choice: string) => {
      if (!session) return;
      // Accumulate any pending time on the previous question.
      const elapsed = Date.now() - questionFocusAtMs;
      recordTime(questionId, elapsed);
      const totalMs = (useExam.getState().timeOnQuestionMs[questionId] ?? 0);
      try {
        const r = await api.answer(session.id, questionId, choice, totalMs);
        noteServer(r.server_now_ms, r.module_deadline_at);
      } catch (e) {
        setErr(String(e));
      }
    },
    [session, recordTime, noteServer, questionFocusAtMs],
  );

  const onChoose = useCallback(
    (choice: string) => {
      const q = module?.questions[currentIndex];
      if (!q) return;
      setAnswer(q.id, choice);
      if (debounceRef.current) window.clearTimeout(debounceRef.current);
      debounceRef.current = window.setTimeout(() => {
        flushAnswer(q.id, choice);
      }, 500);
    },
    [module, currentIndex, setAnswer, flushAnswer],
  );

  const onExpire = useCallback(async () => {
    if (!session) return;
    try {
      await api.submit(session.id);
      setLoc(`/results/${session.id}`);
    } catch (e) {
      setErr(String(e));
    }
  }, [session, setLoc]);

  if (err) {
    return (
      <main className="p-8">
        <div className="text-warn">Error: {err}</div>
      </main>
    );
  }
  if (!module || !session) {
    return <main className="p-8 text-ink/60">Loading…</main>;
  }
  const q = module.questions[currentIndex];

  return (
    <div className="h-screen flex flex-col bg-chrome">
      <Header title={module.title} onExpire={onExpire} />
      <PanelGroup direction="horizontal" className="flex-1">
        <Panel defaultSize={55} minSize={30} className="bg-white">
          <div className="h-full overflow-y-auto p-8">
            <Passage
              questionId={q.id}
              passageMd={q.passage_md}
              passageFigure={q.passage_figure}
            />
          </div>
        </Panel>
        <PanelResizeHandle className="w-px bg-ruled hover:bg-blue-400 transition" />
        <Panel defaultSize={45} minSize={30} className="bg-white">
          <div className="h-full overflow-y-auto p-8">
            <div className="text-xs text-ink/50 mb-3">
              Question {currentIndex + 1}
            </div>
            <Question q={q} onChoose={onChoose} />
          </div>
        </Panel>
      </PanelGroup>
      <Footer
        onPrev={() => setIndex(Math.max(0, currentIndex - 1))}
        onNext={() =>
          setIndex(Math.min(module.questions.length - 1, currentIndex + 1))
        }
        onReview={() => setLoc(`/exam/${session.id}/review`)}
      />
    </div>
  );
}
