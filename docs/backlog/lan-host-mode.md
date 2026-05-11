---
title: LAN host mode (0.0.0.0 bind, join URL, ASCII QR)
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - default bind is 0.0.0.0:8080
  - on boot: log every non-loopback IPv4 join URL (http://<ip>:8080)
  - print an ASCII QR for the first non-loopback URL
  - `--bind 127.0.0.1` flag for single-device mode
---
