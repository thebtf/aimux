package orchestrator

// Pheromone keys for job metadata markers (FR-12).
const (
	PheromoneDiscovery  = "discovery"  // found useful pattern/file
	PheromoneWarning    = "warning"    // risk detected, proceed with caution
	PheromoneRepellent  = "repellent"  // tried this approach, failed — don't retry
	PheromoneProgress   = "progress"   // working on specific area
)

// PheromoneReader checks pheromone markers before strategy decisions.
type PheromoneReader struct{}

// NewPheromoneReader creates a reader.
func NewPheromoneReader() *PheromoneReader {
	return &PheromoneReader{}
}

// ShouldSkipApproach returns true if a repellent marker exists for the approach.
func (r *PheromoneReader) ShouldSkipApproach(pheromones map[string]string, approach string) bool {
	if repellent, ok := pheromones[PheromoneRepellent]; ok {
		return repellent == approach
	}
	return false
}

// GetDiscovery returns the discovery marker value, if any.
func (r *PheromoneReader) GetDiscovery(pheromones map[string]string) string {
	return pheromones[PheromoneDiscovery]
}

// HasWarning returns true if a warning marker exists.
func (r *PheromoneReader) HasWarning(pheromones map[string]string) bool {
	_, ok := pheromones[PheromoneWarning]
	return ok
}
