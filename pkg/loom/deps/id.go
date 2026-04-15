package deps

import (
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
)

// IDGenerator generates unique task identifiers.
type IDGenerator interface {
	NewID() string
}

// uuidGenerator is the production IDGenerator backed by UUID v7 with v4 fallback.
type uuidGenerator struct{}

// UUIDGenerator returns an IDGenerator using uuid.NewV7 (fallback: uuid.New).
func UUIDGenerator() IDGenerator { return uuidGenerator{} }

func (uuidGenerator) NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New().String()
	}
	return id.String()
}

// SequentialIDGenerator is a deterministic IDGenerator for use in tests.
// It returns IDs in the form "id-0", "id-1", "id-2", ... using an atomic counter.
type SequentialIDGenerator struct {
	counter atomic.Int64
}

// NewSequentialIDGenerator returns a SequentialIDGenerator starting at zero.
func NewSequentialIDGenerator() *SequentialIDGenerator { return &SequentialIDGenerator{} }

// NewID returns the next sequential ID in the form "id-N". Concurrent calls are safe.
func (g *SequentialIDGenerator) NewID() string {
	n := g.counter.Add(1) - 1
	return fmt.Sprintf("id-%d", n)
}
