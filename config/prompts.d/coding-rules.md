# Coding Agent Anti-Stub Rules

These rules are NON-NEGOTIABLE. Every function you write MUST comply.

## Parameter Usage (STUB-DISCARD prevention)
- Every function parameter MUST influence the return value or cause a side effect
- `_ = param` is FORBIDDEN — if a parameter is not needed, remove it from the signature
- If you compute a value from parameters, you MUST use it in the return path

## Return Value Integrity (STUB-HARDCODED prevention)
- A function that accepts parameters MUST compute its return value FROM those parameters
- Returning a hardcoded string literal is a stub and is FORBIDDEN
- Examples of forbidden returns:
  - `return "delegating to exec"`
  - `return "not yet implemented"`
  - `return "wiring pending"`
  - `return mcp.NewToolResultText('{"status":"placeholder"}')`

## Implementation Completeness (STUB-NOOP prevention)
- A function body MUST contain logic beyond logging and returning
- If a function's entire body is `log.Info() + return`, it is a no-op stub
- Every code path must perform the operation described by the function name

## No Deferred Implementation (STUB-TODO prevention)
- TODO, FIXME, SCAFFOLD, PLACEHOLDER comments are FORBIDDEN in new code
- If you cannot implement something fully, say so explicitly — do not hide it in a comment
- "For now" / "will be added later" / "Phase N" are stub indicators

## Self-Audit Gate (run before reporting "done")
Before marking any task complete, verify for EACH function you wrote:
1. Does every parameter influence the output? If no → STUB-DISCARD
2. Is every return value computed from inputs? If no → STUB-HARDCODED
3. Does the function do real work beyond logging? If no → STUB-NOOP
4. Are there any TODO/FIXME comments? If yes → STUB-TODO
5. Would replacing the function body with `return nil` fail at least one test? If no → STUB-TEST-STRUCTURAL

If ANY answer indicates a stub, fix it before reporting done.
