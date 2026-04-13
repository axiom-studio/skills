package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

// Salesforce connection cache
var (
	connections = make(map[string]*SalesforceConnection)
	connMutex   sync.RWMutex
)

// SalesforceConnection holds connection info
type SalesforceConnection struct {
	InstanceURL    string
	AccessToken    string
	APIVersion     string
	ExpiryTime     time.Time
	OrganizationID string
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50053"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-salesforce", "1.0.0")

	// Register Salesforce executors with schemas
	server.RegisterExecutorWithSchema("sf-query", &SFQueryExecutor{}, SFQuerySchema)
	server.RegisterExecutorWithSchema("sf-create", &SFCreateExecutor{}, SFCreateSchema)
	server.RegisterExecutorWithSchema("sf-update", &SFUpdateExecutor{}, SFUpdateSchema)
	server.RegisterExecutorWithSchema("sf-delete", &SFDeleteExecutor{}, SFDeleteSchema)
	server.RegisterExecutorWithSchema("sf-describe", &SFDescribeExecutor{}, SFDescribeSchema)
	server.RegisterExecutorWithSchema("sf-soql", &SFSOQLExecutor{}, SFSOQLSchema)

	fmt.Printf("Starting skill-salesforce gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getConnection returns a Salesforce connection (cached)
func getConnection(instanceURL, consumerKey, consumerSecret, accessToken string) (*SalesforceConnection, error) {
	key := instanceURL

	connMutex.RLock()
	conn, ok := connections[key]
	connMutex.RUnlock()

	if ok && conn.ExpiryTime.After(time.Now()) {
		return conn, nil
	}

	connMutex.Lock()
	defer connMutex.Unlock()

	// Double check
	if conn, ok := connections[key]; ok && conn.ExpiryTime.After(time.Now()) {
		return conn, nil
	}

	// If access token provided, use it directly
	if accessToken != "" {
		conn = &SalesforceConnection{
			InstanceURL:  instanceURL,
			AccessToken:  accessToken,
			APIVersion:   "v60.0",
			ExpiryTime:   time.Now().Add(2*time.Hour),
		}
		connections[key] = conn
		return conn, nil
	}

	// OAuth 2.0 JWT or Client Credentials flow
	tokenURL := strings.TrimSuffix(instanceURL, "/") + "/services/oauth2/token"

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", consumerKey)
	data.Set("client_secret", consumerSecret)

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request failed: %s", string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		InstanceURL  string `json:"instance_url"`
		ID           string `json:"id"`
		TokenType    string `json:"token_type"`
		IssuedAt     string `json:"issued_at"`
		Signature    string `json:"signature"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	conn = &SalesforceConnection{
		InstanceURL:  tokenResp.InstanceURL,
		AccessToken:  tokenResp.AccessToken,
		APIVersion:   "v60.0",
		ExpiryTime:   time.Now().Add(2*time.Hour),
	}

	// Extract organization ID from ID field (format: https://login.salesforce.com/id/orgID/userID)
	if tokenResp.ID != "" {
		parts := strings.Split(tokenResp.ID, "/")
		if len(parts) >= 6 {
			conn.OrganizationID = parts[5]
		}
	}

	connections[key] = conn
	return conn, nil
}

// makeRequest makes an authenticated request to Salesforce API
func makeRequest(ctx context.Context, conn *SalesforceConnection, method, path string, body interface{}) (*http.Response, error) {
	url := strings.TrimSuffix(conn.InstanceURL, "/") + "/services/data/" + conn.APIVersion + path

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+conn.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// SFQueryExecutor handles sf-query node type
type SFQueryExecutor struct{}

// SFQueryConfig defines the typed configuration for sf-query
type SFQueryConfig struct {
	InstanceURL     string `json:"instanceURL" description:"Salesforce instance URL (e.g., https://mydomain.my.salesforce.com)"`
	ConsumerKey     string `json:"consumerKey" description:"Salesforce Connected App Consumer Key"`
	ConsumerSecret  string `json:"consumerSecret" description:"Salesforce Connected App Consumer Secret"`
	AccessToken     string `json:"accessToken" description:"Optional: Pre-generated access token"`
	Query           string `json:"query" description:"SOQL query to execute"`
}

// SFQuerySchema is the UI schema for sf-query
var SFQuerySchema = resolver.NewSchemaBuilder("sf-query").
	WithName("Salesforce Query").
	WithCategory("salesforce").
	WithIcon("cloud").
	WithDescription("Execute SOQL queries against Salesforce").
	AddSection("Connection").
		AddTextField("instanceURL", "Instance URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://mydomain.my.salesforce.com"),
		).
		AddTextField("consumerKey", "Consumer Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("3MVG9..."),
			resolver.WithHint("Salesforce Connected App Consumer Key"),
		).
		AddTextField("consumerSecret", "Consumer Secret",
			resolver.WithRequired(),
			resolver.WithPlaceholder("A1B2C3D4E5F6..."),
			
			resolver.WithHint("Salesforce Connected App Consumer Secret"),
		).
		AddTextField("accessToken", "Access Token (Optional)",
			resolver.WithPlaceholder("00D...!AQ..."),
			
			resolver.WithHint("Use pre-generated token instead of OAuth flow"),
		).
		EndSection().
	AddSection("Query").
		AddCodeField("query", "SOQL Query", "sql",
			resolver.WithRequired(),
			resolver.WithHeight(150),
			resolver.WithPlaceholder("SELECT Id, Name, Type FROM Account LIMIT 10"),
		).
		EndSection().
	Build()

func (e *SFQueryExecutor) Type() string { return "sf-query" }

func (e *SFQueryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg SFQueryConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.InstanceURL == "" {
		return nil, fmt.Errorf("instanceURL is required")
	}

	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	// Get connection
	conn, err := getConnection(cfg.InstanceURL, cfg.ConsumerKey, cfg.ConsumerSecret, cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	// Execute SOQL query
	resp, err := makeRequest(ctx, conn, "GET", "/query?q="+urlEncode(cfg.Query), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query failed: %s", string(body))
	}

	var result struct {
		TotalSize int                    `json:"totalSize"`
		Done      bool                   `json:"done"`
		Records   []map[string]interface{} `json:"records"`
		NextURL   string                 `json:"nextRecordsUrl"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Clean up records (remove attributes)
	cleanedRecords := make([]map[string]interface{}, 0, len(result.Records))
	for _, rec := range result.Records {
		cleaned := make(map[string]interface{})
		for k, v := range rec {
			if k != "attributes" {
				cleaned[k] = v
			}
		}
		cleanedRecords = append(cleanedRecords, cleaned)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"records":   cleanedRecords,
			"totalSize": result.TotalSize,
			"done":      result.Done,
			"nextURL":   result.NextURL,
		},
	}, nil
}

// SFCreateExecutor handles sf-create node type
type SFCreateExecutor struct{}

// SFCreateConfig defines the typed configuration for sf-create
type SFCreateConfig struct {
	InstanceURL     string                 `json:"instanceURL" description:"Salesforce instance URL"`
	ConsumerKey     string                 `json:"consumerKey" description:"Salesforce Connected App Consumer Key"`
	ConsumerSecret  string                 `json:"consumerSecret" description:"Salesforce Connected App Consumer Secret"`
	AccessToken     string                 `json:"accessToken" description:"Optional: Pre-generated access token"`
	ObjectType      string                 `json:"objectType" description:"Salesforce object type (Account, Contact, Opportunity, Lead, Case)"`
	Data            map[string]interface{} `json:"data" description:"Field-value pairs for the new record"`
}

// SFCreateSchema is the UI schema for sf-create
var SFCreateSchema = resolver.NewSchemaBuilder("sf-create").
	WithName("Salesforce Create").
	WithCategory("salesforce").
	WithIcon("plus-circle").
	WithDescription("Create new records in Salesforce").
	AddSection("Connection").
		AddTextField("instanceURL", "Instance URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://mydomain.my.salesforce.com"),
		).
		AddTextField("consumerKey", "Consumer Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("3MVG9..."),
		).
		AddTextField("consumerSecret", "Consumer Secret",
			resolver.WithRequired(),
			
		).
		AddTextField("accessToken", "Access Token (Optional)",
			
		).
		EndSection().
	AddSection("Record").
		AddSelectField("objectType", "Object Type", []resolver.SelectOption{
			{Label: "Account", Value: "Account", Icon: "building"},
			{Label: "Contact", Value: "Contact", Icon: "user"},
			{Label: "Opportunity", Value: "Opportunity", Icon: "dollar-sign"},
			{Label: "Lead", Value: "Lead", Icon: "user-plus"},
			{Label: "Case", Value: "Case", Icon: "briefcase"},
		}, resolver.WithRequired()).
		AddJSONField("data", "Record Data",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint("JSON object with field-value pairs"),
		).
		EndSection().
	Build()

func (e *SFCreateExecutor) Type() string { return "sf-create" }

func (e *SFCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.InstanceURL == "" {
		return nil, fmt.Errorf("instanceURL is required")
	}

	if cfg.ObjectType == "" {
		return nil, fmt.Errorf("objectType is required")
	}

	if len(cfg.Data) == 0 {
		return nil, fmt.Errorf("data is required")
	}

	conn, err := getConnection(cfg.InstanceURL, cfg.ConsumerKey, cfg.ConsumerSecret, cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	resp, err := makeRequest(ctx, conn, "POST", "/sobjects/"+cfg.ObjectType, cfg.Data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: %s", string(body))
	}

	var result struct {
		ID      string `json:"id"`
		Success bool   `json:"success"`
		Errors  []struct {
			Message   string   `json:"message"`
			Fields    []string `json:"fields"`
			StatusCode string `json:"statusCode"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      result.ID,
			"success": result.Success,
			"object":  cfg.ObjectType,
		},
	}, nil
}

// SFUpdateExecutor handles sf-update node type
type SFUpdateExecutor struct{}

// SFUpdateConfig defines the typed configuration for sf-update
type SFUpdateConfig struct {
	InstanceURL     string                 `json:"instanceURL" description:"Salesforce instance URL"`
	ConsumerKey     string                 `json:"consumerKey" description:"Salesforce Connected App Consumer Key"`
	ConsumerSecret  string                 `json:"consumerSecret" description:"Salesforce Connected App Consumer Secret"`
	AccessToken     string                 `json:"accessToken" description:"Optional: Pre-generated access token"`
	ObjectType      string                 `json:"objectType" description:"Salesforce object type"`
	RecordID        string                 `json:"recordId" description:"ID of the record to update"`
	Data            map[string]interface{} `json:"data" description:"Field-value pairs to update"`
}

// SFUpdateSchema is the UI schema for sf-update
var SFUpdateSchema = resolver.NewSchemaBuilder("sf-update").
	WithName("Salesforce Update").
	WithCategory("salesforce").
	WithIcon("edit").
	WithDescription("Update existing records in Salesforce").
	AddSection("Connection").
		AddTextField("instanceURL", "Instance URL",
			resolver.WithRequired(),
		).
		AddTextField("consumerKey", "Consumer Key",
			resolver.WithRequired(),
		).
		AddTextField("consumerSecret", "Consumer Secret",
			resolver.WithRequired(),
			
		).
		AddTextField("accessToken", "Access Token (Optional)",
			
		).
		EndSection().
	AddSection("Record").
		AddSelectField("objectType", "Object Type", []resolver.SelectOption{
			{Label: "Account", Value: "Account"},
			{Label: "Contact", Value: "Contact"},
			{Label: "Opportunity", Value: "Opportunity"},
			{Label: "Lead", Value: "Lead"},
			{Label: "Case", Value: "Case"},
		}, resolver.WithRequired()).
		AddTextField("recordId", "Record ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("001XXXXXXXXXXXX"),
			resolver.WithHint("18-character Salesforce record ID"),
		).
		AddJSONField("data", "Update Data",
			resolver.WithRequired(),
			resolver.WithHeight(150),
		).
		EndSection().
	Build()

func (e *SFUpdateExecutor) Type() string { return "sf-update" }

func (e *SFUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFUpdateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.InstanceURL == "" {
		return nil, fmt.Errorf("instanceURL is required")
	}

	if cfg.ObjectType == "" {
		return nil, fmt.Errorf("objectType is required")
	}

	if cfg.RecordID == "" {
		return nil, fmt.Errorf("recordId is required")
	}

	if len(cfg.Data) == 0 {
		return nil, fmt.Errorf("data is required")
	}

	conn, err := getConnection(cfg.InstanceURL, cfg.ConsumerKey, cfg.ConsumerSecret, cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	resp, err := makeRequest(ctx, conn, "PATCH", "/sobjects/"+cfg.ObjectType+"/"+cfg.RecordID, cfg.Data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// 204 No Content is expected for successful update
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update failed: %s", string(body))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"recordId": cfg.RecordID,
			"object":   cfg.ObjectType,
		},
	}, nil
}

// SFDeleteExecutor handles sf-delete node type
type SFDeleteExecutor struct{}

// SFDeleteConfig defines the typed configuration for sf-delete
type SFDeleteConfig struct {
	InstanceURL     string `json:"instanceURL" description:"Salesforce instance URL"`
	ConsumerKey     string `json:"consumerKey" description:"Salesforce Connected App Consumer Key"`
	ConsumerSecret  string `json:"consumerSecret" description:"Salesforce Connected App Consumer Secret"`
	AccessToken     string `json:"accessToken" description:"Optional: Pre-generated access token"`
	ObjectType      string `json:"objectType" description:"Salesforce object type"`
	RecordID        string `json:"recordId" description:"ID of the record to delete"`
}

// SFDeleteSchema is the UI schema for sf-delete
var SFDeleteSchema = resolver.NewSchemaBuilder("sf-delete").
	WithName("Salesforce Delete").
	WithCategory("salesforce").
	WithIcon("trash").
	WithDescription("Delete records from Salesforce").
	AddSection("Connection").
		AddTextField("instanceURL", "Instance URL",
			resolver.WithRequired(),
		).
		AddTextField("consumerKey", "Consumer Key",
			resolver.WithRequired(),
		).
		AddTextField("consumerSecret", "Consumer Secret",
			resolver.WithRequired(),
			
		).
		AddTextField("accessToken", "Access Token (Optional)",
			
		).
		EndSection().
	AddSection("Record").
		AddSelectField("objectType", "Object Type", []resolver.SelectOption{
			{Label: "Account", Value: "Account"},
			{Label: "Contact", Value: "Contact"},
			{Label: "Opportunity", Value: "Opportunity"},
			{Label: "Lead", Value: "Lead"},
			{Label: "Case", Value: "Case"},
		}, resolver.WithRequired()).
		AddTextField("recordId", "Record ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("001XXXXXXXXXXXX"),
		).
		EndSection().
	Build()

func (e *SFDeleteExecutor) Type() string { return "sf-delete" }

func (e *SFDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFDeleteConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.InstanceURL == "" {
		return nil, fmt.Errorf("instanceURL is required")
	}

	if cfg.ObjectType == "" {
		return nil, fmt.Errorf("objectType is required")
	}

	if cfg.RecordID == "" {
		return nil, fmt.Errorf("recordId is required")
	}

	conn, err := getConnection(cfg.InstanceURL, cfg.ConsumerKey, cfg.ConsumerSecret, cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	resp, err := makeRequest(ctx, conn, "DELETE", "/sobjects/"+cfg.ObjectType+"/"+cfg.RecordID, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// 204 No Content is expected for successful delete
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("delete failed: %s", string(body))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"recordId": cfg.RecordID,
			"object":   cfg.ObjectType,
		},
	}, nil
}

// SFDescribeExecutor handles sf-describe node type
type SFDescribeExecutor struct{}

// SFDescribeConfig defines the typed configuration for sf-describe
type SFDescribeConfig struct {
	InstanceURL     string `json:"instanceURL" description:"Salesforce instance URL"`
	ConsumerKey     string `json:"consumerKey" description:"Salesforce Connected App Consumer Key"`
	ConsumerSecret  string `json:"consumerSecret" description:"Salesforce Connected App Consumer Secret"`
	AccessToken     string `json:"accessToken" description:"Optional: Pre-generated access token"`
	ObjectType      string `json:"objectType" description:"Salesforce object type to describe"`
}

// SFDescribeSchema is the UI schema for sf-describe
var SFDescribeSchema = resolver.NewSchemaBuilder("sf-describe").
	WithName("Salesforce Describe").
	WithCategory("salesforce").
	WithIcon("info").
	WithDescription("Get object metadata and field definitions").
	AddSection("Connection").
		AddTextField("instanceURL", "Instance URL",
			resolver.WithRequired(),
		).
		AddTextField("consumerKey", "Consumer Key",
			resolver.WithRequired(),
		).
		AddTextField("consumerSecret", "Consumer Secret",
			resolver.WithRequired(),
			
		).
		AddTextField("accessToken", "Access Token (Optional)",
			
		).
		EndSection().
	AddSection("Object").
		AddSelectField("objectType", "Object Type", []resolver.SelectOption{
			{Label: "Account", Value: "Account"},
			{Label: "Contact", Value: "Contact"},
			{Label: "Opportunity", Value: "Opportunity"},
			{Label: "Lead", Value: "Lead"},
			{Label: "Case", Value: "Case"},
			{Label: "Custom", Value: "custom", Icon: "plus"},
		}, resolver.WithRequired()).
		AddTextField("customObject", "Custom Object API Name",
			resolver.WithPlaceholder("My_Custom_Object__c"),
			resolver.WithHint("Only required if Object Type is Custom"),
		).
		EndSection().
	Build()

func (e *SFDescribeExecutor) Type() string { return "sf-describe" }

func (e *SFDescribeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFDescribeConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.InstanceURL == "" {
		return nil, fmt.Errorf("instanceURL is required")
	}

	objectType := cfg.ObjectType
	if objectType == "custom" {
		if customObj, ok := step.Config["customObject"].(string); ok && customObj != "" {
			objectType = customObj
		} else {
			return nil, fmt.Errorf("customObject is required when objectType is 'custom'")
		}
	}

	if objectType == "" {
		return nil, fmt.Errorf("objectType is required")
	}

	conn, err := getConnection(cfg.InstanceURL, cfg.ConsumerKey, cfg.ConsumerSecret, cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	resp, err := makeRequest(ctx, conn, "GET", "/sobjects/"+objectType+"/describe", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("describe failed: %s", string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract key information
	fields := []map[string]interface{}{}
	if fieldsRaw, ok := result["fields"].([]interface{}); ok {
		for _, f := range fieldsRaw {
			if fieldMap, ok := f.(map[string]interface{}); ok {
				fields = append(fields, map[string]interface{}{
					"name":         fieldMap["name"],
					"label":        fieldMap["label"],
					"type":         fieldMap["type"],
					"length":       fieldMap["length"],
					"required":     fieldMap["nillable"] == false && fieldMap["defaultValue"] == nil,
					"creatable":    fieldMap["createable"],
					"updateable":   fieldMap["updateable"],
					"calculated":   fieldMap["calculated"],
				})
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name":           result["name"],
			"label":          result["label"],
			"keyPrefix":      result["keyPrefix"],
			"createable":     result["createable"],
			"updateable":     result["updateable"],
			"deletable":      result["deletable"],
			"fields":         fields,
			"fieldCount":     len(fields),
			"recordTypes":    result["recordTypeInfos"],
		},
	}, nil
}

// SFSOQLExecutor handles sf-soql node type (alias for sf-query with simpler interface)
type SFSOQLExecutor struct{}

// SFSOQLConfig defines the typed configuration for sf-soql
type SFSOQLConfig struct {
	InstanceURL     string `json:"instanceURL" description:"Salesforce instance URL"`
	ConsumerKey     string `json:"consumerKey" description:"Salesforce Connected App Consumer Key"`
	ConsumerSecret  string `json:"consumerSecret" description:"Salesforce Connected App Consumer Secret"`
	AccessToken     string `json:"accessToken" description:"Optional: Pre-generated access token"`
	Query           string `json:"query" description:"SOQL query to execute"`
	ObjectType      string `json:"objectType" description:"Optional: Object type for query builder mode"`
	Fields          string `json:"fields" description:"Optional: Comma-separated fields (for query builder mode)"`
	WhereClause     string `json:"whereClause" description:"Optional: WHERE clause (for query builder mode)"`
	Limit           int    `json:"limit" default:"100" description:"Optional: LIMIT clause (for query builder mode)"`
}

// SFSOQLSchema is the UI schema for sf-soql
var SFSOQLSchema = resolver.NewSchemaBuilder("sf-soql").
	WithName("Salesforce SOQL").
	WithCategory("salesforce").
	WithIcon("database").
	WithDescription("Execute SOQL queries with optional query builder").
	AddSection("Connection").
		AddTextField("instanceURL", "Instance URL",
			resolver.WithRequired(),
		).
		AddTextField("consumerKey", "Consumer Key",
			resolver.WithRequired(),
		).
		AddTextField("consumerSecret", "Consumer Secret",
			resolver.WithRequired(),
			
		).
		AddTextField("accessToken", "Access Token (Optional)",
			
		).
		EndSection().
	AddSection("Query Mode").
		AddSelectField("mode", "Query Mode", []resolver.SelectOption{
			{Label: "Raw SOQL", Value: "raw", Icon: "code"},
			{Label: "Query Builder", Value: "builder", Icon: "layout-template"},
		}, resolver.WithDefault("raw")).
		EndSection().
	AddSection("Raw Query").
		AddCodeField("query", "SOQL Query", "sql",
			resolver.WithHeight(150),
			resolver.WithPlaceholder("SELECT Id, Name FROM Account WHERE Type = 'Customer'"),
			resolver.WithHint("Use raw SOQL syntax"),
		).
		EndSection().
	AddSection("Query Builder").
		AddSelectField("objectType", "Object Type", []resolver.SelectOption{
			{Label: "Account", Value: "Account"},
			{Label: "Contact", Value: "Contact"},
			{Label: "Opportunity", Value: "Opportunity"},
			{Label: "Lead", Value: "Lead"},
			{Label: "Case", Value: "Case"},
		}).
		AddTextField("fields", "Fields",
			resolver.WithPlaceholder("Id, Name, CreatedDate"),
			resolver.WithHint("Comma-separated field names"),
		).
		AddTextareaField("whereClause", "WHERE Clause",
			resolver.WithRows(3),
			resolver.WithPlaceholder("Type = 'Customer' AND CreatedDate = LAST_N_DAYS:30"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 2000),
		).
		EndSection().
	Build()

func (e *SFSOQLExecutor) Type() string { return "sf-soql" }

func (e *SFSOQLExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFSOQLConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.InstanceURL == "" {
		return nil, fmt.Errorf("instanceURL is required")
	}

	// Build query based on mode
	mode, _ := step.Config["mode"].(string)
	query := cfg.Query

	if mode == "builder" && cfg.ObjectType != "" {
		// Build SOQL from builder components
		fields := cfg.Fields
		if fields == "" {
			fields = "Id"
		}

		query = fmt.Sprintf("SELECT %s FROM %s", fields, cfg.ObjectType)

		if cfg.WhereClause != "" {
			query += fmt.Sprintf(" WHERE %s", cfg.WhereClause)
		}

		if cfg.Limit > 0 {
			query += fmt.Sprintf(" LIMIT %d", cfg.Limit)
		}
	}

	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	conn, err := getConnection(cfg.InstanceURL, cfg.ConsumerKey, cfg.ConsumerSecret, cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	resp, err := makeRequest(ctx, conn, "GET", "/query?q="+urlEncode(query), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query failed: %s", string(body))
	}

	var result struct {
		TotalSize int                    `json:"totalSize"`
		Done      bool                   `json:"done"`
		Records   []map[string]interface{} `json:"records"`
		NextURL   string                 `json:"nextRecordsUrl"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Clean up records
	cleanedRecords := make([]map[string]interface{}, 0, len(result.Records))
	for _, rec := range result.Records {
		cleaned := make(map[string]interface{})
		for k, v := range rec {
			if k != "attributes" {
				cleaned[k] = v
			}
		}
		cleanedRecords = append(cleanedRecords, cleaned)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"records":   cleanedRecords,
			"totalSize": result.TotalSize,
			"done":      result.Done,
			"nextURL":   result.NextURL,
			"query":     query,
		},
	}, nil
}

// urlEncode performs basic URL encoding
func urlEncode(s string) string {
	// Simple URL encoding for SOQL queries
	replacer := strings.NewReplacer(
		" ", "%20",
		"<", "%3C",
		">", "%3E",
		"#", "%23",
		"%", "%25",
		"{", "%7B",
		"}", "%7D",
		"|", "%7C",
		"\\", "%5C",
		"^", "%5E",
		"~", "%7E",
		"[", "%5B",
		"]", "%5D",
		"`", "%60",
		";", "%3B",
		"/", "%2F",
		"?", "%3F",
		":", "%3A",
		"@", "%40",
		"=", "%3D",
		"&", "%26",
		"$", "%24",
	)
	return replacer.Replace(s)
}
