export type Figure = {
  src: string;
  alt?: string;
  width_px?: number;
};

export type Choice = {
  label: string;
  text_md: string;
  figure?: Figure;
};

// QuestionType controls the renderer and the grader. New types are added by
// extending this union (frontend) + registering a grader (backend). An empty
// `type` field is treated as "mcq" for back-compat with existing JSON.
export type QuestionType = "mcq" | "spr";

export type Question = {
  id: string;
  type?: QuestionType;
  passage_md?: string;
  passage_figure?: Figure;
  stem_md: string;
  stem_figure?: Figure;
  choices?: Choice[]; // populated for mcq; absent for spr
};

export type Module = {
  id: string;
  section: string;
  title: string;
  time_limit_s: number;
  questions: Question[];
};

export type SessionState = "in_progress" | "submitted" | "expired";

export type Session = {
  id: string;
  student_id: number;
  test_slug: string;
  current_module: number;
  module_deadline_at: number;
  state: SessionState;
  raw_score?: number;
  scaled_score?: number;
};

export type Response = {
  question_id: string;
  choice: string;
  time_on_question_ms: number;
  last_answered_at: number;
};

export type Highlight = {
  id: number;
  question_id: string;
  anchor_json: string;
  color: string;
  note?: string;
  created_at: number;
};

export type TestListing = {
  slug: string;
  title: string;
  exam_type: string;
  subject?: string;
  modules: number;
};

export type SessionEnvelope = {
  session: Session;
  module: Module;
  test: TestListing;
  responses: Response[] | null;
  highlights: Highlight[] | null;
  server_now_ms: number;
};

export type AnswerResp = {
  server_now_ms: number;
  module_deadline_at: number;
};

export type Result = {
  question_id: string;
  section: string;
  module_id: string;
  chosen: string;
  correct: string;
  is_correct: boolean;
  time_on_question_ms: number;
  rationale_md?: string;
};

export type Summary = {
  session_id: string;
  test_slug: string;
  exam_type?: string;
  subject?: string;
  state: SessionState;
  raw_total: number;
  scaled_total: number;
  scaled_total_low?: number;
  scaled_total_high?: number;
  by_section_raw: Record<string, number>;
  by_section_scaled: Record<string, number>;
  by_section_scaled_low?: Record<string, number>;
  by_section_scaled_high?: Record<string, number>;
  questions: Result[];
};

export type HighlightAnchor = {
  text: string;
  start: number;
  end: number;
  passage_version: number;
};
