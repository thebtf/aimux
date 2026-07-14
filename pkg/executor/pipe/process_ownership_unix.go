//go:build !windows

package pipe

import "github.com/thebtf/aimux/pkg/types"

func processOwnershipBoundary() types.ProcessOwnershipBoundary {
	return types.ProcessOwnershipBoundaryProcessGroup
}
