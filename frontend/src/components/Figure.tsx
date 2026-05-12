import type { Figure } from "../lib/types";

export function FigureView({ fig }: { fig: Figure }) {
  return (
    <img
      src={fig.src}
      alt={fig.alt ?? ""}
      loading="lazy"
      style={fig.width_px ? { maxWidth: fig.width_px } : undefined}
      className="my-2 max-w-full rounded border os-rule"
    />
  );
}
