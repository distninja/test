package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/quad"
	"github.com/gorilla/mux"
)

// APIServer wraps the NinjaCayleyStore with REST endpoints
type APIServer struct {
	store *NinjaCayleyStore
	port  string
}

// APIResponse represents a standard API response
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// CreateRuleRequest represents the request body for creating a rule
type CreateRuleRequest struct {
	Name        string            `json:"name"`
	Command     string            `json:"command"`
	Description string            `json:"description,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
}

// UpdateRuleRequest represents the request body for updating a rule
type UpdateRuleRequest struct {
	Command     string            `json:"command,omitempty"`
	Description string            `json:"description,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
}

// NewAPIServer creates a new API server
func NewAPIServer(dbPath string, port string) (*APIServer, error) {
	store, err := NewNinjaCayleyStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	return &APIServer{
		store: store,
		port:  port,
	}, nil
}

// Start starts the API server
func (api *APIServer) Start() error {
	router := mux.NewRouter()

	// Set up routes
	api.setupRoutes(router)

	// Add middleware
	router.Use(api.loggingMiddleware)
	router.Use(api.corsMiddleware)

	log.Printf("Starting API server on port %s", api.port)
	return http.ListenAndServe(":"+api.port, router)
}

// setupRoutes configures all API routes
func (api *APIServer) setupRoutes(router *mux.Router) {
	// Rule endpoints
	router.HandleFunc("/api/rules", api.createRule).Methods("POST")
	router.HandleFunc("/api/rules", api.listRules).Methods("GET")
	router.HandleFunc("/api/rules/{name}", api.getRule).Methods("GET")
	router.HandleFunc("/api/rules/{name}", api.updateRule).Methods("PUT")
	router.HandleFunc("/api/rules/{name}", api.deleteRule).Methods("DELETE")

	// Health check
	router.HandleFunc("/health", api.healthCheck).Methods("GET")

	// Stats endpoint
	router.HandleFunc("/api/stats", api.getStats).Methods("GET")
}

// createRule creates a new Ninja rule
func (api *APIServer) createRule(w http.ResponseWriter, r *http.Request) {
	var req CreateRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.sendError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	// Validate required fields
	if req.Name == "" {
		api.sendError(w, http.StatusBadRequest, "Rule name is required")
		return
	}
	if req.Command == "" {
		api.sendError(w, http.StatusBadRequest, "Rule command is required")
		return
	}

	// Check if rule already exists
	if _, err := api.store.GetRule(req.Name); err == nil {
		api.sendError(w, http.StatusConflict, "Rule with name '"+req.Name+"' already exists")
		return
	}

	// Create the rule
	rule := &NinjaRule{
		Name:        req.Name,
		Command:     req.Command,
		Description: req.Description,
	}

	// Set variables if provided
	if req.Variables != nil {
		if err := rule.SetVariables(req.Variables); err != nil {
			api.sendError(w, http.StatusBadRequest, "Invalid variables: "+err.Error())
			return
		}
	}

	// Add rule to store
	id, err := api.store.AddRule(rule)
	if err != nil {
		api.sendError(w, http.StatusInternalServerError, "Failed to create rule: "+err.Error())
		return
	}

	// Get the created rule for response
	createdRule, err := api.store.GetRule(req.Name)
	if err != nil {
		api.sendError(w, http.StatusInternalServerError, "Failed to retrieve created rule: "+err.Error())
		return
	}

	response := struct {
		ID   string     `json:"id"`
		Rule *NinjaRule `json:"rule"`
	}{
		ID:   id.String(),
		Rule: createdRule,
	}

	api.sendSuccess(w, http.StatusCreated, "Rule created successfully", response)
}

// getRule retrieves a specific rule by name
func (api *APIServer) getRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if name == "" {
		api.sendError(w, http.StatusBadRequest, "Rule name is required")
		return
	}

	rule, err := api.store.GetRule(name)
	if err != nil {
		api.sendError(w, http.StatusNotFound, "Rule not found: "+err.Error())
		return
	}

	// Parse variables for response
	variables, err := rule.GetVariables()
	if err != nil {
		log.Printf("Warning: Failed to parse variables for rule %s: %v", name, err)
		variables = make(map[string]string)
	}

	response := struct {
		*NinjaRule
		ParsedVariables map[string]string `json:"parsed_variables"`
	}{
		NinjaRule:       rule,
		ParsedVariables: variables,
	}

	api.sendSuccess(w, http.StatusOK, "", response)
}

// listRules retrieves all rules
func (api *APIServer) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := api.getAllRules()
	if err != nil {
		api.sendError(w, http.StatusInternalServerError, "Failed to retrieve rules: "+err.Error())
		return
	}

	response := struct {
		Rules []map[string]interface{} `json:"rules"`
		Count int                      `json:"count"`
	}{
		Rules: rules,
		Count: len(rules),
	}

	api.sendSuccess(w, http.StatusOK, "", response)
}

// updateRule updates an existing rule
func (api *APIServer) updateRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if name == "" {
		api.sendError(w, http.StatusBadRequest, "Rule name is required")
		return
	}

	var req UpdateRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.sendError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	// Get existing rule
	existingRule, err := api.store.GetRule(name)
	if err != nil {
		api.sendError(w, http.StatusNotFound, "Rule not found: "+err.Error())
		return
	}

	// Update fields if provided
	updatedRule := &NinjaRule{
		Name:        existingRule.Name,
		Command:     existingRule.Command,
		Description: existingRule.Description,
		Variables:   existingRule.Variables,
	}

	if req.Command != "" {
		updatedRule.Command = req.Command
	}
	if req.Description != "" {
		updatedRule.Description = req.Description
	}
	if req.Variables != nil {
		if err := updatedRule.SetVariables(req.Variables); err != nil {
			api.sendError(w, http.StatusBadRequest, "Invalid variables: "+err.Error())
			return
		}
	}

	// Delete old rule and add updated one
	if err := api.deleteRuleFromStore(name); err != nil {
		api.sendError(w, http.StatusInternalServerError, "Failed to delete old rule: "+err.Error())
		return
	}

	_, err = api.store.AddRule(updatedRule)
	if err != nil {
		api.sendError(w, http.StatusInternalServerError, "Failed to update rule: "+err.Error())
		return
	}

	// Get the updated rule for response
	updated, err := api.store.GetRule(name)
	if err != nil {
		api.sendError(w, http.StatusInternalServerError, "Failed to retrieve updated rule: "+err.Error())
		return
	}

	api.sendSuccess(w, http.StatusOK, "Rule updated successfully", updated)
}

// deleteRule deletes a rule by name
func (api *APIServer) deleteRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if name == "" {
		api.sendError(w, http.StatusBadRequest, "Rule name is required")
		return
	}

	// Check if rule exists
	_, err := api.store.GetRule(name)
	if err != nil {
		api.sendError(w, http.StatusNotFound, "Rule not found: "+err.Error())
		return
	}

	// Delete the rule
	if err := api.deleteRuleFromStore(name); err != nil {
		api.sendError(w, http.StatusInternalServerError, "Failed to delete rule: "+err.Error())
		return
	}

	api.sendSuccess(w, http.StatusOK, "Rule deleted successfully", nil)
}

// healthCheck provides a health check endpoint
func (api *APIServer) healthCheck(w http.ResponseWriter, r *http.Request) {
	status := struct {
		Status    string `json:"status"`
		Service   string `json:"service"`
		Timestamp string `json:"timestamp"`
	}{
		Status:    "healthy",
		Service:   "ninja-rules-api",
		Timestamp: strconv.FormatInt(time.Now().Unix(), 10),
	}

	api.sendSuccess(w, http.StatusOK, "", status)
}

// getStats provides statistics about the rule store
func (api *APIServer) getStats(w http.ResponseWriter, r *http.Request) {
	stats, err := api.store.GetBuildStats()
	if err != nil {
		api.sendError(w, http.StatusInternalServerError, "Failed to get stats: "+err.Error())
		return
	}

	api.sendSuccess(w, http.StatusOK, "", stats)
}

// getAllRules retrieves all rules from the store
func (api *APIServer) getAllRules() ([]map[string]interface{}, error) {
	var rules []map[string]interface{}

	// Iterate through all quads to find rules
	it := api.store.store.QuadsAllIterator()

	defer func(it graph.Iterator) {
		_ = it.Close()
	}(it)

	ruleIRIs := make(map[quad.Value]bool)

	for it.Next(api.store.ctx) {
		q := api.store.store.Quad(it.Result())

		// Look for type declarations of NinjaRule
		if q.Predicate.String() == `<rdf:type>` && q.Object.String() == `<NinjaRule>` {
			ruleIRIs[q.Subject] = true
		}
	}

	if err := it.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate quads: %w", err)
	}

	// Load each rule
	for ruleIRI := range ruleIRIs {
		var rule NinjaRule
		err := api.store.schema.LoadTo(api.store.ctx, api.store.store, &rule, ruleIRI)
		if err != nil {
			continue // Skip rules we can't load
		}

		// Parse variables
		variables, err := rule.GetVariables()
		if err != nil {
			variables = make(map[string]string)
		}

		ruleData := map[string]interface{}{
			"id":               rule.ID.String(),
			"type":             rule.Type.String(),
			"name":             rule.Name,
			"command":          rule.Command,
			"description":      rule.Description,
			"variables_raw":    rule.Variables,
			"parsed_variables": variables,
		}

		rules = append(rules, ruleData)
	}

	return rules, nil
}

// deleteRuleFromStore removes a rule from the store
func (api *APIServer) deleteRuleFromStore(name string) error {
	ruleIRI := quad.IRI(fmt.Sprintf("rule:%s", name))

	// Create transaction to remove all quads related to this rule
	tx := graph.NewTransaction()

	it := api.store.store.QuadsAllIterator()

	defer func(it graph.Iterator) {
		_ = it.Close()
	}(it)

	for it.Next(api.store.ctx) {
		q := api.store.store.Quad(it.Result())
		if q.Subject == ruleIRI {
			tx.RemoveQuad(q)
		}
	}

	if err := it.Err(); err != nil {
		return fmt.Errorf("failed to iterate quads: %w", err)
	}

	return api.store.store.ApplyTransaction(tx)
}

// sendSuccess sends a successful API response
func (api *APIServer) sendSuccess(w http.ResponseWriter, statusCode int, message string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	}

	_ = json.NewEncoder(w).Encode(response)
}

// sendError sends an error API response
func (api *APIServer) sendError(w http.ResponseWriter, statusCode int, error string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := APIResponse{
		Success: false,
		Error:   error,
	}

	_ = json.NewEncoder(w).Encode(response)
}

// loggingMiddleware logs HTTP requests
func (api *APIServer) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.Method, r.RequestURI, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware adds CORS headers
func (api *APIServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Close closes the API server resources
func (api *APIServer) Close() error {
	return api.store.Close()
}

// nolint:unused
// Example usage for the API server
func runAPIServer() {
	dbPath := "./ninja_db/cayley.db"

	// Create API server
	apiServer, err := NewAPIServer(dbPath, "8080")
	if err != nil {
		log.Fatal("Failed to create API server:", err)
	}

	defer func(apiServer *APIServer) {
		_ = apiServer.Close()
	}(apiServer)

	// Start the server
	log.Fatal(apiServer.Start())
}
