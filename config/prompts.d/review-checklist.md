Review each hunk of the provided diff against this checklist:

1. **Correctness**: Does the code do what the task asks?
2. **Security**: Any injection, hardcoded secrets, unsafe input?
3. **Performance**: Obvious inefficiencies? N+1 queries? Unbounded loops?
4. **Style**: Follows project conventions? Naming consistent?
5. **Tests**: Are edge cases covered? Missing error handling?
6. **Scope**: Does the change stay within the requested scope?
7. **Completeness**: Is this REAL implementation, not a stub? Check for these stub patterns:
   - `_ = variable` — value computed then discarded (STUB-PASSTHROUGH)
   - Function returns hardcoded string like "delegating to..." (STUB-HARDCODED)
   - Function body is only logging + return with no real logic (STUB-NOOP)
   - TODO/FIXME/SCAFFOLD comments indicating unfinished work (STUB-TODO)
   - Parameters received but never used in computation (STUB-DISCARD)
   - Interface method returns only zero/default values (STUB-INTERFACE-EMPTY)
   - Test only checks constructor, never verifies behavior (STUB-TEST-STRUCTURAL)
   - Exported function with no test exercising it (STUB-COVERAGE-ZERO)
   - Every parameter MUST influence the return value or cause a side effect
   - If you find ANY of these patterns: verdict MUST be `changes_requested`
     with the STUB-* rule ID in the comment. Do NOT auto-fix stubs (no `modified`).

For each hunk, provide a verdict:
- `approved`: hunk is correct, safe, AND complete (no stubs)
- `modified`: provide fixed version in `modified` field (NOT for stubs — only for minor fixes)
- `changes_requested`: explain what's wrong in `comment` field. For stubs, include STUB-* rule ID.

Respond as JSON array:
```json
[{"hunk_index": 0, "verdict": "approved|modified|changes_requested", "comment": "...", "modified": "..."}]
```
