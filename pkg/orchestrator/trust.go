package orchestrator

// DomainAuthority maps domain names to authoritative CLIs.
// Constitution P13: Cross-model reviews weight opinions by domain authority.
// Authoritative = veto power: can reject changes in its domain even if other model approved.
type DomainAuthority struct {
	domains map[string]string // domain → authoritative CLI
}

// NewDomainAuthority creates a domain trust hierarchy.
func NewDomainAuthority() *DomainAuthority {
	return &DomainAuthority{
		domains: map[string]string{
			"backend":  "codex",
			"logic":    "codex",
			"security": "codex",
			"frontend": "gemini",
			"ui":       "gemini",
			"design":   "gemini",
		},
	}
}

// IsAuthoritative checks if a CLI has veto power for a given domain.
func (d *DomainAuthority) IsAuthoritative(cli, domain string) bool {
	auth, ok := d.domains[domain]
	return ok && auth == cli
}

// GetAuthority returns the authoritative CLI for a domain, or empty string.
func (d *DomainAuthority) GetAuthority(domain string) string {
	return d.domains[domain]
}

// SetAuthority configures domain authority (for custom overrides from config).
func (d *DomainAuthority) SetAuthority(domain, cli string) {
	d.domains[domain] = cli
}
