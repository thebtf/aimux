# Tasks: Investigate Enhancements v2

**Spec:** .agent/specs/sprint7-investigate-enhance/spec.md
**Generated:** 2026-04-07

## Phase 1: New Domains + Auto-Detection

- [x] T001 Add AutoDetectDomain(topic string) string to pkg/investigate/domains.go — keyword scanning with priority: security > performance > architecture > debugging > research > generic
- [x] T002 Add SecurityDomain to pkg/investigate/domains.go — 8 coverage areas, 8 patterns, 3 angles
- [x] T003 Add PerformanceDomain to pkg/investigate/domains.go — 8 coverage areas, 6 patterns, 3 angles
- [x] T004 Add ArchitectureDomain to pkg/investigate/domains.go — 8 coverage areas, 4 patterns, 3 angles
- [x] T005 Add ResearchDomain to pkg/investigate/domains.go — 8 coverage areas, 4 patterns, 3 angles
- [x] T006 Update start action in pkg/server/server.go — call AutoDetectDomain when domain param empty
- [x] T007 [P] Tests: AutoDetectDomain keyword mapping, new domain registration, start auto-detection

---

## Phase 2: Cross-Tool Dispatch

- [x] T008 Add DispatchThinkCall(pattern string, params map[string]any) (*think.ThinkResult, error) to pkg/investigate/dispatch.go — calls think.GetPattern + Validate + Handle in-process
- [x] T009 Update Assess in pkg/investigate/assess.go — when auto_dispatch=true, parse suggested_think_call, call DispatchThinkCall, add result as finding
- [x] T010 Update assess action in pkg/server/server.go — pass auto_dispatch param to Assess
- [x] T011 [P] Tests: dispatch think call, assess with auto-dispatch, assess without auto-dispatch (backward compat)

---

## Phase 3: Polish

- [x] T012 Full regression: go build ./... && go vet ./... && go test ./... -timeout 300s
- [x] T013 Update CONTINUITY.md

## Dependencies

- T001-T005 independent (can parallelize)
- T006 depends on T001 (auto-detect before server wiring)
- T008 independent
- T009 depends on T008
- T010 depends on T009
