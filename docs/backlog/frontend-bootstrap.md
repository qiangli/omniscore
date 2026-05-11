---
title: Vite + React + TS + Tailwind scaffold
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - frontend/ has Vite + React 18 + TypeScript + Tailwind
  - npm run dev / npm run build / npm run lint all succeed
  - wouter routing, zustand store skeleton
  - vite proxy forwards /api → http://localhost:8080 in dev
gitea_issue: 2
---

Deps: react, react-dom, wouter, zustand, react-resizable-panels, katex, rangy, @radix-ui/react-dialog, @radix-ui/react-popover, qrcode-terminal (server side only), classnames.
