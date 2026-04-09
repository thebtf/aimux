# Tasks: aimux v3 Audit Fixes

**Spec:** spec.md
**Plan:** plan.md
**Generated:** 2026-04-06
**Rule:** NO task marked [x] without VERIFY counterpart also [x].

## Phase 1: P1 Critical Fixes

- [x] T001 [P] Fix data race: add safeBuffer (mutex-protected bytes.Buffer) for pipe executor stdout in D:\Dev\aimux\pkg\executor\pipe\pipe.go
- [x] VERIFY-T001 Confirm: (a) safeBuffer wraps all read/write, (b) build passes, (c) `go test ./pkg/executor/pipe/...` passes, (d) no concurrent access to raw bytes.Buffer

- [x] T002 [P] Enforce MaxConcurrentJobs: CountRunning() + checkConcurrencyLimit() at all 3 async spawn points in D:\Dev\aimux\pkg\server\server.go and D:\Dev\aimux\pkg\session\jobs.go
- [x] VERIFY-T002 Confirm: (a) CountRunning() exists, (b) check at all 3 `go executeJob`/`executePairCoding` calls, (c) build + tests pass

---

**Checkpoint:** Both P1 critical issues fixed. No data races, no unbounded job creation.

## Phase 2: Dead Code Removal

- [x] T003 Delete 7 dead files (chains.go/progress.go didn't exist): spark.go, buffer.go, backoff.go, kill.go, assignments.go, handoff.go, trust.go in D:\Dev\aimux\pkg\
- [x] VERIFY-T003 Confirm: (a) 7 files deleted, (b) `go build` passes, (c) `go test` all 18 packages pass

- [x] T004 Delete unused exports: Registry.IsAvailable, Registry.All, BuildStdinArgs, Selector.SelectByName, IsWindows + unused runtime import in D:\Dev\aimux\pkg\
- [x] VERIFY-T004 Confirm: (a) functions removed, (b) grep confirms no references, (c) `go build` passes. Note: parseAuditFindings is private+used (not dead, just duplicate — refactor later).

- [x] T005 Delete shutdown.go — entirely comments, no live code in D:\Dev\aimux\cmd\aimux\shutdown.go
- [x] VERIFY-T005 Confirm: (a) file deleted, (b) `go build` passes

---

**Checkpoint:** Dead code eliminated. LOC reduced. Codebase leaner.

## Phase 3: P2 High Fixes

- [x] T006 [P] Fix mergeEnv: prepend os.Environ() in pipe.go, conpty.go, pty.go in D:\Dev\aimux\pkg\executor\
- [x] VERIFY-T006 Confirm: (a) os.Environ() used as base in all 3 executors, (b) build passes, (c) tests pass

- [x] T007 Fix pipeSession.Send truncation: replace partial-read break with timeout-based reading in D:\Dev\aimux\pkg\executor\pipe\pipe.go
- [x] VERIFY-T007 Confirm: (a) no `n < len(tmp)` break, (b) 500ms inactivity timeout, (c) goroutine-based async read, (d) tests pass

- [x] T008 Fix GenAI client leak: hoist client to struct field (lazy init), add Close(), return cacheHit bool in D:\Dev\aimux\pkg\tools\deepresearch\client.go
- [x] VERIFY-T008 Confirm: (a) genai client created once (lazy), (b) Close() method exists, (c) Research returns (content, cacheHit, error), (d) server handler uses cacheHit, (e) tests pass

- [x] T009 [P] Validate CWD: filepath.Clean + os.Stat in exec handler in D:\Dev\aimux\pkg\server\server.go
- [x] VERIFY-T009 Confirm: (a) CWD cleaned + stat checked, (b) non-existent returns error, (c) non-directory returns error, (d) tests pass

- [x] T010 [P] Sanitize effort args: strings.ReplaceAll instead of fmt.Sprintf in D:\Dev\aimux\pkg\server\server.go
- [x] VERIFY-T010 Confirm: (a) no fmt.Sprintf with user effort, (b) "%" in effort safe, (c) build passes

- [x] T011 Implement sessions kill/gc: kill fails jobs + deletes session, gc collects completed/failed sessions in D:\Dev\aimux\pkg\server\server.go
- [x] VERIFY-T011 Confirm: (a) case "kill" and "gc" in switch, (b) kill fails running jobs + deletes, (c) gc collects terminal sessions, (d) build+test passes

- [x] T012 Fix WAL recovery mutex: Import() methods on Registry/JobManager, RecoverFromWAL uses them in D:\Dev\aimux\pkg\session\
- [x] VERIFY-T012 Confirm: (a) Import() locks mutex, (b) RecoverFromWAL uses Import (not direct map write), (c) tests pass

---

**Checkpoint:** All P2 high issues fixed. Environment inheritance, session control, security hardening.

## Phase 4: P3-P4 Fixes

- [x] T013 [P] Add prompt size limit: MaxPromptBytes config (default 1MB), check in exec handler
- [x] VERIFY-T013 Confirm: (a) config field + default, (b) check before execution, (c) build+test passes

- [x] T014 [P] Fix SnapshotAll transaction: inlined SQL in tx.Exec, not s.db.Exec
- [x] VERIFY-T014 Confirm: (a) tx.Exec used for both sessions and jobs, (b) build passes

- [x] T015 [P] Fix cached field: already fixed in T008 (Research returns cacheHit bool, server uses it)
- [x] VERIFY-T015 Confirmed in T008

- [x] T016 Implement async for consensus/debate: executeStrategy helper, async branching with job_id
- [x] VERIFY-T016 Confirm: (a) async returns job_id, (b) sync unchanged, (c) concurrency limit checked, (d) build+test passes

- [x] T017 Store CancelFunc: context.WithCancel at all 5 async spawn points, RegisterCancel + CancelJob in JobManager, cancel handler uses CancelJob
- [x] VERIFY-T017 Confirm: (a) 0 context.Background() in async spawns, (b) RegisterCancel at all 5 points, (c) CancelJob cancels context + fails job, (d) build+test passes

---

**Checkpoint:** All P3-P4 fixes done.

## Phase 5: Final Verification

- [x] T018 stub-grep.sh → "PASSED: 0 stub patterns found"
- [x] VERIFY-T018 Confirmed

- [x] T019 go test ./... → 17 packages PASS, 0 failures (cmd/aimux has no test files)
- [x] VERIFY-T019 Confirmed

- [x] T020 go build ./... → clean
- [x] VERIFY-T020 Confirmed

- [x] T021 LOC: 6739 production (down from ~7200 before dead code removal). 70 commits total.
- [x] VERIFY-T021 Confirmed

---

**Checkpoint:** All audit findings resolved. Production hardened.

## Dependencies

- T001, T002 independent (parallel)
- T003, T004, T005 independent (parallel), no dependency on Phase 1
- T006-T012 independent of each other, recommended after Phase 2 (cleaner)
- T013-T017 independent of each other, after Phase 3
- T018-T021 after all

## Execution Strategy

- **Parallel:** T001||T002, T003||T004||T005, T006||T009||T010, T013||T014||T015
- **Commit:** one per verified task pair (or batch per phase)
- **Total:** 21 tasks + 21 verifications = 42 items
