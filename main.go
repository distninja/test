package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/graph"
	_ "github.com/cayleygraph/cayley/graph/kv/bolt"
	"github.com/cayleygraph/cayley/schema"
	"github.com/cayleygraph/quad"
)

// NinjaRule represents a build rule in Ninja
type NinjaRule struct {
	ID          quad.IRI          `json:"@id" quad:"@id"`
	Type        quad.IRI          `json:"@type" quad:"@type"`
	Name        string            `json:"name" quad:"name"`
	Command     string            `json:"command" quad:"command"`
	Description string            `json:"description,omitempty" quad:"description"`
	Generator   bool              `json:"generator,omitempty" quad:"generator"`
	Restat      bool              `json:"restat,omitempty" quad:"restat"`
	Variables   map[string]string `json:"variables,omitempty" quad:"variables"`
	CreatedAt   time.Time         `json:"created_at" quad:"created_at"`
}

// NinjaBuild represents a build statement
type NinjaBuild struct {
	ID        quad.IRI          `json:"@id" quad:"@id"`
	Type      quad.IRI          `json:"@type" quad:"@type"`
	BuildID   string            `json:"build_id" quad:"build_id"`
	Rule      quad.IRI          `json:"rule" quad:"rule"`
	Variables map[string]string `json:"variables,omitempty" quad:"variables"`
	Pool      string            `json:"pool,omitempty" quad:"pool"`
	CreatedAt time.Time         `json:"created_at" quad:"created_at"`
}

// NinjaTarget represents a build target
type NinjaTarget struct {
	ID           quad.IRI  `json:"@id" quad:"@id"`
	Type         quad.IRI  `json:"@type" quad:"@type"`
	Path         string    `json:"path" quad:"path"`
	Status       string    `json:"status" quad:"status"`
	Hash         string    `json:"hash,omitempty" quad:"hash"`
	LastModified time.Time `json:"last_modified" quad:"last_modified"`
	Build        quad.IRI  `json:"build" quad:"build"`
}

// NinjaFile represents source files and dependencies
type NinjaFile struct {
	ID           quad.IRI  `json:"@id" quad:"@id"`
	Type         quad.IRI  `json:"@type" quad:"@type"`
	Path         string    `json:"path" quad:"path"`
	FileType     string    `json:"file_type" quad:"file_type"` // "source", "header", "object", etc.
	LastModified time.Time `json:"last_modified" quad:"last_modified"`
	Size         int64     `json:"size" quad:"size"`
}

// NinjaCayleyStore implements Ninja build graph using Cayley
type NinjaCayleyStore struct {
	store  *cayley.Handle
	schema *schema.Config
	ctx    context.Context
}

// Quad predicates for relationships
const (
	PredicateRuleUsedBy     = "rule_used_by"
	PredicateHasInput       = "has_input"
	PredicateHasOutput      = "has_output"
	PredicateHasImplicitDep = "has_implicit_dep"
	PredicateHasOrderDep    = "has_order_dep"
	PredicateDependsOn      = "depends_on"
	PredicateProduces       = "produces"
	PredicateIncluded       = "included"
	PredicateLinkedWith     = "linked_with"
)

// NewNinjaCayleyStore creates a new Cayley-based Ninja graph store
func NewNinjaCayleyStore(dbPath string) (*NinjaCayleyStore, error) {
	// Initialize Cayley with BoltDB backend
	store, err := cayley.NewGraph("bolt", dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cayley store: %w", err)
	}

	// Register types
	schema.RegisterType("NinjaRule", NinjaRule{})
	schema.RegisterType("NinjaBuild", NinjaBuild{})
	schema.RegisterType("NinjaTarget", NinjaTarget{})
	schema.RegisterType("NinjaFile", NinjaFile{})

	// Configure schema
	schemaConfig := schema.NewConfig()

	ctx := context.Background()

	return &NinjaCayleyStore{
		store:  store,
		schema: schemaConfig,
		ctx:    ctx,
	}, nil
}

// Close closes the Cayley store
func (ncs *NinjaCayleyStore) Close() error {
	return ncs.store.Close()
}

// AddRule adds a build rule to the graph
func (ncs *NinjaCayleyStore) AddRule(rule *NinjaRule) error {
	rule.ID = quad.IRI(fmt.Sprintf("rule:%s", rule.Name))
	rule.Type = "NinjaRule"
	rule.CreatedAt = time.Now()

	qw := graph.NewWriter(ncs.store)

	defer func(qw graph.BatchWriter) {
		_ = qw.Close()
	}(qw)

	_, err := ncs.schema.WriteAsQuads(qw, rule)
	if err != nil {
		return fmt.Errorf("failed to write rule: %w", err)
	}

	return nil
}

// GetRule retrieves a rule by name
func (ncs *NinjaCayleyStore) GetRule(name string) (*NinjaRule, error) {
	var rule NinjaRule
	err := ncs.schema.LoadTo(ncs.ctx, ncs.store, &rule, quad.IRI(fmt.Sprintf("rule:%s", name)))
	if err != nil {
		return nil, fmt.Errorf("failed to load rule %s: %w", name, err)
	}

	return &rule, nil
}

// AddBuild adds a build statement to the graph
func (ncs *NinjaCayleyStore) AddBuild(build *NinjaBuild, inputs, outputs, implicitDeps, orderDeps []string) error {
	tx := graph.NewTransaction()

	// Set build metadata
	build.ID = quad.IRI(fmt.Sprintf("build:%s", build.BuildID))
	build.Type = "NinjaBuild"
	build.CreatedAt = time.Now()

	// Write build object
	buildQuads, err := ncs.schema.WriteAsQuads(nil, build)
	if err != nil {
		return fmt.Errorf("failed to serialize build: %w", err)
	}

	for _, q := range buildQuads {
		tx.AddQuad(q)
	}

	// Create output targets
	for _, output := range outputs {
		target := &NinjaTarget{
			ID:           quad.IRI(fmt.Sprintf("target:%s", output)),
			Type:         quad.IRI("NinjaTarget"),
			Path:         output,
			Status:       "clean",
			LastModified: time.Now(),
			Build:        build.ID,
		}

		targetQuads, err := ncs.schema.WriteAsQuads(nil, target)
		if err != nil {
			return fmt.Errorf("failed to serialize target: %w", err)
		}

		for _, q := range targetQuads {
			tx.AddQuad(q)
		}

		// Link build to output
		tx.AddQuad(quad.Make(build.ID, PredicateHasOutput, quad.IRI(fmt.Sprintf("target:%s", output)), nil))
	}

	// Create input file nodes and relationships
	for _, input := range inputs {
		inputFile := &NinjaFile{
			ID:       quad.IRI(fmt.Sprintf("file:%s", input)),
			Type:     quad.IRI("NinjaFile"),
			Path:     input,
			FileType: ncs.inferFileType(input),
		}

		fileQuads, err := ncs.schema.WriteAsQuads(nil, inputFile)
		if err != nil {
			return fmt.Errorf("failed to serialize input file: %w", err)
		}

		for _, q := range fileQuads {
			tx.AddQuad(q)
		}

		// Link build to input
		tx.AddQuad(quad.Make(build.ID, PredicateHasInput, quad.IRI(fmt.Sprintf("file:%s", input)), nil))

		// Create dependencies from outputs to inputs
		for _, output := range outputs {
			tx.AddQuad(quad.Make(
				quad.IRI(fmt.Sprintf("target:%s", output)),
				PredicateDependsOn,
				quad.IRI(fmt.Sprintf("file:%s", input)),
				nil,
			))
		}
	}

	// Handle implicit dependencies
	for _, implicitDep := range implicitDeps {
		depFile := &NinjaFile{
			ID:       quad.IRI(fmt.Sprintf("file:%s", implicitDep)),
			Type:     quad.IRI("NinjaFile"),
			Path:     implicitDep,
			FileType: ncs.inferFileType(implicitDep),
		}

		fileQuads, err := ncs.schema.WriteAsQuads(nil, depFile)
		if err != nil {
			return fmt.Errorf("failed to serialize implicit dep: %w", err)
		}

		for _, q := range fileQuads {
			tx.AddQuad(q)
		}

		tx.AddQuad(quad.Make(build.ID, PredicateHasImplicitDep, quad.IRI(fmt.Sprintf("file:%s", implicitDep)), nil))

		for _, output := range outputs {
			tx.AddQuad(quad.Make(
				quad.IRI(fmt.Sprintf("target:%s", output)),
				PredicateDependsOn,
				quad.IRI(fmt.Sprintf("file:%s", implicitDep)),
				nil,
			))
		}
	}

	// Handle order-only dependencies
	for _, orderDep := range orderDeps {
		tx.AddQuad(quad.Make(build.ID, PredicateHasOrderDep, quad.IRI(fmt.Sprintf("file:%s", orderDep)), nil))
	}

	// Apply transaction
	return ncs.store.ApplyTransaction(tx)
}

// inferFileType infers file type from extension
func (ncs *NinjaCayleyStore) inferFileType(path string) string {
	ext := strings.ToLower(path[strings.LastIndex(path, ".")+1:])
	switch ext {
	case "cpp", "cc", "cxx", "c":
		return "source"
	case "h", "hpp", "hxx":
		return "header"
	case "o", "obj":
		return "object"
	case "a", "lib":
		return "library"
	case "exe", "":
		return "executable"
	default:
		return "unknown"
	}
}

// GetTarget retrieves a target by path
func (ncs *NinjaCayleyStore) GetTarget(path string) (*NinjaTarget, error) {
	var target NinjaTarget
	err := ncs.schema.LoadTo(ncs.ctx, ncs.store, &target, quad.IRI(fmt.Sprintf("target:%s", path)))
	if err != nil {
		return nil, fmt.Errorf("failed to load target %s: %w", path, err)
	}

	return &target, nil
}

// GetBuildDependencies returns all dependencies of a target
func (ncs *NinjaCayleyStore) GetBuildDependencies(targetPath string) ([]*NinjaFile, error) {
	// Query for all files that the target depends on
	p := cayley.StartPath(ncs.store, quad.IRI(fmt.Sprintf("target:%s", targetPath))).
		Out(quad.IRI(PredicateDependsOn))

	var dependencies []*NinjaFile
	err := ncs.schema.LoadPathTo(ncs.ctx, ncs.store, &dependencies, p)
	if err != nil {
		return nil, fmt.Errorf("failed to get dependencies for %s: %w", targetPath, err)
	}

	return dependencies, nil
}

// GetReverseDependencies returns all targets that depend on a file
func (ncs *NinjaCayleyStore) GetReverseDependencies(filePath string) ([]*NinjaTarget, error) {
	// Query for all targets that depend on this file
	p := cayley.StartPath(ncs.store, quad.IRI(fmt.Sprintf("file:%s", filePath))).
		In(quad.IRI(PredicateDependsOn))

	var dependents []*NinjaTarget
	err := ncs.schema.LoadPathTo(ncs.ctx, ncs.store, &dependents, p)
	if err != nil {
		return nil, fmt.Errorf("failed to get reverse dependencies for %s: %w", filePath, err)
	}

	return dependents, nil
}

// GetBuildOrder returns targets in topological order
func (ncs *NinjaCayleyStore) GetBuildOrder() ([]string, error) {
	// Get all targets
	targets, err := ncs.GetAllTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to get all targets: %w", err)
	}

	// Build dependency graph
	depGraph := make(map[string][]string)
	inDegree := make(map[string]int)

	for _, target := range targets {
		depGraph[target.Path] = []string{}
		inDegree[target.Path] = 0
	}

	// Populate dependencies
	for _, target := range targets {
		deps, err := ncs.GetBuildDependencies(target.Path)
		if err != nil {
			continue
		}

		for _, dep := range deps {
			// Check if dependency is also a target
			if _, exists := inDegree[dep.Path]; exists {
				depGraph[dep.Path] = append(depGraph[dep.Path], target.Path)
				inDegree[target.Path]++
			}
		}
	}

	// Topological sort using Kahn's algorithm
	var queue []string
	for target, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, target)
		}
	}

	var result []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		for _, neighbor := range depGraph[current] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	if len(result) != len(targets) {
		return nil, fmt.Errorf("circular dependency detected")
	}

	return result, nil
}

// GetAllTargets returns all targets in the graph
func (ncs *NinjaCayleyStore) GetAllTargets() ([]*NinjaTarget, error) {
	p := cayley.StartPath(ncs.store).Has(quad.IRI("@type"), quad.IRI("NinjaTarget"))

	var targets []*NinjaTarget
	err := ncs.schema.LoadPathTo(ncs.ctx, ncs.store, &targets, p)
	if err != nil {
		return nil, fmt.Errorf("failed to get all targets: %w", err)
	}

	return targets, nil
}

// GetTargetsByRule returns all targets built by a specific rule
func (ncs *NinjaCayleyStore) GetTargetsByRule(ruleName string) ([]*NinjaTarget, error) {
	// Find builds using this rule, then find their outputs
	ruleIRI := quad.IRI(fmt.Sprintf("rule:%s", ruleName))

	p := cayley.StartPath(ncs.store).Has(quad.IRI("rule"), ruleIRI).
		Out(quad.IRI(PredicateHasOutput))

	var targets []*NinjaTarget
	err := ncs.schema.LoadPathTo(ncs.ctx, ncs.store, &targets, p)
	if err != nil {
		return nil, fmt.Errorf("failed to get targets by rule %s: %w", ruleName, err)
	}

	return targets, nil
}

// UpdateTargetStatus updates the status of a target
func (ncs *NinjaCayleyStore) UpdateTargetStatus(targetPath, status string) error {
	tx := graph.NewTransaction()

	targetIRI := quad.IRI(fmt.Sprintf("target:%s", targetPath))

	// Remove old status
	oldStatusQuads := ncs.store.QuadsAllIterator().Iterate(ncs.ctx).
		Limit(100) // Reasonable limit for status updates

	for oldStatusQuads.Next(ncs.ctx) {
		q := oldStatusQuads.Result()
		if q.Subject == targetIRI && q.Predicate == quad.IRI("status") {
			tx.RemoveQuad(q)
		}
	}
	oldStatusQuads.Close()

	// Add new status
	tx.AddQuad(quad.Make(targetIRI, quad.IRI("status"), quad.String(status), nil))
	tx.AddQuad(quad.Make(targetIRI, quad.IRI("last_modified"), quad.Time(time.Now()), nil))

	return ncs.store.ApplyTransaction(tx)
}

// FindCycles detects circular dependencies in the build graph
func (ncs *NinjaCayleyStore) FindCycles() ([][]string, error) {
	targets, err := ncs.GetAllTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to get targets: %w", err)
	}

	visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited
	var cycles [][]string
	var currentPath []string

	var dfs func(string) error
	dfs = func(target string) error {
		if visited[target] == 1 {
			// Found cycle
			cycleStart := -1
			for i, path := range currentPath {
				if path == target {
					cycleStart = i
					break
				}
			}
			if cycleStart >= 0 {
				cycle := make([]string, len(currentPath[cycleStart:]))
				copy(cycle, currentPath[cycleStart:])
				cycles = append(cycles, cycle)
			}
			return nil
		}

		if visited[target] == 2 {
			return nil
		}

		visited[target] = 1
		currentPath = append(currentPath, target)

		deps, err := ncs.GetBuildDependencies(target)
		if err != nil {
			return err
		}

		for _, dep := range deps {
			// Only follow dependencies that are also targets
			if _, exists := visited[dep.Path]; exists {
				err = dfs(dep.Path)
				if err != nil {
					return err
				}
			}
		}

		visited[target] = 2
		currentPath = currentPath[:len(currentPath)-1]

		return nil
	}

	for _, target := range targets {
		if visited[target.Path] == 0 {
			err = dfs(target.Path)
			if err != nil {
				return nil, err
			}
		}
	}

	return cycles, nil
}

// GetBuildStats returns statistics about the build graph
func (ncs *NinjaCayleyStore) GetBuildStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Count rules
	rulesPath := cayley.StartPath(ncs.store).Has(quad.IRI("@type"), quad.IRI("NinjaRule"))
	ruleCount, err := rulesPath.Count().Limit(1000).All().Next(ncs.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count rules: %w", err)
	}
	stats["total_rules"] = ruleCount

	// Count builds
	buildsPath := cayley.StartPath(ncs.store).Has(quad.IRI("@type"), quad.IRI("NinjaBuild"))
	buildCount, err := buildsPath.Count().Limit(1000).All().Next(ncs.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count builds: %w", err)
	}
	stats["total_builds"] = buildCount

	// Count targets
	targetsPath := cayley.StartPath(ncs.store).Has(quad.IRI("@type"), quad.IRI("NinjaTarget"))
	targetCount, err := targetsPath.Count().Limit(1000).All().Next(ncs.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count targets: %w", err)
	}
	stats["total_targets"] = targetCount

	// Count files
	filesPath := cayley.StartPath(ncs.store).Has(quad.IRI("@type"), quad.IRI("NinjaFile"))
	fileCount, err := filesPath.Count().Limit(1000).All().Next(ncs.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count files: %w", err)
	}
	stats["total_files"] = fileCount

	return stats, nil
}

// Example usage
func main() {
	// Create Ninja Cayley store
	ncs, err := NewNinjaCayleyStore("ninja_cayley.db")
	if err != nil {
		log.Fatal("Failed to create Ninja Cayley store:", err)
	}

	defer func(ncs *NinjaCayleyStore) {
		_ = ncs.Close()
	}(ncs)

	// Add build rules
	cxxRule := &NinjaRule{
		Name:        "cxx",
		Command:     "g++ -MMD -MF $out.d -c $in -o $out $cflags",
		Description: "Compiling C++ object $out",
		Variables: map[string]string{
			"cflags": "-Wall -g -std=c++17",
		},
	}

	linkRule := &NinjaRule{
		Name:        "link",
		Command:     "g++ $in -o $out $ldflags",
		Description: "Linking executable $out",
		Variables: map[string]string{
			"ldflags": "-pthread -lm",
		},
	}

	err = ncs.AddRule(cxxRule)
	if err != nil {
		log.Fatal("Failed to add cxx rule:", err)
	}

	err = ncs.AddRule(linkRule)
	if err != nil {
		log.Fatal("Failed to add link rule:", err)
	}

	// Add build statements
	mainBuild := &NinjaBuild{
		BuildID: "main_obj",
		Rule:    quad.IRI("rule:cxx"),
	}

	err = ncs.AddBuild(
		mainBuild,
		[]string{"src/main.cpp"},               // inputs
		[]string{"build/main.o"},               // outputs
		[]string{"src/main.h", "src/common.h"}, // implicit deps
		[]string{},                             // order deps
	)
	if err != nil {
		log.Fatal("Failed to add main build:", err)
	}

	utilBuild := &NinjaBuild{
		BuildID: "util_obj",
		Rule:    quad.IRI("rule:cxx"),
	}

	err = ncs.AddBuild(
		utilBuild,
		[]string{"src/util.cpp"},
		[]string{"build/util.o"},
		[]string{"src/util.h", "src/common.h"},
		[]string{},
	)
	if err != nil {
		log.Fatal("Failed to add util build:", err)
	}

	appBuild := &NinjaBuild{
		BuildID: "app_exe",
		Rule:    quad.IRI("rule:link"),
	}

	err = ncs.AddBuild(
		appBuild,
		[]string{"build/main.o", "build/util.o"},
		[]string{"build/app"},
		[]string{},
		[]string{},
	)
	if err != nil {
		log.Fatal("Failed to add app build:", err)
	}

	// Query the build graph
	fmt.Println("=== Ninja Build Graph with Cayley ===")

	// Get build statistics
	stats, err := ncs.GetBuildStats()
	if err != nil {
		log.Fatal("Failed to get build stats:", err)
	}

	fmt.Println("Build Statistics:")
	for key, value := range stats {
		fmt.Printf("  %s: %v\n", key, value)
	}

	// Get build order
	buildOrder, err := ncs.GetBuildOrder()
	if err != nil {
		log.Fatal("Failed to get build order:", err)
	}

	fmt.Println("\nBuild Order:")
	for i, target := range buildOrder {
		fmt.Printf("  %d. %s\n", i+1, target)
	}

	// Get dependencies
	deps, err := ncs.GetBuildDependencies("build/app")
	if err != nil {
		log.Fatal("Failed to get dependencies:", err)
	}

	fmt.Println("\nDependencies for 'build/app':")
	for _, dep := range deps {
		fmt.Printf("  - %s (%s)\n", dep.Path, dep.FileType)
	}

	// Get reverse dependencies
	reverseDeps, err := ncs.GetReverseDependencies("src/common.h")
	if err != nil {
		log.Fatal("Failed to get reverse dependencies:", err)
	}

	fmt.Println("\nTargets depending on 'src/common.h':")
	for _, target := range reverseDeps {
		fmt.Printf("  - %s\n", target.Path)
	}

	// Check for cycles
	cycles, err := ncs.FindCycles()
	if err != nil {
		log.Fatal("Failed to find cycles:", err)
	}

	if len(cycles) > 0 {
		fmt.Println("\nCircular dependencies found:")
		for i, cycle := range cycles {
			fmt.Printf("  Cycle %d: %s\n", i+1, strings.Join(cycle, " -> "))
		}
	} else {
		fmt.Println("\nNo circular dependencies found.")
	}

	// Update target status
	err = ncs.UpdateTargetStatus("build/main.o", "building")
	if err != nil {
		log.Fatal("Failed to update target status:", err)
	}

	target, err := ncs.GetTarget("build/main.o")
	if err != nil {
		log.Fatal("Failed to get target:", err)
	}

	fmt.Printf("\nTarget 'build/main.o' status: %s\n", target.Status)

	// Get targets by rule
	cxxTargets, err := ncs.GetTargetsByRule("cxx")
	if err != nil {
		log.Fatal("Failed to get targets by rule:", err)
	}

	fmt.Println("\nTargets built with 'cxx' rule:")
	for _, target := range cxxTargets {
		fmt.Printf("  - %s (status: %s)\n", target.Path, target.Status)
	}
}
