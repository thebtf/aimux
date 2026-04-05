Review each hunk of the provided diff against this checklist:

1. **Correctness**: Does the code do what the task asks?
2. **Security**: Any injection, hardcoded secrets, unsafe input?
3. **Performance**: Obvious inefficiencies? N+1 queries? Unbounded loops?
4. **Style**: Follows project conventions? Naming consistent?
5. **Tests**: Are edge cases covered? Missing error handling?
6. **Scope**: Does the change stay within the requested scope?

For each hunk, provide a verdict:
- `approved`: hunk is correct and safe
- `modified`: provide fixed version in `modified` field
- `changes_requested`: explain what's wrong in `comment` field

Respond as JSON array:
```json
[{"hunk_index": 0, "verdict": "approved|modified|changes_requested", "comment": "...", "modified": "..."}]
```
