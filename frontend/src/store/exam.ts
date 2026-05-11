import { create } from "zustand";
import type {
  Highlight,
  Module,
  Response,
  Session,
  TestListing,
} from "../lib/types";
import { newSync, type TimerSync } from "../lib/timer";

type ExamStore = {
  session: Session | null;
  module: Module | null;
  test: TestListing | null;
  currentIndex: number;
  answers: Record<string, string>;
  timeOnQuestionMs: Record<string, number>;
  highlights: Highlight[];
  sync: TimerSync | null;
  questionFocusAtMs: number;
  setEnvelope: (e: {
    session: Session;
    module: Module;
    test: TestListing;
    responses?: Response[] | null;
    highlights?: Highlight[] | null;
    server_now_ms: number;
  }) => void;
  setAnswer: (questionId: string, choice: string) => void;
  recordTime: (questionId: string, addMs: number) => void;
  setIndex: (i: number) => void;
  pushHighlight: (h: Highlight) => void;
  removeHighlight: (hid: number) => void;
  noteServer: (server_now_ms: number, deadline_at: number) => void;
  reset: () => void;
};

export const useExam = create<ExamStore>((set) => ({
  session: null,
  module: null,
  test: null,
  currentIndex: 0,
  answers: {},
  timeOnQuestionMs: {},
  highlights: [],
  sync: null,
  questionFocusAtMs: Date.now(),
  setEnvelope: (e) => {
    const answers: Record<string, string> = {};
    const times: Record<string, number> = {};
    for (const r of e.responses ?? []) {
      if (r.choice) answers[r.question_id] = r.choice;
      times[r.question_id] = r.time_on_question_ms;
    }
    set({
      session: e.session,
      module: e.module,
      test: e.test,
      answers,
      timeOnQuestionMs: times,
      highlights: e.highlights ?? [],
      currentIndex: 0,
      sync: newSync(e.server_now_ms, e.session.module_deadline_at),
      questionFocusAtMs: Date.now(),
    });
  },
  setAnswer: (q, c) =>
    set((s) => ({ answers: { ...s.answers, [q]: c } })),
  recordTime: (q, addMs) =>
    set((s) => ({
      timeOnQuestionMs: {
        ...s.timeOnQuestionMs,
        [q]: (s.timeOnQuestionMs[q] ?? 0) + addMs,
      },
    })),
  setIndex: (i) => set({ currentIndex: i, questionFocusAtMs: Date.now() }),
  pushHighlight: (h) =>
    set((s) => ({ highlights: [...s.highlights, h] })),
  removeHighlight: (hid) =>
    set((s) => ({ highlights: s.highlights.filter((x) => x.id !== hid) })),
  noteServer: (server_now_ms, deadline_at) =>
    set({ sync: newSync(server_now_ms, deadline_at) }),
  reset: () =>
    set({
      session: null,
      module: null,
      test: null,
      currentIndex: 0,
      answers: {},
      timeOnQuestionMs: {},
      highlights: [],
      sync: null,
      questionFocusAtMs: Date.now(),
    }),
}));
