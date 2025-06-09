package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	dbPath string
}

// Quad predicates for relationships
const (
	PredicateHasInput       = "has_input"
	PredicateHasOutput      = "has_output"
	PredicateHasImplicitDep = "has_implicit_dep"
	PredicateHasOrderDep    = "has_order_dep"
	PredicateDependsOn      = "depends_on"
)

// NewNinjaCayleyStore creates a new Cayley-based Ninja graph store
func NewNinjaCayleyStore(dbPath string) (*NinjaCayleyStore, error) {
	// Ensure the directory exists
	dbDir := filepath.Dir(dbPath)
	if dbDir == "." {
		// If no directory specified, create a subdirectory
		dbDir = filepath.Join(".", "ninja_db")
		dbPath = filepath.Join(dbDir, "cayley.db")
	}

	err := os.MkdirAll(dbDir, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create database directory %s: %w", dbDir, err)
	}

	// Check if database exists, if not initialize it
	var store *cayley.Handle
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// Initialize new database
		err = graph.InitQuadStore("bolt", dbPath, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Cayley store at %s: %w", dbPath, err)
		}
	}

	// Open the database
	store, err = cayley.NewGraph("bolt", dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open Cayley store at %s: %w", dbPath, err)
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
		dbPath: dbPath,
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
	qw := graph.NewWriter(ncs.store)
	defer func(qw graph.BatchWriter) {
		_ = qw.Close()
	}(qw)

	// Set build metadata
	build.ID = quad.IRI(fmt.Sprintf("build:%s", build.BuildID))
	build.Type = "NinjaBuild"
	build.CreatedAt = time.Now()

	// Write build object
	_, err := ncs.schema.WriteAsQuads(qw, build)
	if err != nil {
		return fmt.Errorf("failed to serialize build: %w", err)
	}

	var quads []quad.Quad

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

		_, err := ncs.schema.WriteAsQuads(qw, target)
		if err != nil {
			return fmt.Errorf("failed to serialize target: %w", err)
		}

		// Link build to output
		quads = append(quads, quad.Make(build.ID, PredicateHasOutput, quad.IRI(fmt.Sprintf("target:%s", output)), nil))
	}

	// Create input file nodes and relationships
	for _, input := range inputs {
		inputFile := &NinjaFile{
			ID:       quad.IRI(fmt.Sprintf("file:%s", input)),
			Type:     quad.IRI("NinjaFile"),
			Path:     input,
			FileType: ncs.inferFileType(input),
		}

		_, err := ncs.schema.WriteAsQuads(qw, inputFile)
		if err != nil {
			return fmt.Errorf("failed to serialize input file: %w", err)
		}

		// Link build to input
		quads = append(quads, quad.Make(build.ID, PredicateHasInput, quad.IRI(fmt.Sprintf("file:%s", input)), nil))

		// Create dependencies from outputs to inputs
		for _, output := range outputs {
			quads = append(quads, quad.Make(
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

		_, err := ncs.schema.WriteAsQuads(qw, depFile)
		if err != nil {
			return fmt.Errorf("failed to serialize implicit dep: %w", err)
		}

		quads = append(quads, quad.Make(build.ID, PredicateHasImplicitDep, quad.IRI(fmt.Sprintf("file:%s", implicitDep)), nil))

		for _, output := range outputs {
			quads = append(quads, quad.Make(
				quad.IRI(fmt.Sprintf("target:%s", output)),
				PredicateDependsOn,
				quad.IRI(fmt.Sprintf("file:%s", implicitDep)),
				nil,
			))
		}
	}

	// Handle order-only dependencies
	for _, orderDep := range orderDeps {
		quads = append(quads, quad.Make(build.ID, PredicateHasOrderDep, quad.IRI(fmt.Sprintf("file:%s", orderDep)), nil))
	}

	// Write all quads at once
	if len(quads) > 0 {
		_, err = qw.WriteQuads(quads)
		if err != nil {
			return fmt.Errorf("failed to write quads: %w", err)
		}
	}

	return nil
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

	// Remove old status - iterate through quads to find status ones
	it := ncs.store.QuadsAllIterator()

	defer func(it graph.Iterator) {
		_ = it.Close()
	}(it)

	for it.Next(ncs.ctx) {
		ref := it.Result()
		q := ncs.store.Quad(ref)
		if q.Subject == targetIRI && q.Predicate == quad.IRI("status") {
			tx.RemoveQuad(q)
		}
	}

	if err := it.Err(); err != nil {
		return fmt.Errorf("failed to iterate quads: %w", err)
	}

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
	rulesIt, _ := rulesPath.BuildIterator().Optimize()

	defer func(rulesIt graph.Iterator) {
		_ = rulesIt.Close()
	}(rulesIt)

	ruleCount := 0
	for rulesIt.Next(ncs.ctx) {
		ruleCount++
	}
	if err := rulesIt.Err(); err != nil {
		return nil, fmt.Errorf("failed to count rules: %w", err)
	}
	stats["total_rules"] = ruleCount

	// Count builds
	buildsPath := cayley.StartPath(ncs.store).Has(quad.IRI("@type"), quad.IRI("NinjaBuild"))
	buildsIt, _ := buildsPath.BuildIterator().Optimize()

	defer func(buildsIt graph.Iterator) {
		_ = buildsIt.Close()
	}(buildsIt)

	buildCount := 0
	for buildsIt.Next(ncs.ctx) {
		buildCount++
	}
	if err := buildsIt.Err(); err != nil {
		return nil, fmt.Errorf("failed to count builds: %w", err)
	}
	stats["total_builds"] = buildCount

	// Count targets
	targetsPath := cayley.StartPath(ncs.store).Has(quad.IRI("@type"), quad.IRI("NinjaTarget"))
	targetsIt, _ := targetsPath.BuildIterator().Optimize()

	defer func(targetsIt graph.Iterator) {
		_ = targetsIt.Close()
	}(targetsIt)

	targetCount := 0
	for targetsIt.Next(ncs.ctx) {
		targetCount++
	}
	if err := targetsIt.Err(); err != nil {
		return nil, fmt.Errorf("failed to count targets: %w", err)
	}
	stats["total_targets"] = targetCount

	// Count files
	filesPath := cayley.StartPath(ncs.store).Has(quad.IRI("@type"), quad.IRI("NinjaFile"))
	filesIt, _ := filesPath.BuildIterator().Optimize()

	defer func(filesIt graph.Iterator) {
		_ = filesIt.Close()
	}(filesIt)

	fileCount := 0
	for filesIt.Next(ncs.ctx) {
		fileCount++
	}
	if err := filesIt.Err(); err != nil {
		return nil, fmt.Errorf("failed to count files: %w", err)
	}
	stats["total_files"] = fileCount

	return stats, nil
}

func (ncs *NinjaCayleyStore) CleanupDatabase() error {
	if err := ncs.Close(); err != nil {
		return err
	}

	return os.RemoveAll(filepath.Dir(ncs.dbPath))
}

// Example usage
func main() {
	// Create Ninja Cayley store
	dbPath := "./ninja_db/cayley.db"
	ncs, err := NewNinjaCayleyStore(dbPath)
	if err != nil {
		fmt.Println("Failed to create Ninja Cayley store:", err.Error())
		os.Exit(1)
	}

	fmt.Printf("Database created at: %s\n", dbPath)

	defer func(ncs *NinjaCayleyStore) {
		_ = ncs.CleanupDatabase()
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
		fmt.Println("Failed to add cxx rule:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	err = ncs.AddRule(linkRule)
	if err != nil {
		fmt.Println("Failed to add link rule:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
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
		fmt.Println("Failed to add main build:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
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
		fmt.Println("Failed to add util build:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
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
		fmt.Println("Failed to add app build:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	// Query the build graph
	fmt.Println("\nNinja Build Graph with Cayley")

	// Get build statistics
	stats, err := ncs.GetBuildStats()
	if err != nil {
		fmt.Println("Failed to get build stats:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	fmt.Println("Build Statistics:")
	for key, value := range stats {
		fmt.Printf("  %s: %v\n", key, value)
	}

	// Get build order
	buildOrder, err := ncs.GetBuildOrder()
	if err != nil {
		fmt.Println("Failed to get build order:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	fmt.Println("\nBuild Order:")
	for i, target := range buildOrder {
		fmt.Printf("  %d. %s\n", i+1, target)
	}

	// Get dependencies
	deps, err := ncs.GetBuildDependencies("build/app")
	if err != nil {
		fmt.Println("Failed to get dependencies:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	fmt.Println("\nDependencies for 'build/app':")
	for _, dep := range deps {
		fmt.Printf("  - %s (%s)\n", dep.Path, dep.FileType)
	}

	// Get reverse dependencies
	reverseDeps, err := ncs.GetReverseDependencies("src/common.h")
	if err != nil {
		fmt.Println("Failed to get reverse dependencies:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	fmt.Println("\nTargets depending on 'src/common.h':")
	for _, target := range reverseDeps {
		fmt.Printf("  - %s\n", target.Path)
	}

	// Check for cycles
	cycles, err := ncs.FindCycles()
	if err != nil {
		fmt.Println("Failed to find cycles:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
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
		fmt.Println("Failed to update target status:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	target, err := ncs.GetTarget("build/main.o")
	if err != nil {
		fmt.Println("Failed to get target:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	fmt.Printf("\nTarget 'build/main.o' status: %s\n", target.Status)

	// Get targets by rule
	cxxTargets, err := ncs.GetTargetsByRule("cxx")
	if err != nil {
		fmt.Println("Failed to get targets by rule:", err.Error())
		_ = ncs.CleanupDatabase()
		os.Exit(1)
	}

	fmt.Println("\nTargets built with 'cxx' rule:")
	for _, target := range cxxTargets {
		fmt.Printf("  - %s (status: %s)\n", target.Path, target.Status)
	}

	fmt.Println("\nDatabase operations completed successfully!")
}
