# omniscore skills

Operational playbooks for content-pipeline tasks, written in the ycode skill
format (YAML frontmatter + markdown body). Each subdirectory is one
user-invocable skill.

| Skill | Purpose |
|---|---|
| [`convert-sat-pdf`](convert-sat-pdf/skill.md)         | One Digital SAT Bluebook PDF set → `data/omni-data/sat/<slug>/`. |
| [`convert-ap-pdf`](convert-ap-pdf/skill.md)           | One College Board AP released-exam PDF → `data/omni-data/ap/<slug>/`. |
| [`validate-imported-test`](validate-imported-test/skill.md) | Run structural checks against an emitted or hand-edited `test.json`. |

The underlying binaries (`bin/sat-import`, `bin/ap-import`) are documented
in [`CLAUDE.md`](../CLAUDE.md). The skill files are the human-facing
how-to; the importer source is at [`internal/importer/`](../internal/importer/).
