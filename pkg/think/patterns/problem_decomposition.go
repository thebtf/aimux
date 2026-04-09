package patterns

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	think "github.com/thebtf/aimux/pkg/think"
)

type problemDecompositionPattern struct {
	sampling think.SamplingProvider
}

// NewProblemDecompositionPattern returns the "problem_decomposition" pattern handler.
func NewProblemDecompositionPattern() think.PatternHandler { return &problemDecompositionPattern{} }

// SetSampling injects the sampling provider. Implements think.SamplingAwareHandler.
func (p *problemDecompositionPattern) SetSampling(provider think.SamplingProvider) {
	p.sampling = provider
}

func (p *problemDecompositionPattern) Name() string { return "problem_decomposition" }

func (p *problemDecompositionPattern) Description() string {
	return "Break a problem into sub-problems, dependencies, risks, and stakeholders"
}

func (p *problemDecompositionPattern) Validate(input map[string]any) (map[string]any, error) {
	problem, ok := input["problem"]
	if !ok {
		return nil, fmt.Errorf("missing required field: problem")
	}
	s, ok := problem.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("field 'problem' must be a non-empty string")
	}
	out := map[string]any{"problem": s}
	if v, ok := input["methodology"].(string); ok {
		out["methodology"] = v
	}
	if v, ok := input["subProblems"].([]any); ok {
		out["subProblems"] = v
	}
	if v, ok := input["dependencies"].([]any); ok {
		out["dependencies"] = v
	}
	if v, ok := input["risks"].([]any); ok {
		out["risks"] = v
	}
	if v, ok := input["stakeholders"].([]any); ok {
		out["stakeholders"] = v
	}
	return out, nil
}

func (p *problemDecompositionPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	problem := validInput["problem"].(string)

	countSlice := func(key string) int {
		if v, ok := validInput[key].([]any); ok {
			return len(v)
		}
		return 0
	}

	methodology := ""
	if v, ok := validInput["methodology"].(string); ok {
		methodology = v
	}

	// When subProblems is absent/empty and a sampling provider is available,
	// ask the LLM to decompose the problem and derive subProblems + dependencies.
	subProblemsProvided := countSlice("subProblems") > 0
	if !subProblemsProvided && p.sampling != nil {
		generated, err := p.generateDecomposition(problem)
		if err == nil && generated != nil {
			// Merge generated data into validInput so the rest of Handle uses it.
			validInput = mergeGenerated(validInput, generated)
		}
		// On error: fall through silently — graceful degradation.
	}

	// Auto-analysis: when subProblems and dependencies are still empty after sampling
	// (or sampling is unavailable), derive suggestions from domain templates.
	var suggestedSubProblems []string
	var suggestedDependencies []map[string]string
	var autoAnalysisSource string

	if countSlice("subProblems") == 0 && countSlice("dependencies") == 0 {
		_ = ExtractKeywords(problem) // extract for future enrichment; used via MatchDomainTemplate
		tmpl := MatchDomainTemplate(problem)
		if tmpl != nil {
			suggestedSubProblems = tmpl.SubProblems
			for _, dep := range tmpl.Dependencies {
				suggestedDependencies = append(suggestedDependencies, dep)
			}
			autoAnalysisSource = "domain-template"
		} else {
			autoAnalysisSource = "keyword-analysis"
		}
	}

	data := map[string]any{
		"problem":          problem,
		"methodology":      methodology,
		"subProblemCount":  countSlice("subProblems"),
		"dependencyCount":  countSlice("dependencies"),
		"riskCount":        countSlice("risks"),
		"stakeholderCount": countSlice("stakeholders"),
		"totalComponents":  countSlice("subProblems") + countSlice("dependencies") + countSlice("risks") + countSlice("stakeholders"),
	}

	// Include auto-analysis suggestions when no user-supplied subProblems/dependencies.
	if len(suggestedSubProblems) > 0 || autoAnalysisSource != "" {
		data["suggestedSubProblems"] = suggestedSubProblems
		data["suggestedDependencies"] = suggestedDependencies
		data["autoAnalysis"] = map[string]any{"source": autoAnalysisSource}

		// Run DAG on suggested dependencies.
		if len(suggestedDependencies) > 0 {
			dagDeps := make([]any, len(suggestedDependencies))
			for i, d := range suggestedDependencies {
				dagDeps[i] = map[string]any{"from": d["from"], "to": d["to"]}
			}
			edges := extractDagDependencies(dagDeps)
			if edges != nil {
				spAny := make([]any, len(suggestedSubProblems))
				for i, s := range suggestedSubProblems {
					spAny[i] = s
				}
				dagRes := analyzeDag(edges, spAny)
				data["dag"] = map[string]any{
					"hasCycle":          dagRes.hasCycle,
					"cyclePath":         dagRes.cyclePath,
					"topologicalOrder":  dagRes.topologicalOrder,
					"orphanSubProblems": dagRes.orphanSubProblems,
				}
			}
		}
	}

	// DAG analysis — only when dependencies are provided as {from, to} objects.
	if deps, ok := validInput["dependencies"].([]any); ok && len(deps) > 0 {
		edges := extractDagDependencies(deps)
		if edges != nil {
			subProblems, _ := validInput["subProblems"].([]any)
			result := analyzeDag(edges, subProblems)
			data["dag"] = map[string]any{
				"hasCycle":          result.hasCycle,
				"cyclePath":         result.cyclePath,
				"topologicalOrder":  result.topologicalOrder,
				"orphanSubProblems": result.orphanSubProblems,
			}
		}
	}

	// Guidance — always included.
	data["guidance"] = BuildGuidance("problem_decomposition",
		func() string {
			if countSlice("subProblems") > 0 {
				return "full"
			}
			if autoAnalysisSource != "" {
				return "enriched"
			}
			return "basic"
		}(),
		[]string{"subProblems", "dependencies", "risks", "stakeholders"},
	)

	return think.MakeThinkResult("problem_decomposition", data, sessionID, nil, "", []string{"totalComponents"}), nil
}

// samplingDecomposition is the JSON shape we ask the LLM to return.
type samplingDecomposition struct {
	SubProblems  []map[string]any `json:"subProblems"`
	Dependencies []map[string]any `json:"dependencies"`
}

// generateDecomposition calls the sampling provider to auto-decompose problem.
// Returns nil, error on any failure so the caller can gracefully degrade.
func (p *problemDecompositionPattern) generateDecomposition(problem string) (*samplingDecomposition, error) {
	prompt := fmt.Sprintf(
		`Decompose this problem into 3-7 sub-problems with dependencies. `+
			`Problem: %s. `+
			`Return JSON: {"subProblems": [{"id": "sp1", "description": "..."}], `+
			`"dependencies": [{"from": "sp1", "to": "sp2"}]}`,
		problem,
	)
	messages := []think.SamplingMessage{
		{Role: "user", Content: prompt},
	}
	raw, err := p.sampling.RequestSampling(context.Background(), messages, 2000)
	if err != nil {
		return nil, err
	}
	var result samplingDecomposition
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("sampling JSON parse failed: %w", err)
	}
	return &result, nil
}

// mergeGenerated returns a new validInput map with subProblems and dependencies
// populated from the sampling result (preserves existing keys unchanged).
func mergeGenerated(base map[string]any, generated *samplingDecomposition) map[string]any {
	out := make(map[string]any, len(base)+2)
	for k, v := range base {
		out[k] = v
	}
	subProblems := make([]any, len(generated.SubProblems))
	for i, sp := range generated.SubProblems {
		subProblems[i] = sp
	}
	deps := make([]any, len(generated.Dependencies))
	for i, d := range generated.Dependencies {
		deps[i] = d
	}
	out["subProblems"] = subProblems
	out["dependencies"] = deps
	return out
}

// dagEdge represents a directed edge from → to in the dependency graph.
type dagEdge struct{ from, to string }

// dagResult holds the outcome of DAG analysis.
type dagResult struct {
	hasCycle          bool
	cyclePath         []string
	topologicalOrder  []string
	orphanSubProblems []string
}

// extractDagDependencies parses a []any of {from, to} objects into []dagEdge.
// Returns nil if the slice is empty or any element is not a valid {from, to} object.
func extractDagDependencies(deps []any) []dagEdge {
	if len(deps) == 0 {
		return nil
	}
	edges := make([]dagEdge, 0, len(deps))
	for _, d := range deps {
		obj, ok := d.(map[string]any)
		if !ok {
			return nil
		}
		from, fromOK := obj["from"].(string)
		to, toOK := obj["to"].(string)
		if !fromOK || !toOK {
			return nil
		}
		edges = append(edges, dagEdge{from: from, to: to})
	}
	return edges
}

// analyzeDag performs DFS cycle detection, Kahn's topological sort, and orphan
// sub-problem detection on the provided directed graph.
func analyzeDag(edges []dagEdge, subProblems []any) dagResult {
	// Collect all node names from edges.
	nodeSet := map[string]struct{}{}
	for _, e := range edges {
		nodeSet[e.from] = struct{}{}
		nodeSet[e.to] = struct{}{}
	}

	// Build adjacency list.
	adj := map[string][]string{}
	for n := range nodeSet {
		adj[n] = nil
	}
	for _, e := range edges {
		adj[e.from] = append(adj[e.from], e.to)
	}

	// DFS cycle detection using white(0)/gray(1)/black(2) coloring.
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	for n := range nodeSet {
		color[n] = white
	}

	var cyclePath []string
	var dfs func(node string, path []string) bool
	dfs = func(node string, path []string) bool {
		color[node] = gray
		for _, neighbor := range adj[node] {
			if color[neighbor] == gray {
				// Found a back edge — record cycle path from the repeated node.
				idx := -1
				for i, p := range path {
					if p == neighbor {
						idx = i
						break
					}
				}
				cycle := make([]string, len(path)-idx+1)
				copy(cycle, path[idx:])
				cycle[len(cycle)-1] = neighbor
				cyclePath = cycle
				return true
			}
			if color[neighbor] == white {
				if dfs(neighbor, append(path, neighbor)) {
					return true
				}
			}
		}
		color[node] = black
		return false
	}

	hasCycle := false
	// Iterate in stable order so results are deterministic.
	nodes := sortedKeys(nodeSet)
	for _, n := range nodes {
		if color[n] == white {
			if dfs(n, []string{n}) {
				hasCycle = true
				break
			}
		}
	}

	// Kahn's topological sort — only when there is no cycle.
	var topologicalOrder []string
	if !hasCycle {
		inDegree := map[string]int{}
		for n := range nodeSet {
			inDegree[n] = 0
		}
		for _, e := range edges {
			inDegree[e.to]++
		}
		// Seed queue with zero-in-degree nodes in stable order.
		queue := []string{}
		for _, n := range nodes {
			if inDegree[n] == 0 {
				queue = append(queue, n)
			}
		}
		order := make([]string, 0, len(nodeSet))
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			order = append(order, n)
			neighbors := adj[n]
			sort.Strings(neighbors) // stable order within adjacency list
			for _, neighbor := range neighbors {
				inDegree[neighbor]--
				if inDegree[neighbor] == 0 {
					queue = append(queue, neighbor)
				}
			}
		}
		topologicalOrder = order
	}

	// Orphan sub-problems: named in subProblems but not referenced in any edge.
	subProblemNames := extractSubProblemNames(subProblems)
	orphans := []string{}
	for _, name := range subProblemNames {
		if _, inGraph := nodeSet[name]; !inGraph {
			orphans = append(orphans, name)
		}
	}

	return dagResult{
		hasCycle:          hasCycle,
		cyclePath:         cyclePath,
		topologicalOrder:  topologicalOrder,
		orphanSubProblems: orphans,
	}
}

// extractSubProblemNames resolves each sub-problem entry to a string name.
// Accepts plain strings or objects with an "id" or "name" key.
func extractSubProblemNames(subProblems []any) []string {
	names := make([]string, 0, len(subProblems))
	for _, s := range subProblems {
		switch v := s.(type) {
		case string:
			names = append(names, v)
		case map[string]any:
			if id, ok := v["id"].(string); ok {
				names = append(names, id)
			} else if name, ok := v["name"].(string); ok {
				names = append(names, name)
			}
		}
	}
	return names
}

// sortedKeys returns the keys of a map[string]struct{} in sorted order.
func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
