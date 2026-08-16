# Release helper tests

The controller is implemented and tested in Go. Run its tests from the repository root:

```bash
go test ./...
```

This directory only tests the Bash helpers used by GitHub Actions for Docker Hub descriptions, release notes, and `latest` tag reconciliation.

```bash
./tests/run_tests.sh
./tests/run_tests.sh --list
./tests/run_tests.sh --filter release
```

The helper suite needs Bash, coreutils, GNU grep, and awk. Reconciliation cases also need `jq`; they skip when it is unavailable.

| File | Coverage |
| --- | --- |
| `cases/12_github_workflows.sh` | Publishing workflow invariants and SPDX headers |
| `cases/13_dockerhub_description.sh` | Docker Hub description generation |
| `cases/14_latest_tag_reconciliation.sh` | Registry `latest` tag reconciliation |
| `cases/15_test_runner.sh` | Test runner failure and discovery behavior |
| `cases/16_release_note_publication.sh` | Idempotent GitHub release-note publication |
| `cases/17_reports.sh` | JUnit and Markdown report generation |