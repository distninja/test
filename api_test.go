package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIClient provides methods to interact with the Ninja Rules API
type APIClient struct {
	baseURL string
	client  *http.Client
}

// NewAPIClient creates a new API client
func NewAPIClient(baseURL string) *APIClient {
	return &APIClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CreateRule creates a new rule
func (c *APIClient) CreateRule(name, command, description string, variables map[string]string) error {
	reqBody := CreateRuleRequest{
		Name:        name,
		Command:     command,
		Description: description,
		Variables:   variables,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	resp, err := c.client.Post(c.baseURL+"/api/rules", "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}

	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create rule: %s", string(body))
	}

	return nil
}

// GetRule retrieves a rule by name
func (c *APIClient) GetRule(name string) (*NinjaRule, error) {
	resp, err := c.client.Get(c.baseURL + "/api/rules/" + name)
	if err != nil {
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rule not found")
	}

	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	// Convert the data back to NinjaRule
	ruleData, ok := apiResp.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	rule := &NinjaRule{
		Name:        ruleData["name"].(string),
		Command:     ruleData["command"].(string),
		Description: ruleData["description"].(string),
		Variables:   ruleData["variables"].(string),
	}

	return rule, nil
}

// nolint:unused
// Example usage of the client
func testAPIClient() {
	client := NewAPIClient("http://localhost:8080")

	// Test creating a rule
	fmt.Println("Creating rule...")
	err := client.CreateRule(
		"test_cxx",
		"g++ -c $in -o $out $cflags",
		"Test C++ compilation rule",
		map[string]string{
			"cflags": "-Wall -O2",
		},
	)
	if err != nil {
		fmt.Printf("Error creating rule: %v\n", err)
		return
	}
	fmt.Println("Rule created successfully!")

	// Test getting the rule
	fmt.Println("Getting rule...")
	rule, err := client.GetRule("test_cxx")
	if err != nil {
		fmt.Printf("Error getting rule: %v\n", err)
		return
	}
	fmt.Printf("Retrieved rule: %+v\n", rule)
}
