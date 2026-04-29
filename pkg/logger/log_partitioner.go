// Package logger: LogPartitioner routes log entries to per-tenant log files.
// T038 — AIMUX-12 Phase 6: per-tenant log file routing.
//
// Design notes:
//   - sync.Map provides lock-free reads on the hot path (existing tenant).
//   - Each tenant gets its own lumberjack.Logger for rotation semantics.
//   - File mode 0600 is enforced at creation time on Unix (NFR-11).
//   - Path traversal protection: sanitizeTenantID rejects any ID containing
//     directory separators, null bytes, or leading dots before a file is opened.
//   - The fallback lumberjack.Logger handles the empty-tenantID legacy path
//     (legacy-default mode, FR-12 amendment).
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thebtf/aimux/pkg/tenant"
	"golang.org/x/text/unicode/norm"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// LogPartitionerWriter is the interface satisfied by LogPartitioner.
// Tests use this interface to inject a mock without importing the concrete type.
type LogPartitionerWriter interface {
	WriteFor(tenantID string, entry []byte) (int, error)
}

// LogPartitioner routes log entry bytes to per-tenant lumberjack files.
// Instances are safe for concurrent use. The zero value is not valid;
// use NewLogPartitioner.
type LogPartitioner struct {
	baseDir  string
	registry *tenant.TenantRegistry // optional; nil in single-tenant mode
	fallback *lumberjack.Logger     // receives empty-tenantID writes (legacy-default)

	// loggers holds *lumberjack.Logger values keyed by tenantID string.
	// sync.Map is used so the hot path (existing tenant key) is read-lock-free.
	loggers sync.Map

	// initMu prevents concurrent open of the same tenant file on first write.
	// The outer sync.Map handles the read-side; this mutex serialises the rare
	// first-write race for each tenantID.
	initMu sync.Mutex
}

// NewLogPartitioner creates a LogPartitioner that writes tenant log files under
// baseDir. registry is optional (may be nil in legacy single-tenant mode).
// fallback is required and receives writes for empty tenantID.
//
// Panics if fallback is nil.
func NewLogPartitioner(baseDir string, registry *tenant.TenantRegistry, fallback *lumberjack.Logger) *LogPartitioner {
	if fallback == nil {
		panic("logger.NewLogPartitioner: fallback must not be nil")
	}
	return &LogPartitioner{
		baseDir:  baseDir,
		registry: registry,
		fallback: fallback,
	}
}

// WriteFor appends entry bytes to the log file for the given tenantID.
//
// Routing rules:
//   - tenantID == "" → writes to the fallback lumberjack logger (legacy-default path).
//   - tenantID fails sanitizeTenantID → writes to fallback (path traversal protection).
//   - otherwise → lazy-opens <baseDir>/<tenantID>.log and writes there.
//
// The method is safe to call concurrently from multiple goroutines.
// It returns (n, err) mirroring io.Writer semantics.
func (p *LogPartitioner) WriteFor(tenantID string, entry []byte) (int, error) {
	// Empty tenantID → legacy fallback.
	if tenantID == "" || tenantID == tenant.LegacyDefault {
		return p.fallback.Write(entry)
	}

	// Sanitize before any filesystem operation.
	safeName, ok := sanitizeTenantID(tenantID)
	if !ok {
		// Dangerous ID — route to fallback to avoid silent drop.
		return p.fallback.Write(entry)
	}

	// Fast path: writer already open.
	if v, loaded := p.loggers.Load(safeName); loaded {
		lj := v.(*lumberjack.Logger)
		return lj.Write(entry)
	}

	// Slow path: first write for this tenant — open the file under a mutex
	// to prevent two goroutines from both creating the same lumberjack instance.
	p.initMu.Lock()
	// Re-check inside the lock (another goroutine may have won).
	if v, loaded := p.loggers.Load(safeName); loaded {
		p.initMu.Unlock()
		lj := v.(*lumberjack.Logger)
		return lj.Write(entry)
	}

	lj, err := p.openTenantLogger(safeName)
	if err != nil {
		p.initMu.Unlock()
		// Cannot open the file — route to fallback rather than dropping.
		_, _ = fmt.Fprintf(os.Stderr, "aimux: LogPartitioner: open tenant %q: %v\n", safeName, err)
		return p.fallback.Write(entry)
	}
	p.loggers.Store(safeName, lj)
	p.initMu.Unlock()

	return lj.Write(entry)
}

// Close shuts down all opened tenant lumberjack instances. Subsequent WriteFor
// calls will reopen lumberjack handles. Safe to call multiple times.
func (p *LogPartitioner) Close() error {
	var firstErr error
	p.loggers.Range(func(key, value any) bool {
		lj := value.(*lumberjack.Logger)
		if err := lj.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		p.loggers.Delete(key)
		return true
	})
	return firstErr
}

// openTenantLogger creates or opens the lumberjack.Logger for the given safe tenant name.
// Called once per tenant under initMu. safeName has already been validated by
// sanitizeTenantID — it contains no path separators or control characters.
func (p *LogPartitioner) openTenantLogger(safeName string) (*lumberjack.Logger, error) {
	if err := os.MkdirAll(p.baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create baseDir %s: %w", p.baseDir, err)
	}

	path := filepath.Join(p.baseDir, safeName+".log")

	// Enforce mode 0600 on Unix at creation time. On Windows, os.OpenFile with
	// 0o600 is a no-op for ACLs, but the intent is recorded for FR-14 compliance.
	if err := createFileIfNotExists(path, 0o600); err != nil {
		return nil, err
	}

	lj := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    100, // 100 MB default; operator may override via config (future)
		MaxBackups: 3,
		MaxAge:     30,
		Compress:   false,
		LocalTime:  true,
	}
	return lj, nil
}

// sanitizeTenantID validates that the tenantID is safe to use as a file name
// component. Returns (safeName, true) on success or ("", false) on rejection.
//
// Security policy (W1 — AIMUX-12 v5.1.0):
// Strict ASCII allowlist [a-zA-Z0-9_-] after NFC normalization. This defeats
// Unicode visual-spoofing where two distinct tenant IDs render identically in
// operator audit logs (Cyrillic homoglyphs, RTL override, zero-width joiner,
// decomposed combining marks).
//
// Rejected patterns:
//   - empty string
//   - any character outside [a-zA-Z0-9_-]
//   - any Unicode codepoint above U+007F (non-ASCII)
//   - input that would change under NFC normalization (denormalized form)
//   - leading "." (defense-in-depth — '.' already excluded by allowlist, but
//     keep explicit for self-documenting policy)
//   - contains ".." (defense-in-depth — '.' already excluded by allowlist)
func sanitizeTenantID(id string) (string, bool) {
	if id == "" {
		return "", false
	}

	// Reject if NFC normalization would mutate the input. A caller that
	// passed a denormalized form (decomposed combining marks) is rejected
	// rather than silently normalized — we want byte-identical audit trails.
	if !norm.NFC.IsNormalString(id) {
		return "", false
	}

	// Defense-in-depth: leading dot.
	if id[0] == '.' {
		return "", false
	}

	// Strict ASCII allowlist. Iterate as runes so a multi-byte non-ASCII
	// codepoint is detected as a single illegal rune, not as multiple bytes.
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return "", false
		}
	}

	// Defense-in-depth: belt-and-braces against future allowlist drift.
	// '.' is already excluded above, so ".." cannot appear — keep the check
	// so a future maintainer who relaxes the allowlist still cannot regress
	// path-traversal protection silently.
	if strings.Contains(id, "..") {
		return "", false
	}

	return id, true
}
