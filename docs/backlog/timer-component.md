---
title: Header timer with hide/show + red lock + auto-submit
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - mm:ss countdown matching the server module_deadline_at
  - "Hide Timer" toggle until 5 minutes remain
  - at ≤5 min: timer turns red and unhide is disabled
  - on expiry: client auto-POSTs /submit; server enforces too
  - skew correction: `displayed = max(0, deadline_at - server_now) - (clientNow - lastSync)`
---
