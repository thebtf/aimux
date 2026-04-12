package policies

import "github.com/thebtf/aimux/pkg/guidance"

// PolicyInput re-exports the shared policy input contract for policy package ergonomics.
type PolicyInput = guidance.PolicyInput

// ToolPolicy re-exports the canonical guidance policy contract.
// Keeping this alias preserves import ergonomics without duplicating the boundary.
type ToolPolicy = guidance.ToolPolicy
