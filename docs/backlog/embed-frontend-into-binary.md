---
title: Embed Vite dist into the Go binary
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - `//go:embed frontend/dist` embeds the production build
  - chi mounts the embedded FS at /
  - /healthz still works
  - the built binary serves the React app with no external file system reads
---

Runtime = pure Go. Frontend is Vite-built ahead of time and baked in.
