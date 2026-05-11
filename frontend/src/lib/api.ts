import type {
  AnswerResp,
  Highlight,
  SessionEnvelope,
  Summary,
  TestListing,
} from "./types";

async function http<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "include",
    headers: body !== undefined ? { "Content-Type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let detail = res.statusText;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) detail = j.error;
    } catch {
      /* ignore */
    }
    throw new Error(`${res.status} ${detail}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  joinAs: (displayName: string) =>
    http<{ id: number; display_name: string }>("POST", "/api/students", {
      display_name: displayName,
    }),
  listTests: () => http<{ tests: TestListing[] }>("GET", "/api/tests"),
  createSession: (testSlug: string) =>
    http<SessionEnvelope>("POST", "/api/sessions", { test_slug: testSlug }),
  loadSession: (id: string) =>
    http<SessionEnvelope>("GET", `/api/sessions/${id}`),
  answer: (
    id: string,
    questionId: string,
    choice: string,
    timeOnMs: number,
  ) =>
    http<AnswerResp>("PATCH", `/api/sessions/${id}/answer`, {
      question_id: questionId,
      choice,
      time_on_question_ms: timeOnMs,
    }),
  advance: (id: string) =>
    http<SessionEnvelope | { done: true }>(
      "POST",
      `/api/sessions/${id}/advance`,
    ),
  submit: (id: string) =>
    http<Summary>("POST", `/api/sessions/${id}/submit`),
  results: (id: string) =>
    http<Summary>("GET", `/api/sessions/${id}/results`),
  addHighlight: (
    id: string,
    questionId: string,
    anchorJson: string,
    color: string,
  ) =>
    http<{ id: number }>("POST", `/api/sessions/${id}/highlights`, {
      question_id: questionId,
      anchor_json: anchorJson,
      color,
    }),
  deleteHighlight: (id: string, hid: number) =>
    http<void>("DELETE", `/api/sessions/${id}/highlights/${hid}`),
};

export type { Highlight };
