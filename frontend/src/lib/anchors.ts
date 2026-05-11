import type { HighlightAnchor } from "./types";

// PASSAGE_VERSION is bumped if the passage rendering changes in a way that
// invalidates stored anchors (e.g. punctuation cleanup). MVP = 1.
export const PASSAGE_VERSION = 1;

// Compute the character offset of (node, offset) within the textContent of root.
function offsetWithin(root: Node, node: Node, offset: number): number {
  let pos = 0;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let n: Node | null = walker.nextNode();
  while (n) {
    if (n === node) return pos + offset;
    pos += (n as Text).data.length;
    n = walker.nextNode();
  }
  return pos;
}

// Serialize the current Selection (if inside `root`) as a HighlightAnchor.
export function selectionToAnchor(root: HTMLElement): HighlightAnchor | null {
  const sel = window.getSelection();
  if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return null;
  const range = sel.getRangeAt(0);
  if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) {
    return null;
  }
  const start = offsetWithin(root, range.startContainer, range.startOffset);
  const end = offsetWithin(root, range.endContainer, range.endOffset);
  const text = range.toString();
  if (!text.trim()) return null;
  return { text, start, end, passage_version: PASSAGE_VERSION };
}

// Apply a list of anchors as `<mark class="os-hl">` wrappers inside `root`.
// Re-applies cleanly on every call (idempotent).
export function applyHighlights(root: HTMLElement, anchors: HighlightAnchor[]): void {
  // Strip any existing marks first.
  root.querySelectorAll("mark.os-hl").forEach((m) => {
    const parent = m.parentNode!;
    while (m.firstChild) parent.insertBefore(m.firstChild, m);
    parent.removeChild(m);
    parent.normalize();
  });
  // Sort descending so insertions don't shift earlier offsets.
  const sorted = [...anchors]
    .filter((a) => a.passage_version === PASSAGE_VERSION)
    .sort((a, b) => b.start - a.start);
  for (const a of sorted) {
    wrapRange(root, a.start, a.end);
  }
}

function wrapRange(root: HTMLElement, start: number, end: number): void {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let pos = 0;
  let n: Node | null = walker.nextNode();
  let startNode: Text | null = null;
  let startOff = 0;
  let endNode: Text | null = null;
  let endOff = 0;
  while (n) {
    const t = n as Text;
    const len = t.data.length;
    if (!startNode && pos + len > start) {
      startNode = t;
      startOff = start - pos;
    }
    if (!endNode && pos + len >= end) {
      endNode = t;
      endOff = end - pos;
      break;
    }
    pos += len;
    n = walker.nextNode();
  }
  if (!startNode || !endNode) return;
  try {
    const r = document.createRange();
    r.setStart(startNode, startOff);
    r.setEnd(endNode, endOff);
    const mark = document.createElement("mark");
    mark.className = "os-hl";
    // surroundContents fails on partial-element selections; fall back to extract.
    try {
      r.surroundContents(mark);
    } catch {
      const frag = r.extractContents();
      mark.appendChild(frag);
      r.insertNode(mark);
    }
  } catch {
    /* ignore — malformed anchor */
  }
}
