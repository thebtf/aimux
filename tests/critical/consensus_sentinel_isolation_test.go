//go:build !short

// T015 — @critical TestCritical_Consensus_PerCLISentinelIsolation.
//
// AIMUX-14 CR-001 Phase 2 (US2). Verifies per-Session sentinel pattern
// detection isolates round boundaries — round N's response для CLI A is
// never contaminated с CLI B's output even when Sends are interleaved.
//
// Anti-stub: removing per-Session sentinel state (e.g., a single shared
// reader) would mix outputs across CLIs and the per-session content
// assertion would fail.

package critical

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/session"
)

func TestCritical_Consensus_PerCLISentinelIsolation(t *testing.T) {
	bin := buildPersistentTestCLI(t)

	type cliSpec struct {
		name     string
		sentinel string
	}
	clis := []cliSpec{
		{name: "codex", sentinel: "===CODEX_END==="},
		{name: "gemini", sentinel: "===GEMINI_END==="},
		{name: "claude", sentinel: "===CLAUDE_END==="},
	}

	type sessionState struct {
		spec    cliSpec
		sess    *session.BaseSession
		cleanup func()
	}

	states := make([]sessionState, len(clis))
	for i, c := range clis {
		cmd := exec.Command(bin)
		// Inherit current env then override sentinel so subprocess startup
		// works cross-platform (PATH, SystemRoot on Windows etc preserved).
		cmd.Env = append(os.Environ(), "PERSISTENT_TESTCLI_SENTINEL="+c.sentinel)

		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatalf("[%s] StdinPipe: %v", c.name, err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("[%s] StdoutPipe: %v", c.name, err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("[%s] cmd.Start: %v", c.name, err)
		}
		sess := session.New(c.name, stdin, stdout, 5*time.Second, nil, nil,
			"^"+regexpQuoteMeta(c.sentinel)+"$")
		states[i] = sessionState{
			spec: c,
			sess: sess,
			cleanup: func() {
				_ = sess.Close()
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			},
		}
	}
	defer func() {
		for _, s := range states {
			s.cleanup()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Interleaved rounds — each round drives ALL clis concurrently.
	const rounds = 5
	for round := 1; round <= rounds; round++ {
		var wg sync.WaitGroup
		errs := make([]error, len(states))
		responses := make([]string, len(states))
		for i, st := range states {
			wg.Add(1)
			go func(idx int, s sessionState) {
				defer wg.Done()
				prompt := fmt.Sprintf("%s-round-%d", s.spec.name, round)
				res, err := s.sess.Send(ctx, prompt)
				if err != nil {
					errs[idx] = err
					return
				}
				responses[idx] = res.Content
			}(i, st)
		}
		wg.Wait()

		for i, e := range errs {
			if e != nil {
				t.Fatalf("[%s] round %d Send: %v", states[i].spec.name, round, e)
			}
		}

		// Per-CLI assertion: each response must contain the CLI's own prompt
		// AND the CLI's own sentinel — NOT another CLI's prompt or sentinel.
		for i, s := range states {
			ownPrompt := fmt.Sprintf("%s-round-%d", s.spec.name, round)
			if !strings.Contains(responses[i], ownPrompt) {
				t.Errorf("[%s] round %d: response missing own prompt %q (got %q)",
					s.spec.name, round, ownPrompt, responses[i])
			}
			if !strings.Contains(responses[i], s.spec.sentinel) {
				t.Errorf("[%s] round %d: response missing own sentinel %q (got %q)",
					s.spec.name, round, s.spec.sentinel, responses[i])
			}
			// Cross-contamination check: must NOT contain another CLI's prompt or sentinel.
			for j, other := range states {
				if i == j {
					continue
				}
				otherPrompt := fmt.Sprintf("%s-round-%d", other.spec.name, round)
				if strings.Contains(responses[i], otherPrompt) {
					t.Errorf("[%s] round %d: response contains CROSS-CONTAMINATION from %s prompt %q",
						s.spec.name, round, other.spec.name, otherPrompt)
				}
				if strings.Contains(responses[i], other.spec.sentinel) {
					t.Errorf("[%s] round %d: response contains CROSS-CONTAMINATION from %s sentinel",
						s.spec.name, round, other.spec.name)
				}
			}
		}
	}

	t.Logf("US2 sentinel isolation verified: %d CLIs × %d rounds, no cross-contamination",
		len(states), rounds)

	// Anti-stub: subprocess survival is implicitly proven by 5 rounds of
	// successful Sends per CLI without error — if any subprocess died
	// mid-test, the next Send would block on EOF and the ctx timeout
	// would surface as test failure.
}

// regexpQuoteMeta minimal QuoteMeta substitute — sentinel strings here contain
// only `=` and ASCII letters; no real regex metacharacters need escaping.
func regexpQuoteMeta(s string) string { return s }
