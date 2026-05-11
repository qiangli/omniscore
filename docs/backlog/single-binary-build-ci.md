---
title: Single-binary build + GitHub Actions CI matrix
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - Makefile: `make build`, `make dev`, `make test`, `make clean`
  - CI runs go vet, gofmt -l, go test ./..., npm run build, npm run lint
  - release workflow cross-compiles for darwin/{arm64,amd64}, linux/amd64, windows/amd64
  - all four binaries pure-Go, no CGO
---
