// Per-(exam, subject) lookup for section codes → human labels. Falls back to
// the prettified raw section code when no entry matches, so an unknown AP
// subject still renders something readable on the Results page.
const LABELS: Record<string, Record<string, string>> = {
  sat: {
    rw: "Reading & Writing",
    math: "Math",
  },
  "ap:calc_bc": {
    mcq_no_calc: "MCQ Part A — No calculator",
    mcq_calc: "MCQ Part B — Calculator",
    mcq_total: "Multiple Choice (Total)",
    frq_no_calc: "FRQ Part A — No calculator",
    frq_calc: "FRQ Part B — Calculator",
  },
};

export function sectionLabel(
  examType: string | undefined,
  subject: string | undefined,
  section: string,
): string {
  if (examType) {
    const subjectKey = subject ? `${examType}:${subject}` : examType;
    const fromSubject = LABELS[subjectKey]?.[section];
    if (fromSubject) return fromSubject;
    const fromExam = LABELS[examType]?.[section];
    if (fromExam) return fromExam;
  }
  return section.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}
