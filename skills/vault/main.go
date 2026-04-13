package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconVault = "lock"
)

// VaultClient holds the HTTP client and base URL
type VaultClient struct {
	client  *http.Client
	baseURL string
	token   string
}

// NewVaultClient creates a new Vault client
func NewVaultClient(address, token string) *VaultClient {
	return &VaultClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: strings.TrimSuffix(address, "/"),
		token:   token,
	}
}

// doRequest performs an HTTP request to Vault
func (c *VaultClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	url := fmt.Sprintf("%s/v1/%s", c.baseURL, path)

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	if c.token != "" {
		req.Header.Set("X-Vault-Token", c.token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return respBody, resp.StatusCode, fmt.Errorf("vault error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, resp.StatusCode, nil
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt(config map[string]interface{}, key string, def int) int {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func getStringSlice(config map[string]interface{}, key string) []string {
	if v, ok := config[key]; ok {
		switch arr := v.(type) {
		case []interface{}:
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		case []string:
			return arr
		case string:
			return strings.Split(arr, ",")
		}
	}
	return nil
}

func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
		// Try to parse from JSON string
		if s, ok := v.(string); ok {
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(s), &m); err == nil {
				return m
			}
		}
	}
	return nil
}

// ============================================================================
// SCHEMA BUILDERS
// ============================================================================

func addConnectionSection(builder *resolver.SchemaBuilder) *resolver.SchemaBuilder {
	return builder.
		AddSection("Connection").
		AddExpressionField("address", "Vault Address",
			resolver.WithDefault("http://127.0.0.1:8200"),
			resolver.WithPlaceholder("http://127.0.0.1:8200"),
			resolver.WithHint("HashiCorp Vault server URL"),
		).
		AddExpressionField("token", "Token",
			resolver.WithPlaceholder("s.xxxxxx"),
			resolver.WithHint("Vault authentication token"),
			resolver.WithSensitive(),
		).
		EndSection()
}

// ============================================================================
// SCHEMAS - KV OPERATIONS
// ============================================================================

// KvGetSchema is the UI schema for vault-kv-get
var KvGetSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-kv-get")).
	WithName("Vault KV Get").
	WithCategory("read").
	WithIcon(iconVault).
	WithDescription("Read a secret from Vault KV secrets engine").
	AddSection("Secret Path").
		AddExpressionField("path", "Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret/data/myapp"),
			resolver.WithHint("Path to the secret (e.g., secret/data/myapp for KV v2)"),
		).
		AddSelectField("version", "KV Version",
			[]resolver.SelectOption{
				{Value: "2", Label: "KV v2"},
				{Value: "1", Label: "KV v1"},
			},
			resolver.WithDefault("2"),
			resolver.WithHint("KV secrets engine version"),
		).
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("secret"),
			resolver.WithPlaceholder("secret"),
			resolver.WithHint("KV mount point (default: secret)"),
		).
		EndSection().
	Build()

// KvPutSchema is the UI schema for vault-kv-put
var KvPutSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-kv-put")).
	WithName("Vault KV Put").
	WithCategory("write").
	WithIcon(iconVault).
	WithDescription("Write a secret to Vault KV secrets engine").
	AddSection("Secret Path").
		AddExpressionField("path", "Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret/data/myapp"),
			resolver.WithHint("Path to the secret"),
		).
		AddSelectField("version", "KV Version",
			[]resolver.SelectOption{
				{Value: "2", Label: "KV v2"},
				{Value: "1", Label: "KV v1"},
			},
			resolver.WithDefault("2"),
		).
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("secret"),
			resolver.WithPlaceholder("secret"),
			resolver.WithHint("KV mount point (default: secret)"),
		).
		EndSection().
	AddSection("Secret Data").
		AddTextareaField("data", "Data (JSON)",
			resolver.WithRequired(),
			resolver.WithRows(5),
			resolver.WithPlaceholder(`{"username": "admin", "password": "secret123"}`),
			resolver.WithHint("Secret data as JSON object"),
		).
		EndSection().
	Build()

// KvDeleteSchema is the UI schema for vault-kv-delete
var KvDeleteSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-kv-delete")).
	WithName("Vault KV Delete").
	WithCategory("delete").
	WithIcon(iconVault).
	WithDescription("Delete a secret from Vault KV secrets engine").
	AddSection("Secret Path").
		AddExpressionField("path", "Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret/data/myapp"),
			resolver.WithHint("Path to the secret to delete"),
		).
		AddSelectField("version", "KV Version",
			[]resolver.SelectOption{
				{Value: "2", Label: "KV v2"},
				{Value: "1", Label: "KV v1"},
			},
			resolver.WithDefault("2"),
		).
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("secret"),
			resolver.WithPlaceholder("secret"),
			resolver.WithHint("KV mount point (default: secret)"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - GENERIC OPERATIONS
// ============================================================================

// ReadSchema is the UI schema for vault-read
var ReadSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-read")).
	WithName("Vault Read").
	WithCategory("read").
	WithIcon(iconVault).
	WithDescription("Read data from any Vault path").
	AddSection("Path").
		AddExpressionField("path", "Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sys/policies/acl"),
			resolver.WithHint("Full Vault API path (e.g., sys/policies/acl)"),
		).
		EndSection().
	Build()

// WriteSchema is the UI schema for vault-write
var WriteSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-write")).
	WithName("Vault Write").
	WithCategory("write").
	WithIcon(iconVault).
	WithDescription("Write data to any Vault path").
	AddSection("Path & Data").
		AddExpressionField("path", "Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret/data/myapp"),
			resolver.WithHint("Full Vault API path"),
		).
		AddTextareaField("data", "Data (JSON)",
			resolver.WithRequired(),
			resolver.WithRows(5),
			resolver.WithPlaceholder(`{"key": "value"}`),
			resolver.WithHint("Data to write as JSON object"),
		).
		EndSection().
	Build()

// DeleteSchema is the UI schema for vault-delete
var DeleteSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-delete")).
	WithName("Vault Delete").
	WithCategory("delete").
	WithIcon(iconVault).
	WithDescription("Delete data from any Vault path").
	AddSection("Path").
		AddExpressionField("path", "Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret/data/myapp"),
			resolver.WithHint("Full Vault API path to delete"),
		).
		EndSection().
	Build()

// ListSchema is the UI schema for vault-list
var ListSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-list")).
	WithName("Vault List").
	WithCategory("read").
	WithIcon(iconVault).
	WithDescription("List secrets at a Vault path").
	AddSection("Path").
		AddExpressionField("path", "Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret/data/"),
			resolver.WithHint("Path to list (should end with / for directories)"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - LEASE OPERATIONS
// ============================================================================

// LeaseRenewSchema is the UI schema for vault-lease-renew
var LeaseRenewSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-lease-renew")).
	WithName("Vault Lease Renew").
	WithCategory("lease").
	WithIcon(iconVault).
	WithDescription("Renew a Vault lease").
	AddSection("Lease").
		AddExpressionField("leaseId", "Lease ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret/data/myapp/abc123"),
			resolver.WithHint("The lease ID to renew"),
		).
		AddNumberField("increment", "Increment (seconds)",
			resolver.WithDefault(0),
			resolver.WithHint("Additional time to add to lease (0 = use default)"),
		).
		EndSection().
	Build()

// LeaseRevokeSchema is the UI schema for vault-lease-revoke
var LeaseRevokeSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-lease-revoke")).
	WithName("Vault Lease Revoke").
	WithCategory("lease").
	WithIcon(iconVault).
	WithDescription("Revoke a Vault lease").
	AddSection("Lease").
		AddExpressionField("leaseId", "Lease ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret/data/myapp/abc123"),
			resolver.WithHint("The lease ID to revoke"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - TOKEN OPERATIONS
// ============================================================================

// TokenLookupSchema is the UI schema for vault-token-lookup
var TokenLookupSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-token-lookup")).
	WithName("Vault Token Lookup").
	WithCategory("token").
	WithIcon(iconVault).
	WithDescription("Look up information about a Vault token").
	AddSection("Token").
		AddExpressionField("token", "Token to Lookup",
			resolver.WithPlaceholder("s.xxxxxx"),
			resolver.WithHint("Token to look up (empty = current token)"),
			resolver.WithSensitive(),
		).
		AddToggleField("self", "Lookup Self",
			resolver.WithDefault(false),
			resolver.WithHint("Look up the current token instead"),
		).
		EndSection().
	Build()

// TokenRenewSchema is the UI schema for vault-token-renew
var TokenRenewSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-token-renew")).
	WithName("Vault Token Renew").
	WithCategory("token").
	WithIcon(iconVault).
	WithDescription("Renew a Vault token").
	AddSection("Token").
		AddExpressionField("token", "Token to Renew",
			resolver.WithPlaceholder("s.xxxxxx"),
			resolver.WithHint("Token to renew (empty = current token)"),
			resolver.WithSensitive(),
		).
		AddToggleField("self", "Renew Self",
			resolver.WithDefault(true),
			resolver.WithHint("Renew the current token"),
		).
		AddNumberField("increment", "Increment (seconds)",
			resolver.WithDefault(0),
			resolver.WithHint("Additional time to add (0 = use default)"),
		).
		EndSection().
	Build()

// TokenCreateSchema is the UI schema for vault-token-create
var TokenCreateSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-token-create")).
	WithName("Vault Token Create").
	WithCategory("token").
	WithIcon(iconVault).
	WithDescription("Create a new Vault token").
	AddSection("Token Settings").
		AddExpressionField("policies", "Policies",
			resolver.WithPlaceholder("default,my-policy"),
			resolver.WithHint("Comma-separated list of policies"),
		).
		AddExpressionField("ttl", "TTL",
			resolver.WithPlaceholder("1h"),
			resolver.WithHint("Token TTL (e.g., 1h, 24h, 7d)"),
		).
		AddExpressionField("maxTtl", "Max TTL",
			resolver.WithPlaceholder("24h"),
			resolver.WithHint("Maximum TTL (e.g., 24h, 7d)"),
		).
		AddExpressionField("displayName", "Display Name",
			resolver.WithPlaceholder("my-token"),
			resolver.WithHint("Display name for the token"),
		).
		AddToggleField("renewable", "Renewable",
			resolver.WithDefault(true),
			resolver.WithHint("Whether the token can be renewed"),
		).
		AddToggleField("orphan", "Orphan",
			resolver.WithDefault(false),
			resolver.WithHint("Create an orphan token (no parent)"),
		).
		EndSection().
	Build()

// TokenRevokeSchema is the UI schema for vault-token-revoke
var TokenRevokeSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-token-revoke")).
	WithName("Vault Token Revoke").
	WithCategory("token").
	WithIcon(iconVault).
	WithDescription("Revoke a Vault token").
	AddSection("Token").
		AddExpressionField("token", "Token to Revoke",
			resolver.WithPlaceholder("s.xxxxxx"),
			resolver.WithHint("Token to revoke"),
			resolver.WithSensitive(),
		).
		AddToggleField("self", "Revoke Self",
			resolver.WithDefault(false),
			resolver.WithHint("Revoke the current token (warning: will invalidate current session)"),
		).
		AddToggleField("orphan", "Revoke Orphan",
			resolver.WithDefault(false),
			resolver.WithHint("Revoke orphan token and all its children"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - PKI OPERATIONS
// ============================================================================

// PkiIssueSchema is the UI schema for vault-pki-issue
var PkiIssueSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-pki-issue")).
	WithName("Vault PKI Issue").
	WithCategory("pki").
	WithIcon(iconVault).
	WithDescription("Issue a certificate from Vault PKI").
	AddSection("PKI Settings").
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("pki"),
			resolver.WithPlaceholder("pki"),
			resolver.WithHint("PKI mount point"),
		).
		AddExpressionField("role", "Role",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-role"),
			resolver.WithHint("PKI role name"),
		).
		AddExpressionField("commonName", "Common Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("example.com"),
			resolver.WithHint("Certificate common name"),
		).
		AddExpressionField("altNames", "Alt Names",
			resolver.WithPlaceholder("www.example.com,api.example.com"),
			resolver.WithHint("Comma-separated alternative names"),
		).
		AddExpressionField("ipSans", "IP SANs",
			resolver.WithPlaceholder("192.168.1.1,10.0.0.1"),
			resolver.WithHint("Comma-separated IP addresses"),
		).
		AddExpressionField("ttl", "TTL",
			resolver.WithPlaceholder("720h"),
			resolver.WithHint("Certificate TTL (e.g., 720h, 30d)"),
		).
		EndSection().
	Build()

// PkiRenewSchema is the UI schema for vault-pki-renew
var PkiRenewSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-pki-renew")).
	WithName("Vault PKI Renew").
	WithCategory("pki").
	WithIcon(iconVault).
	WithDescription("Renew a certificate from Vault PKI").
	AddSection("PKI Settings").
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("pki"),
			resolver.WithPlaceholder("pki"),
			resolver.WithHint("PKI mount point"),
		).
		AddExpressionField("serial", "Serial Number",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00:11:22:33:44:55"),
			resolver.WithHint("Certificate serial number"),
		).
		AddExpressionField("ttl", "TTL",
			resolver.WithPlaceholder("720h"),
			resolver.WithHint("New TTL for the certificate"),
		).
		EndSection().
	Build()

// PkiRevokeSchema is the UI schema for vault-pki-revoke
var PkiRevokeSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-pki-revoke")).
	WithName("Vault PKI Revoke").
	WithCategory("pki").
	WithIcon(iconVault).
	WithDescription("Revoke a certificate from Vault PKI").
	AddSection("PKI Settings").
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("pki"),
			resolver.WithPlaceholder("pki"),
			resolver.WithHint("PKI mount point"),
		).
		AddExpressionField("serial", "Serial Number",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00:11:22:33:44:55"),
			resolver.WithHint("Certificate serial number to revoke"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - DATABASE OPERATIONS
// ============================================================================

// DatabaseCredsSchema is the UI schema for vault-database-creds
var DatabaseCredsSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-database-creds")).
	WithName("Vault Database Creds").
	WithCategory("database").
	WithIcon(iconVault).
	WithDescription("Get dynamic database credentials from Vault").
	AddSection("Database Settings").
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("database"),
			resolver.WithPlaceholder("database"),
			resolver.WithHint("Database secrets mount point"),
		).
		AddExpressionField("role", "Role",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-db-role"),
			resolver.WithHint("Database role name"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - TRANSIT OPERATIONS
// ============================================================================

// TransitEncryptSchema is the UI schema for vault-transit-encrypt
var TransitEncryptSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-transit-encrypt")).
	WithName("Vault Transit Encrypt").
	WithCategory("transit").
	WithIcon(iconVault).
	WithDescription("Encrypt data using Vault Transit engine").
	AddSection("Transit Settings").
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("transit"),
			resolver.WithPlaceholder("transit"),
			resolver.WithHint("Transit mount point"),
		).
		AddExpressionField("key", "Key Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-key"),
			resolver.WithHint("Transit key name"),
		).
		AddTextareaField("plaintext", "Plaintext",
			resolver.WithRequired(),
			resolver.WithRows(3),
			resolver.WithPlaceholder("data to encrypt"),
			resolver.WithHint("Data to encrypt"),
		).
		AddExpressionField("context", "Context",
			resolver.WithPlaceholder("optional-context"),
			resolver.WithHint("Additional context for encryption (optional)"),
		).
		EndSection().
	Build()

// TransitDecryptSchema is the UI schema for vault-transit-decrypt
var TransitDecryptSchema = addConnectionSection(resolver.NewSchemaBuilder("vault-transit-decrypt")).
	WithName("Vault Transit Decrypt").
	WithCategory("transit").
	WithIcon(iconVault).
	WithDescription("Decrypt data using Vault Transit engine").
	AddSection("Transit Settings").
		AddExpressionField("mount", "Mount Point",
			resolver.WithDefault("transit"),
			resolver.WithPlaceholder("transit"),
			resolver.WithHint("Transit mount point"),
		).
		AddExpressionField("key", "Key Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-key"),
			resolver.WithHint("Transit key name"),
		).
		AddTextareaField("ciphertext", "Ciphertext",
			resolver.WithRequired(),
			resolver.WithRows(3),
			resolver.WithPlaceholder("vault:v1:abcd..."),
			resolver.WithHint("Encrypted data (vault:v1:... format)"),
		).
		AddExpressionField("context", "Context",
			resolver.WithPlaceholder("optional-context"),
			resolver.WithHint("Additional context for decryption (optional)"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - HEALTH
// ============================================================================

// HealthSchema is the UI schema for vault-health
var HealthSchema = resolver.NewSchemaBuilder("vault-health").
	WithName("Vault Health").
	WithCategory("system").
	WithIcon(iconVault).
	WithDescription("Check Vault server health status").
	AddSection("Connection").
		AddExpressionField("address", "Vault Address",
			resolver.WithDefault("http://127.0.0.1:8200"),
			resolver.WithPlaceholder("http://127.0.0.1:8200"),
			resolver.WithHint("HashiCorp Vault server URL"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS - KV OPERATIONS
// ============================================================================

// KvGetExecutor handles vault-kv-get
type KvGetExecutor struct{}

func (e *KvGetExecutor) Type() string { return "vault-kv-get" }

func (e *KvGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	path := resolver.ResolveString(getString(config, "path"))
	mount := resolver.ResolveString(getString(config, "mount"))
	version := resolver.ResolveString(getString(config, "version"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	if mount == "" {
		mount = "secret"
	}
	if version == "" {
		version = "2"
	}

	var vaultPath string
	if version == "2" {
		vaultPath = fmt.Sprintf("%s/data/%s", mount, path)
	} else {
		vaultPath = fmt.Sprintf("%s/%s", mount, path)
	}

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "GET", vaultPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	// Extract data for convenience
	if data, ok := result["data"].(map[string]interface{}); ok {
		if version == "2" {
			if innerData, ok := data["data"].(map[string]interface{}); ok {
				output["data"] = innerData
				// Also add individual keys
				for k, v := range innerData {
					output[k] = v
				}
			}
		} else {
			output["data"] = data
			for k, v := range data {
				output[k] = v
			}
		}
	}

	if metadata, ok := result["metadata"].(map[string]interface{}); ok {
		output["metadata"] = metadata
	}

	return &executor.StepResult{Output: output}, nil
}

// KvPutExecutor handles vault-kv-put
type KvPutExecutor struct{}

func (e *KvPutExecutor) Type() string { return "vault-kv-put" }

func (e *KvPutExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	path := resolver.ResolveString(getString(config, "path"))
	mount := resolver.ResolveString(getString(config, "mount"))
	version := resolver.ResolveString(getString(config, "version"))
	dataStr := resolver.ResolveString(getString(config, "data"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if dataStr == "" {
		return nil, fmt.Errorf("data is required")
	}

	if mount == "" {
		mount = "secret"
	}
	if version == "" {
		version = "2"
	}

	var secretData map[string]interface{}
	if err := json.Unmarshal([]byte(dataStr), &secretData); err != nil {
		return nil, fmt.Errorf("invalid JSON data: %w", err)
	}

	var vaultPath string
	var body interface{}

	if version == "2" {
		vaultPath = fmt.Sprintf("%s/data/%s", mount, path)
		body = map[string]interface{}{
			"data": secretData,
		}
	} else {
		vaultPath = fmt.Sprintf("%s/%s", mount, path)
		body = secretData
	}

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "POST", vaultPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to write secret: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw":     string(respBody),
		"success": true,
		"path":    vaultPath,
	}

	if metadata, ok := result["data"].(map[string]interface{}); ok {
		output["metadata"] = metadata
	}

	return &executor.StepResult{Output: output}, nil
}

// KvDeleteExecutor handles vault-kv-delete
type KvDeleteExecutor struct{}

func (e *KvDeleteExecutor) Type() string { return "vault-kv-delete" }

func (e *KvDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	path := resolver.ResolveString(getString(config, "path"))
	mount := resolver.ResolveString(getString(config, "mount"))
	version := resolver.ResolveString(getString(config, "version"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	if mount == "" {
		mount = "secret"
	}
	if version == "" {
		version = "2"
	}

	var vaultPath string
	if version == "2" {
		vaultPath = fmt.Sprintf("%s/data/%s", mount, path)
	} else {
		vaultPath = fmt.Sprintf("%s/%s", mount, path)
	}

	client := NewVaultClient(address, token)
	_, _, err := client.doRequest(ctx, "DELETE", vaultPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to delete secret: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"path":    vaultPath,
		},
	}, nil
}

// ============================================================================
// EXECUTORS - GENERIC OPERATIONS
// ============================================================================

// ReadExecutor handles vault-read
type ReadExecutor struct{}

func (e *ReadExecutor) Type() string { return "vault-read" }

func (e *ReadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	path := resolver.ResolveString(getString(config, "path"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	client := NewVaultClient(address, token)
	respBody, statusCode, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read from %s: %w", path, err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{
				"raw":        string(respBody),
				"statusCode": statusCode,
			},
		}, nil
	}

	output := map[string]interface{}{
		"raw":        string(respBody),
		"statusCode": statusCode,
		"data":       result["data"],
	}

	// Flatten data fields
	if data, ok := result["data"].(map[string]interface{}); ok {
		for k, v := range data {
			output[k] = v
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// WriteExecutor handles vault-write
type WriteExecutor struct{}

func (e *WriteExecutor) Type() string { return "vault-write" }

func (e *WriteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	path := resolver.ResolveString(getString(config, "path"))
	dataStr := resolver.ResolveString(getString(config, "data"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if dataStr == "" {
		return nil, fmt.Errorf("data is required")
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
		return nil, fmt.Errorf("invalid JSON data: %w", err)
	}

	client := NewVaultClient(address, token)
	respBody, statusCode, err := client.doRequest(ctx, "POST", path, data)
	if err != nil {
		return nil, fmt.Errorf("failed to write to %s: %w", path, err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{
				"raw":        string(respBody),
				"statusCode": statusCode,
				"success":    true,
			},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"raw":        string(respBody),
			"statusCode": statusCode,
			"success":    true,
			"data":       result["data"],
		},
	}, nil
}

// DeleteExecutor handles vault-delete
type DeleteExecutor struct{}

func (e *DeleteExecutor) Type() string { return "vault-delete" }

func (e *DeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	path := resolver.ResolveString(getString(config, "path"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	client := NewVaultClient(address, token)
	_, statusCode, err := client.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to delete %s: %w", path, err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"path":       path,
			"statusCode": statusCode,
		},
	}, nil
}

// ListExecutor handles vault-list
type ListExecutor struct{}

func (e *ListExecutor) Type() string { return "vault-list" }

func (e *ListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	path := resolver.ResolveString(getString(config, "path"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	client := NewVaultClient(address, token)

	// Vault LIST uses GET with list=true query parameter
	vaultPath := fmt.Sprintf("%s?list=true", path)
	respBody, _, err := client.doRequest(ctx, "GET", vaultPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", path, err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	// Extract keys from response
	if data, ok := result["data"].(map[string]interface{}); ok {
		if keys, ok := data["keys"].([]interface{}); ok {
			output["keys"] = keys
			// Also add as string array
			keyStrings := make([]string, len(keys))
			for i, k := range keys {
				if s, ok := k.(string); ok {
					keyStrings[i] = s
				}
			}
			output["keyNames"] = keyStrings
		}
		if keyInfo, ok := data["key_info"].(map[string]interface{}); ok {
			output["keyInfo"] = keyInfo
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// EXECUTORS - LEASE OPERATIONS
// ============================================================================

// LeaseRenewExecutor handles vault-lease-renew
type LeaseRenewExecutor struct{}

func (e *LeaseRenewExecutor) Type() string { return "vault-lease-renew" }

func (e *LeaseRenewExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	leaseID := resolver.ResolveString(getString(config, "leaseId"))
	increment := getInt(config, "increment", 0)

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if leaseID == "" {
		return nil, fmt.Errorf("lease ID is required")
	}

	body := map[string]interface{}{
		"lease_id": leaseID,
	}
	if increment > 0 {
		body["increment"] = fmt.Sprintf("%ds", increment)
	}

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "PUT", "sys/leases/renew", body)
	if err != nil {
		return nil, fmt.Errorf("failed to renew lease: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if data, ok := result["data"].(map[string]interface{}); ok {
		output["leaseId"] = data["id"]
		if leaseDuration, ok := data["lease_duration"].(float64); ok {
			output["leaseDuration"] = int(leaseDuration)
		}
		if renewable, ok := data["renewable"].(bool); ok {
			output["renewable"] = renewable
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// LeaseRevokeExecutor handles vault-lease-revoke
type LeaseRevokeExecutor struct{}

func (e *LeaseRevokeExecutor) Type() string { return "vault-lease-revoke" }

func (e *LeaseRevokeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	leaseID := resolver.ResolveString(getString(config, "leaseId"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if leaseID == "" {
		return nil, fmt.Errorf("lease ID is required")
	}

	body := map[string]interface{}{
		"lease_id": leaseID,
	}

	client := NewVaultClient(address, token)
	_, _, err := client.doRequest(ctx, "PUT", "sys/leases/revoke", body)
	if err != nil {
		return nil, fmt.Errorf("failed to revoke lease: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"leaseId": leaseID,
		},
	}, nil
}

// ============================================================================
// EXECUTORS - TOKEN OPERATIONS
// ============================================================================

// TokenLookupExecutor handles vault-token-lookup
type TokenLookupExecutor struct{}

func (e *TokenLookupExecutor) Type() string { return "vault-token-lookup" }

func (e *TokenLookupExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	masterToken := resolver.ResolveString(getString(config, "token"))
	lookupToken := resolver.ResolveString(getString(config, "token"))
	self := getBool(config, "self", false)

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if masterToken == "" {
		return nil, fmt.Errorf("vault token is required")
	}

	client := NewVaultClient(address, masterToken)

	var respBody []byte
	var err error

	if self || lookupToken == "" {
		// Look up self
		respBody, _, err = client.doRequest(ctx, "GET", "auth/token/lookup-self", nil)
	} else {
		// Look up specific token
		body := map[string]interface{}{
			"token": lookupToken,
		}
		respBody, _, err = client.doRequest(ctx, "POST", "auth/token/lookup", body)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to lookup token: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if data, ok := result["data"].(map[string]interface{}); ok {
		output["tokenInfo"] = data
		if policies, ok := data["policies"].([]interface{}); ok {
			policyNames := make([]string, len(policies))
			for i, p := range policies {
				if s, ok := p.(string); ok {
					policyNames[i] = s
				}
			}
			output["policies"] = policyNames
		}
		if ttl, ok := data["ttl"].(float64); ok {
			output["ttl"] = int(ttl)
		}
		if expireTime, ok := data["expire_time"].(string); ok {
			output["expireTime"] = expireTime
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// TokenRenewExecutor handles vault-token-renew
type TokenRenewExecutor struct{}

func (e *TokenRenewExecutor) Type() string { return "vault-token-renew" }

func (e *TokenRenewExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	masterToken := resolver.ResolveString(getString(config, "token"))
	renewToken := resolver.ResolveString(getString(config, "token"))
	self := getBool(config, "self", true)
	increment := getInt(config, "increment", 0)

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if masterToken == "" {
		return nil, fmt.Errorf("vault token is required")
	}

	client := NewVaultClient(address, masterToken)

	body := map[string]interface{}{}
	if increment > 0 {
		body["increment"] = fmt.Sprintf("%ds", increment)
	}

	var respBody []byte
	var err error

	if self || renewToken == "" {
		// Renew self
		respBody, _, err = client.doRequest(ctx, "POST", "auth/token/renew-self", body)
	} else {
		// Renew specific token
		body["token"] = renewToken
		respBody, _, err = client.doRequest(ctx, "POST", "auth/token/renew", body)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to renew token: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if auth, ok := result["auth"].(map[string]interface{}); ok {
		if clientToken, ok := auth["client_token"].(string); ok {
			output["token"] = clientToken
		}
		if ttl, ok := auth["lease_duration"].(float64); ok {
			output["ttl"] = int(ttl)
		}
		if policies, ok := auth["policies"].([]interface{}); ok {
			policyNames := make([]string, len(policies))
			for i, p := range policies {
				if s, ok := p.(string); ok {
					policyNames[i] = s
				}
			}
			output["policies"] = policyNames
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// TokenCreateExecutor handles vault-token-create
type TokenCreateExecutor struct{}

func (e *TokenCreateExecutor) Type() string { return "vault-token-create" }

func (e *TokenCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}

	body := map[string]interface{}{}

	if policies := getString(config, "policies"); policies != "" {
		body["policies"] = strings.Split(policies, ",")
	}
	if ttl := getString(config, "ttl"); ttl != "" {
		body["ttl"] = ttl
	}
	if maxTtl := getString(config, "maxTtl"); maxTtl != "" {
		body["max_ttl"] = maxTtl
	}
	if displayName := getString(config, "displayName"); displayName != "" {
		body["display_name"] = displayName
	}
	if getBool(config, "renewable", true) {
		body["renewable"] = true
	}
	if getBool(config, "orphan", false) {
		body["orphan"] = true
	}

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "POST", "auth/token/create", body)
	if err != nil {
		return nil, fmt.Errorf("failed to create token: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if auth, ok := result["auth"].(map[string]interface{}); ok {
		if clientToken, ok := auth["client_token"].(string); ok {
			output["token"] = clientToken
		}
		if accessor, ok := auth["accessor"].(string); ok {
			output["accessor"] = accessor
		}
		if policies, ok := auth["policies"].([]interface{}); ok {
			policyNames := make([]string, len(policies))
			for i, p := range policies {
				if s, ok := p.(string); ok {
					policyNames[i] = s
				}
			}
			output["policies"] = policyNames
		}
		if ttl, ok := auth["lease_duration"].(float64); ok {
			output["ttl"] = int(ttl)
		}
		if entityID, ok := auth["entity_id"].(string); ok {
			output["entityId"] = entityID
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// TokenRevokeExecutor handles vault-token-revoke
type TokenRevokeExecutor struct{}

func (e *TokenRevokeExecutor) Type() string { return "vault-token-revoke" }

func (e *TokenRevokeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	masterToken := resolver.ResolveString(getString(config, "token"))
	revokeToken := resolver.ResolveString(getString(config, "token"))
	self := getBool(config, "self", false)
	orphan := getBool(config, "orphan", false)

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if masterToken == "" {
		return nil, fmt.Errorf("vault token is required")
	}

	client := NewVaultClient(address, masterToken)

	var endpoint string
	var body map[string]interface{}

	if self {
		endpoint = "auth/token/revoke-self"
	} else if orphan {
		endpoint = "auth/token/revoke-orphan"
		body = map[string]interface{}{
			"token": revokeToken,
		}
	} else {
		endpoint = "auth/token/revoke"
		body = map[string]interface{}{
			"token": revokeToken,
		}
	}

	var err error
	if body != nil {
		_, _, err = client.doRequest(ctx, "POST", endpoint, body)
	} else {
		_, _, err = client.doRequest(ctx, "POST", endpoint, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to revoke token: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
		},
	}, nil
}

// ============================================================================
// EXECUTORS - PKI OPERATIONS
// ============================================================================

// PkiIssueExecutor handles vault-pki-issue
type PkiIssueExecutor struct{}

func (e *PkiIssueExecutor) Type() string { return "vault-pki-issue" }

func (e *PkiIssueExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	mount := resolver.ResolveString(getString(config, "mount"))
	role := resolver.ResolveString(getString(config, "role"))
	commonName := resolver.ResolveString(getString(config, "commonName"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if role == "" {
		return nil, fmt.Errorf("role is required")
	}
	if commonName == "" {
		return nil, fmt.Errorf("commonName is required")
	}

	if mount == "" {
		mount = "pki"
	}

	body := map[string]interface{}{
		"common_name": commonName,
	}

	if altNames := getString(config, "altNames"); altNames != "" {
		body["alt_names"] = altNames
	}
	if ipSans := getString(config, "ipSans"); ipSans != "" {
		body["ip_sans"] = ipSans
	}
	if ttl := getString(config, "ttl"); ttl != "" {
		body["ttl"] = ttl
	}

	vaultPath := fmt.Sprintf("%s/issue/%s", mount, role)

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "POST", vaultPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to issue certificate: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if data, ok := result["data"].(map[string]interface{}); ok {
		if cert, ok := data["certificate"].(string); ok {
			output["certificate"] = cert
		}
		if key, ok := data["private_key"].(string); ok {
			output["privateKey"] = key
		}
		if caChain, ok := data["ca_chain"].([]interface{}); ok {
			caStrs := make([]string, len(caChain))
			for i, c := range caChain {
				if s, ok := c.(string); ok {
					caStrs[i] = s
				}
			}
			output["caChain"] = strings.Join(caStrs, "\n")
		}
		if serial, ok := data["serial_number"].(string); ok {
			output["serialNumber"] = serial
		}
		if expiry, ok := data["expiration"].(float64); ok {
			output["expiration"] = int64(expiry)
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// PkiRenewExecutor handles vault-pki-renew
type PkiRenewExecutor struct{}

func (e *PkiRenewExecutor) Type() string { return "vault-pki-renew" }

func (e *PkiRenewExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	mount := resolver.ResolveString(getString(config, "mount"))
	serial := resolver.ResolveString(getString(config, "serial"))
	ttl := resolver.ResolveString(getString(config, "ttl"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if serial == "" {
		return nil, fmt.Errorf("serial number is required")
	}

	if mount == "" {
		mount = "pki"
	}

	body := map[string]interface{}{}
	if ttl != "" {
		body["ttl"] = ttl
	}

	vaultPath := fmt.Sprintf("%s/renew/%s", mount, serial)

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "PUT", vaultPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to renew certificate: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if data, ok := result["data"].(map[string]interface{}); ok {
		output["certificate"] = data
	}

	return &executor.StepResult{Output: output}, nil
}

// PkiRevokeExecutor handles vault-pki-revoke
type PkiRevokeExecutor struct{}

func (e *PkiRevokeExecutor) Type() string { return "vault-pki-revoke" }

func (e *PkiRevokeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	mount := resolver.ResolveString(getString(config, "mount"))
	serial := resolver.ResolveString(getString(config, "serial"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if serial == "" {
		return nil, fmt.Errorf("serial number is required")
	}

	if mount == "" {
		mount = "pki"
	}

	body := map[string]interface{}{
		"serial_number": serial,
	}

	vaultPath := fmt.Sprintf("%s/revoke", mount)

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "POST", vaultPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to revoke certificate: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if data, ok := result["data"].(map[string]interface{}); ok {
		output["revocationTime"] = data["revocation_time"]
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// EXECUTORS - DATABASE OPERATIONS
// ============================================================================

// DatabaseCredsExecutor handles vault-database-creds
type DatabaseCredsExecutor struct{}

func (e *DatabaseCredsExecutor) Type() string { return "vault-database-creds" }

func (e *DatabaseCredsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	mount := resolver.ResolveString(getString(config, "mount"))
	role := resolver.ResolveString(getString(config, "role"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if role == "" {
		return nil, fmt.Errorf("role is required")
	}

	if mount == "" {
		mount = "database"
	}

	vaultPath := fmt.Sprintf("%s/creds/%s", mount, role)

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "GET", vaultPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get database credentials: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if data, ok := result["data"].(map[string]interface{}); ok {
		if username, ok := data["username"].(string); ok {
			output["username"] = username
		}
		if password, ok := data["password"].(string); ok {
			output["password"] = password
		}
		// Copy all data fields
		for k, v := range data {
			output[k] = v
		}
	}

	if leaseID, ok := result["lease_id"].(string); ok {
		output["leaseId"] = leaseID
	}
	if leaseDuration, ok := result["lease_duration"].(float64); ok {
		output["leaseDuration"] = int(leaseDuration)
	}
	if renewable, ok := result["renewable"].(bool); ok {
		output["renewable"] = renewable
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// EXECUTORS - TRANSIT OPERATIONS
// ============================================================================

// TransitEncryptExecutor handles vault-transit-encrypt
type TransitEncryptExecutor struct{}

func (e *TransitEncryptExecutor) Type() string { return "vault-transit-encrypt" }

func (e *TransitEncryptExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	mount := resolver.ResolveString(getString(config, "mount"))
	key := resolver.ResolveString(getString(config, "key"))
	plaintext := resolver.ResolveString(getString(config, "plaintext"))
	context := resolver.ResolveString(getString(config, "context"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if plaintext == "" {
		return nil, fmt.Errorf("plaintext is required")
	}

	if mount == "" {
		mount = "transit"
	}

	// Base64 encode the plaintext
	encoded := base64.StdEncoding.EncodeToString([]byte(plaintext))

	body := map[string]interface{}{
		"plaintext": encoded,
	}

	if context != "" {
		body["context"] = base64.StdEncoding.EncodeToString([]byte(context))
	}

	vaultPath := fmt.Sprintf("%s/encrypt/%s", mount, key)

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "POST", vaultPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if data, ok := result["data"].(map[string]interface{}); ok {
		if ciphertext, ok := data["ciphertext"].(string); ok {
			output["ciphertext"] = ciphertext
		}
		if keyVersion, ok := data["key_version"].(float64); ok {
			output["keyVersion"] = int(keyVersion)
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// TransitDecryptExecutor handles vault-transit-decrypt
type TransitDecryptExecutor struct{}

func (e *TransitDecryptExecutor) Type() string { return "vault-transit-decrypt" }

func (e *TransitDecryptExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))
	token := resolver.ResolveString(getString(config, "token"))
	mount := resolver.ResolveString(getString(config, "mount"))
	key := resolver.ResolveString(getString(config, "key"))
	ciphertext := resolver.ResolveString(getString(config, "ciphertext"))
	context := resolver.ResolveString(getString(config, "context"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if ciphertext == "" {
		return nil, fmt.Errorf("ciphertext is required")
	}

	if mount == "" {
		mount = "transit"
	}

	body := map[string]interface{}{
		"ciphertext": ciphertext,
	}

	if context != "" {
		body["context"] = base64.StdEncoding.EncodeToString([]byte(context))
	}

	vaultPath := fmt.Sprintf("%s/decrypt/%s", mount, key)

	client := NewVaultClient(address, token)
	respBody, _, err := client.doRequest(ctx, "POST", vaultPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": string(respBody),
	}

	if data, ok := result["data"].(map[string]interface{}); ok {
		if plaintext, ok := data["plaintext"].(string); ok {
			output["plaintext"] = plaintext
			// Decode from base64
			if decoded, err := base64.StdEncoding.DecodeString(plaintext); err == nil {
				output["plaintextDecoded"] = string(decoded)
			}
		}
		if keyVersion, ok := data["key_version"].(float64); ok {
			output["keyVersion"] = int(keyVersion)
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// EXECUTORS - HEALTH
// ============================================================================

// HealthExecutor handles vault-health
type HealthExecutor struct{}

func (e *HealthExecutor) Type() string { return "vault-health" }

func (e *HealthExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	address := resolver.ResolveString(getString(config, "address"))

	if address == "" {
		return nil, fmt.Errorf("vault address is required")
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	url := fmt.Sprintf("%s/v1/sys/health", strings.TrimSuffix(address, "/"))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{
				"raw":        string(respBody),
				"statusCode": resp.StatusCode,
				"healthy":    false,
			},
		}, nil
	}

	output := map[string]interface{}{
		"raw":        string(respBody),
		"statusCode": resp.StatusCode,
	}

	if initialized, ok := result["initialized"].(bool); ok {
		output["initialized"] = initialized
	}
	if sealed, ok := result["sealed"].(bool); ok {
		output["sealed"] = sealed
	}
	if standby, ok := result["standby"].(bool); ok {
		output["standby"] = standby
	}
	if performanceStandby, ok := result["performance_standby"].(bool); ok {
		output["performanceStandby"] = performanceStandby
	}
	if replicationPerformanceMode, ok := result["replication_performance_mode"].(string); ok {
		output["replicationPerformanceMode"] = replicationPerformanceMode
	}
	if replicationDrMode, ok := result["replication_dr_mode"].(string); ok {
		output["replicationDrMode"] = replicationDrMode
	}

	// Determine overall health
	output["healthy"] = resp.StatusCode == 200 && !output["sealed"].(bool)

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50072"
	}

	server := grpc.NewSkillServer("skill-vault", "1.0.0")

	// Register KV executors
	server.RegisterExecutorWithSchema("vault-kv-get", &KvGetExecutor{}, KvGetSchema)
	server.RegisterExecutorWithSchema("vault-kv-put", &KvPutExecutor{}, KvPutSchema)
	server.RegisterExecutorWithSchema("vault-kv-delete", &KvDeleteExecutor{}, KvDeleteSchema)

	// Register generic executors
	server.RegisterExecutorWithSchema("vault-read", &ReadExecutor{}, ReadSchema)
	server.RegisterExecutorWithSchema("vault-write", &WriteExecutor{}, WriteSchema)
	server.RegisterExecutorWithSchema("vault-delete", &DeleteExecutor{}, DeleteSchema)
	server.RegisterExecutorWithSchema("vault-list", &ListExecutor{}, ListSchema)

	// Register lease executors
	server.RegisterExecutorWithSchema("vault-lease-renew", &LeaseRenewExecutor{}, LeaseRenewSchema)
	server.RegisterExecutorWithSchema("vault-lease-revoke", &LeaseRevokeExecutor{}, LeaseRevokeSchema)

	// Register token executors
	server.RegisterExecutorWithSchema("vault-token-lookup", &TokenLookupExecutor{}, TokenLookupSchema)
	server.RegisterExecutorWithSchema("vault-token-renew", &TokenRenewExecutor{}, TokenRenewSchema)
	server.RegisterExecutorWithSchema("vault-token-create", &TokenCreateExecutor{}, TokenCreateSchema)
	server.RegisterExecutorWithSchema("vault-token-revoke", &TokenRevokeExecutor{}, TokenRevokeSchema)

	// Register PKI executors
	server.RegisterExecutorWithSchema("vault-pki-issue", &PkiIssueExecutor{}, PkiIssueSchema)
	server.RegisterExecutorWithSchema("vault-pki-renew", &PkiRenewExecutor{}, PkiRenewSchema)
	server.RegisterExecutorWithSchema("vault-pki-revoke", &PkiRevokeExecutor{}, PkiRevokeSchema)

	// Register database executors
	server.RegisterExecutorWithSchema("vault-database-creds", &DatabaseCredsExecutor{}, DatabaseCredsSchema)

	// Register transit executors
	server.RegisterExecutorWithSchema("vault-transit-encrypt", &TransitEncryptExecutor{}, TransitEncryptSchema)
	server.RegisterExecutorWithSchema("vault-transit-decrypt", &TransitDecryptExecutor{}, TransitDecryptSchema)

	// Register health
	server.RegisterExecutorWithSchema("vault-health", &HealthExecutor{}, HealthSchema)

	fmt.Printf("Starting skill-vault gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
