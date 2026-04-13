package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	_ "github.com/snowflakedb/gosnowflake"
)

// Snowflake connections cache
var (
	connections = make(map[string]*sql.DB)
	connMutex   sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50053"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-snowflake", "1.0.0")

	// Register Snowflake executors with schemas
	server.RegisterExecutorWithSchema("sf-query", &SFQueryExecutor{}, SFQuerySchema)
	server.RegisterExecutorWithSchema("sf-warehouse", &SFWarehouseExecutor{}, SFWarehouseSchema)
	server.RegisterExecutorWithSchema("sf-stage", &SFStageExecutor{}, SFStageSchema)
	server.RegisterExecutorWithSchema("sf-copy", &SFCopyExecutor{}, SFCopySchema)
	server.RegisterExecutorWithSchema("sf-stream", &SFStreamExecutor{}, SFStreamSchema)

	fmt.Printf("Starting skill-snowflake gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// buildDSN constructs a Snowflake DSN from connection parameters
func buildDSN(account, user, password, database, schema, warehouse, role string) (string, error) {
	dsn := fmt.Sprintf("%s:%s@%s", user, password, account)
	if database != "" {
		dsn += "/" + database
		if schema != "" {
			dsn += "/" + schema
		}
	}
	params := make([]string, 0)
	if warehouse != "" {
		params = append(params, fmt.Sprintf("warehouse=%s", warehouse))
	}
	if role != "" {
		params = append(params, fmt.Sprintf("role=%s", role))
	}
	if len(params) > 0 {
		dsn += "?"
		for i, p := range params {
			if i > 0 {
				dsn += "&"
			}
			dsn += p
		}
	}
	return dsn, nil
}

// getConnection returns a Snowflake database connection (cached)
func getConnection(dsn string) (*sql.DB, error) {
	connMutex.RLock()
	db, ok := connections[dsn]
	connMutex.RUnlock()

	if ok {
		return db, nil
	}

	connMutex.Lock()
	defer connMutex.Unlock()

	// Double check
	if db, ok := connections[dsn]; ok {
		return db, nil
	}

	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return nil, err
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, err
	}

	connections[dsn] = db
	return db, nil
}

// SFQueryExecutor handles sf-query node type
type SFQueryExecutor struct{}

func (e *SFQueryExecutor) Type() string { return "sf-query" }

// SFQueryConfig defines the typed configuration for sf-query
type SFQueryConfig struct {
	Account   string        `json:"account" description:"Snowflake account identifier (e.g., org-account)"`
	User      string        `json:"user" description:"Snowflake username"`
	Password  string        `json:"password" description:"Snowflake password (supports {{bindings.xxx}})"`
	Database  string        `json:"database" description:"Database name"`
	Schema    string        `json:"schema" description:"Schema name"`
	Warehouse string        `json:"warehouse" description:"Warehouse to use"`
	Role      string        `json:"role" description:"Role to use"`
	Query     string        `json:"query" description:"SQL query to execute"`
	Args      []interface{} `json:"args" description:"Query parameters"`
}

// SFQuerySchema is the UI schema for sf-query
var SFQuerySchema = resolver.NewSchemaBuilder("sf-query").
	WithName("Snowflake Query").
	WithCategory("data").
	WithIcon("database").
	WithDescription("Execute SQL queries against Snowflake data warehouse").
	AddSection("Connection").
		AddTextField("account", "Account",
			resolver.WithRequired(),
			resolver.WithPlaceholder("org-account"),
			resolver.WithHint("Your Snowflake account identifier"),
		).
		AddTextField("user", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("your_username"),
		).
		AddExpressionField("password", "Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("{{bindings.snowflake_password}}"),
			resolver.WithHint("Use bindings for secure credential access"),
		).
		AddTextField("database", "Database",
			resolver.WithPlaceholder("MY_DATABASE"),
		).
		AddTextField("schema", "Schema",
			resolver.WithPlaceholder("PUBLIC"),
		).
		AddTextField("warehouse", "Warehouse",
			resolver.WithPlaceholder("COMPUTE_WH"),
		).
		AddTextField("role", "Role",
			resolver.WithPlaceholder("ACCOUNTADMIN"),
		).
		EndSection().
	AddSection("Query").
		AddCodeField("query", "SQL Query", "sql",
			resolver.WithRequired(),
			resolver.WithHeight(150),
			resolver.WithPlaceholder("SELECT * FROM my_table WHERE id = ?"),
		).
		AddTagsField("args", "Parameters",
			resolver.WithHint("Query parameters for prepared statement"),
		).
		EndSection().
	Build()

func (e *SFQueryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg SFQueryConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		// Fallback for non-SDK resolver
		if err := resolver.ResolveConfig(step.Config, &cfg, templateResolver.(*resolver.Resolver)); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Account == "" || cfg.User == "" || cfg.Password == "" {
		return nil, fmt.Errorf("account, user, and password are required")
	}

	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	// Build DSN
	dsn, err := buildDSN(cfg.Account, cfg.User, cfg.Password, cfg.Database, cfg.Schema, cfg.Warehouse, cfg.Role)
	if err != nil {
		return nil, fmt.Errorf("failed to build connection string: %w", err)
	}

	// Get connection
	db, err := getConnection(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	// Execute query
	rows, err := db.QueryContext(ctx, cfg.Query, cfg.Args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// Get columns
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Scan results
	var results []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			// Convert []byte to string
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"rows":      results,
			"count":     len(results),
			"database":  cfg.Database,
			"schema":    cfg.Schema,
			"warehouse": cfg.Warehouse,
		},
	}, nil
}

// SFWarehouseExecutor handles sf-warehouse node type
type SFWarehouseExecutor struct{}

func (e *SFWarehouseExecutor) Type() string { return "sf-warehouse" }

// SFWarehouseConfig defines the typed configuration for sf-warehouse
type SFWarehouseConfig struct {
	Account      string `json:"account" description:"Snowflake account identifier"`
	User         string `json:"user" description:"Snowflake username"`
	Password     string `json:"password" description:"Snowflake password"`
	Database     string `json:"database" description:"Database name"`
	Warehouse    string `json:"warehouse" description:"Warehouse name"`
	Action       string `json:"action" description:"Action to perform: start, stop, resume, suspend, status"`
	Size         string `json:"size" description:"Warehouse size for resize (XSMALL, SMALL, MEDIUM, LARGE, XLARGE, etc.)"`
	AutoSuspend  int    `json:"autoSuspend" description:"Auto-suspend time in minutes"`
	AutoResume   bool   `json:"autoResume" description:"Enable auto-resume"`
}

// SFWarehouseSchema is the UI schema for sf-warehouse
var SFWarehouseSchema = resolver.NewSchemaBuilder("sf-warehouse").
	WithName("Snowflake Warehouse").
	WithCategory("data").
	WithIcon("server").
	WithDescription("Manage Snowflake warehouses (start, stop, resize)").
	AddSection("Connection").
		AddTextField("account", "Account",
			resolver.WithRequired(),
			resolver.WithPlaceholder("org-account"),
		).
		AddTextField("user", "Username",
			resolver.WithRequired(),
		).
		AddExpressionField("password", "Password",
			resolver.WithRequired(),
		).
		AddTextField("database", "Database",
			resolver.WithPlaceholder("MY_DATABASE"),
		).
		EndSection().
	AddSection("Warehouse").
		AddTextField("warehouse", "Warehouse Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("COMPUTE_WH"),
		).
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "Start / Resume", Value: "start"},
			{Label: "Stop / Suspend", Value: "stop"},
			{Label: "Status", Value: "status"},
			{Label: "Resize", Value: "resize"},
		}, resolver.WithRequired()).
		AddSelectField("size", "Size (for resize)", []resolver.SelectOption{
			{Label: "X-Small", Value: "XSMALL"},
			{Label: "Small", Value: "SMALL"},
			{Label: "Medium", Value: "MEDIUM"},
			{Label: "Large", Value: "LARGE"},
			{Label: "X-Large", Value: "XLARGE"},
			{Label: "2X-Large", Value: "XXLARGE"},
			{Label: "3X-Large", Value: "XXXLARGE"},
		}, resolver.WithPlaceholder("MEDIUM")).
		AddNumberField("autoSuspend", "Auto Suspend (minutes)",
			resolver.WithPlaceholder("5"),
		).
		AddToggleField("autoResume", "Auto Resume",
			resolver.WithDefault(true),
		).
		EndSection().
	Build()

func (e *SFWarehouseExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFWarehouseConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		if err := resolver.ResolveConfig(step.Config, &cfg, templateResolver.(*resolver.Resolver)); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Account == "" || cfg.User == "" || cfg.Password == "" {
		return nil, fmt.Errorf("account, user, and password are required")
	}

	if cfg.Warehouse == "" {
		return nil, fmt.Errorf("warehouse is required")
	}

	// Build DSN
	dsn, err := buildDSN(cfg.Account, cfg.User, cfg.Password, cfg.Database, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to build connection string: %w", err)
	}

	// Get connection
	db, err := getConnection(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	var query string
	var resultMsg string

	switch cfg.Action {
	case "start", "resume":
		query = fmt.Sprintf("ALTER WAREHOUSE %s RESUME", cfg.Warehouse)
		resultMsg = fmt.Sprintf("Warehouse %s resumed successfully", cfg.Warehouse)
	case "stop", "suspend":
		query = fmt.Sprintf("ALTER WAREHOUSE %s SUSPEND", cfg.Warehouse)
		resultMsg = fmt.Sprintf("Warehouse %s suspended successfully", cfg.Warehouse)
	case "status":
		query = fmt.Sprintf("SHOW WAREHOUSES LIKE '%s'", cfg.Warehouse)
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to get warehouse status: %w", err)
		}
		defer rows.Close()

		columns, _ := rows.Columns()
		var results []map[string]interface{}
		for rows.Next() {
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}
			rows.Scan(valuePtrs...)
			row := make(map[string]interface{})
			for i, col := range columns {
				if b, ok := values[i].([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = values[i]
				}
			}
			results = append(results, row)
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"warehouse": cfg.Warehouse,
				"status":    results,
				"action":    "status",
			},
		}, nil
	case "resize":
		if cfg.Size == "" {
			return nil, fmt.Errorf("size is required for resize action")
		}
		query = fmt.Sprintf("ALTER WAREHOUSE %s SET WAREHOUSE_SIZE = %s", cfg.Warehouse, cfg.Size)
		resultMsg = fmt.Sprintf("Warehouse %s resized to %s", cfg.Warehouse, cfg.Size)
	default:
		return nil, fmt.Errorf("invalid action: %s (must be start, stop, resume, suspend, status, or resize)", cfg.Action)
	}

	if query != "" {
		_, err = db.ExecContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("warehouse action failed: %w", err)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"warehouse":   cfg.Warehouse,
			"action":      cfg.Action,
			"message":     resultMsg,
			"success":     true,
		},
	}, nil
}

// SFStageExecutor handles sf-stage node type
type SFStageExecutor struct{}

func (e *SFStageExecutor) Type() string { return "sf-stage" }

// SFStageConfig defines the typed configuration for sf-stage
type SFStageConfig struct {
	Account     string            `json:"account" description:"Snowflake account identifier"`
	User        string            `json:"user" description:"Snowflake username"`
	Password    string            `json:"password" description:"Snowflake password"`
	Database    string            `json:"database" description:"Database name"`
	Schema      string            `json:"schema" description:"Schema name"`
	StageName   string            `json:"stageName" description:"Stage name"`
	Action      string            `json:"action" description:"Action: create, drop, list, describe"`
	StageType   string            `json:"stageType" description:"Stage type: internal, external, user, table"`
	URL         string            `json:"url" description:"External stage URL (for external stages)"`
	Credentials map[string]string `json:"credentials" description:"Credentials for external stage"`
	FileFormat  string            `json:"fileFormat" description:"File format name"`
}

// SFStageSchema is the UI schema for sf-stage
var SFStageSchema = resolver.NewSchemaBuilder("sf-stage").
	WithName("Snowflake Stage").
	WithCategory("data").
	WithIcon("folder").
	WithDescription("Manage Snowflake stages for data loading/unloading").
	AddSection("Connection").
		AddTextField("account", "Account",
			resolver.WithRequired(),
			resolver.WithPlaceholder("org-account"),
		).
		AddTextField("user", "Username",
			resolver.WithRequired(),
		).
		AddExpressionField("password", "Password",
			resolver.WithRequired(),
		).
		AddTextField("database", "Database",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_DATABASE"),
		).
		AddTextField("schema", "Schema",
			resolver.WithPlaceholder("PUBLIC"),
		).
		EndSection().
	AddSection("Stage").
		AddTextField("stageName", "Stage Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_STAGE"),
		).
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "Create Stage", Value: "create"},
			{Label: "Drop Stage", Value: "drop"},
			{Label: "List Files", Value: "list"},
			{Label: "Describe Stage", Value: "describe"},
		}, resolver.WithRequired()).
		AddSelectField("stageType", "Stage Type (for create)", []resolver.SelectOption{
			{Label: "Internal", Value: "internal"},
			{Label: "External", Value: "external"},
		}, resolver.WithDefault("internal")).
		AddExpressionField("url", "External URL",
			resolver.WithPlaceholder("s3://my-bucket/path/"),
			resolver.WithHint("Required for external stages"),
		).
		AddTextField("fileFormat", "File Format",
			resolver.WithPlaceholder("MY_CSV_FORMAT"),
		).
		EndSection().
	Build()

func (e *SFStageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFStageConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		if err := resolver.ResolveConfig(step.Config, &cfg, templateResolver.(*resolver.Resolver)); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Account == "" || cfg.User == "" || cfg.Password == "" {
		return nil, fmt.Errorf("account, user, and password are required")
	}

	if cfg.StageName == "" {
		return nil, fmt.Errorf("stageName is required")
	}

	// Build DSN
	dsn, err := buildDSN(cfg.Account, cfg.User, cfg.Password, cfg.Database, cfg.Schema, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to build connection string: %w", err)
	}

	// Get connection
	db, err := getConnection(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	var query string
	var resultMsg string

	switch cfg.Action {
	case "create":
		query = fmt.Sprintf("CREATE STAGE IF NOT EXISTS %s", cfg.StageName)
		if cfg.StageType == "external" && cfg.URL != "" {
			query += fmt.Sprintf(" URL = '%s'", cfg.URL)
		}
		if cfg.FileFormat != "" {
			query += fmt.Sprintf(" FILE_FORMAT = (FORMAT_NAME = %s)", cfg.FileFormat)
		}
		resultMsg = fmt.Sprintf("Stage %s created successfully", cfg.StageName)
	case "drop":
		query = fmt.Sprintf("DROP STAGE IF EXISTS %s", cfg.StageName)
		resultMsg = fmt.Sprintf("Stage %s dropped successfully", cfg.StageName)
	case "list":
		query = fmt.Sprintf("LIST @%s", cfg.StageName)
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to list stage files: %w", err)
		}
		defer rows.Close()

		columns, _ := rows.Columns()
		var results []map[string]interface{}
		for rows.Next() {
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}
			rows.Scan(valuePtrs...)
			row := make(map[string]interface{})
			for i, col := range columns {
				if b, ok := values[i].([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = values[i]
				}
			}
			results = append(results, row)
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"stage":  cfg.StageName,
				"files":  results,
				"count":  len(results),
				"action": "list",
			},
		}, nil
	case "describe":
		query = fmt.Sprintf("DESCRIBE STAGE %s", cfg.StageName)
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to describe stage: %w", err)
		}
		defer rows.Close()

		columns, _ := rows.Columns()
		var results []map[string]interface{}
		for rows.Next() {
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}
			rows.Scan(valuePtrs...)
			row := make(map[string]interface{})
			for i, col := range columns {
				if b, ok := values[i].([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = values[i]
				}
			}
			results = append(results, row)
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"stage":       cfg.StageName,
				"properties":  results,
				"action":      "describe",
			},
		}, nil
	default:
		return nil, fmt.Errorf("invalid action: %s", cfg.Action)
	}

	if query != "" {
		_, err = db.ExecContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("stage action failed: %w", err)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"stage":   cfg.StageName,
			"action":  cfg.Action,
			"message": resultMsg,
			"success": true,
		},
	}, nil
}

// SFCopyExecutor handles sf-copy node type
type SFCopyExecutor struct{}

func (e *SFCopyExecutor) Type() string { return "sf-copy" }

// SFCopyConfig defines the typed configuration for sf-copy
type SFCopyConfig struct {
	Account       string            `json:"account" description:"Snowflake account identifier"`
	User          string            `json:"user" description:"Snowflake username"`
	Password      string            `json:"password" description:"Snowflake password"`
	Database      string            `json:"database" description:"Database name"`
	Schema        string            `json:"schema" description:"Schema name"`
	Warehouse     string            `json:"warehouse" description:"Warehouse to use"`
	Direction     string            `json:"direction" description:"Direction: into (load) or into_table (load) or from_table (unload)"`
	TableName     string            `json:"tableName" description:"Target/source table name"`
	StageName     string            `json:"stageName" description:"Stage name"`
	FilePath      string            `json:"filePath" description:"File path in stage (supports patterns)"`
	FileFormat    string            `json:"fileFormat" description:"File format name or type"`
	CopyOptions   map[string]string `json:"copyOptions" description:"COPY INTO options"`
	OnError       string            `json:"onError" description:"Error handling: continue, skip_file, abort_statement"`
	ValidationMode string           `json:"validationMode" description:"Validation mode: RETURN_n_ROWS, RETURN_ERRORS, RETURN_ALL_ERRORS"`
}

// SFCopySchema is the UI schema for sf-copy
var SFCopySchema = resolver.NewSchemaBuilder("sf-copy").
	WithName("Snowflake COPY INTO").
	WithCategory("data").
	WithIcon("upload").
	WithDescription("Load or unload data using Snowflake COPY INTO command").
	AddSection("Connection").
		AddTextField("account", "Account",
			resolver.WithRequired(),
			resolver.WithPlaceholder("org-account"),
		).
		AddTextField("user", "Username",
			resolver.WithRequired(),
		).
		AddExpressionField("password", "Password",
			resolver.WithRequired(),
		).
		AddTextField("database", "Database",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_DATABASE"),
		).
		AddTextField("schema", "Schema",
			resolver.WithPlaceholder("PUBLIC"),
		).
		AddTextField("warehouse", "Warehouse",
			resolver.WithPlaceholder("COMPUTE_WH"),
		).
		EndSection().
	AddSection("Copy Operation").
		AddSelectField("direction", "Direction", []resolver.SelectOption{
			{Label: "Load into Table", Value: "into_table"},
			{Label: "Unload from Table", Value: "from_table"},
		}, resolver.WithRequired()).
		AddTextField("tableName", "Table Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_TABLE"),
		).
		AddTextField("stageName", "Stage Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_STAGE"),
		).
		AddTextField("filePath", "File Path",
			resolver.WithPlaceholder("data/*.csv"),
			resolver.WithHint("File path or pattern in stage"),
		).
		EndSection().
	AddSection("Options").
		AddTextField("fileFormat", "File Format",
			resolver.WithPlaceholder("TYPE = CSV FIELD_DELIMITER = ',' SKIP_HEADER = 1"),
		).
		AddSelectField("onError", "On Error", []resolver.SelectOption{
			{Label: "Continue", Value: "CONTINUE"},
			{Label: "Skip File", Value: "SKIP_FILE"},
			{Label: "Abort Statement", Value: "ABORT_STATEMENT"},
		}, resolver.WithDefault("ABORT_STATEMENT")).
		AddTextField("validationMode", "Validation Mode",
			resolver.WithPlaceholder("RETURN_ALL_ERRORS"),
		).
		EndSection().
	Build()

func (e *SFCopyExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFCopyConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		if err := resolver.ResolveConfig(step.Config, &cfg, templateResolver.(*resolver.Resolver)); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Account == "" || cfg.User == "" || cfg.Password == "" {
		return nil, fmt.Errorf("account, user, and password are required")
	}

	if cfg.TableName == "" || cfg.StageName == "" {
		return nil, fmt.Errorf("tableName and stageName are required")
	}

	// Build DSN
	dsn, err := buildDSN(cfg.Account, cfg.User, cfg.Password, cfg.Database, cfg.Schema, cfg.Warehouse, "")
	if err != nil {
		return nil, fmt.Errorf("failed to build connection string: %w", err)
	}

	// Get connection
	db, err := getConnection(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	var query string

	switch cfg.Direction {
	case "into_table", "into":
		// COPY INTO table FROM stage
		query = fmt.Sprintf("COPY INTO %s FROM @%s", cfg.TableName, cfg.StageName)
		if cfg.FilePath != "" {
			query += fmt.Sprintf("/%s", cfg.FilePath)
		}
		if cfg.FileFormat != "" {
			query += fmt.Sprintf(" FILE_FORMAT = (%s)", cfg.FileFormat)
		}
		if cfg.OnError != "" {
			query += fmt.Sprintf(" ON_ERROR = '%s'", cfg.OnError)
		}
		if cfg.ValidationMode != "" {
			query += fmt.Sprintf(" VALIDATION_MODE = %s", cfg.ValidationMode)
		}
	case "from_table":
		// COPY INTO stage FROM table
		query = fmt.Sprintf("COPY INTO @%s FROM %s", cfg.StageName, cfg.TableName)
		if cfg.FilePath != "" {
			query = fmt.Sprintf("COPY INTO @%s/%s FROM %s", cfg.StageName, cfg.FilePath, cfg.TableName)
		}
		if cfg.FileFormat != "" {
			query += fmt.Sprintf(" FILE_FORMAT = (%s)", cfg.FileFormat)
		}
	default:
		return nil, fmt.Errorf("invalid direction: %s (must be into_table or from_table)", cfg.Direction)
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("copy operation failed: %w", err)
	}
	defer rows.Close()

	// Get results
	columns, _ := rows.Columns()
	var results []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		rows.Scan(valuePtrs...)
		row := make(map[string]interface{})
		for i, col := range columns {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		results = append(results, row)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"table":     cfg.TableName,
			"stage":     cfg.StageName,
			"direction": cfg.Direction,
			"results":   results,
			"success":   true,
		},
	}, nil
}

// SFStreamExecutor handles sf-stream node type
type SFStreamExecutor struct{}

func (e *SFStreamExecutor) Type() string { return "sf-stream" }

// SFStreamConfig defines the typed configuration for sf-stream
type SFStreamConfig struct {
	Account     string `json:"account" description:"Snowflake account identifier"`
	User        string `json:"user" description:"Snowflake username"`
	Password    string `json:"password" description:"Snowflake password"`
	Database    string `json:"database" description:"Database name"`
	Schema      string `json:"schema" description:"Schema name"`
	StreamName  string `json:"streamName" description:"Stream name"`
	TableName   string `json:"tableName" description:"Source table name for stream"`
	Action      string `json:"action" description:"Action: create, drop, describe, consume"`
	AppendOnly  bool   `json:"appendOnly" description:"Create append-only stream"`
	ShowInitial bool   `json:"showInitial" description:"Show initial rows in stream"`
}

// SFStreamSchema is the UI schema for sf-stream
var SFStreamSchema = resolver.NewSchemaBuilder("sf-stream").
	WithName("Snowflake Stream").
	WithCategory("data").
	WithIcon("activity").
	WithDescription("Manage Snowflake streams for change data capture").
	AddSection("Connection").
		AddTextField("account", "Account",
			resolver.WithRequired(),
			resolver.WithPlaceholder("org-account"),
		).
		AddTextField("user", "Username",
			resolver.WithRequired(),
		).
		AddExpressionField("password", "Password",
			resolver.WithRequired(),
		).
		AddTextField("database", "Database",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_DATABASE"),
		).
		AddTextField("schema", "Schema",
			resolver.WithPlaceholder("PUBLIC"),
		).
		EndSection().
	AddSection("Stream").
		AddTextField("streamName", "Stream Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_STREAM"),
		).
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "Create Stream", Value: "create"},
			{Label: "Drop Stream", Value: "drop"},
			{Label: "Describe Stream", Value: "describe"},
			{Label: "Consume Stream", Value: "consume"},
		}, resolver.WithRequired()).
		AddTextField("tableName", "Source Table (for create)",
			resolver.WithPlaceholder("MY_TABLE"),
			resolver.WithHint("Required when creating a stream"),
		).
		AddToggleField("appendOnly", "Append Only",
			resolver.WithHint("Create an append-only stream"),
		).
		AddToggleField("showInitial", "Show Initial",
			resolver.WithHint("Show initial rows when stream is created"),
		).
		EndSection().
	Build()

func (e *SFStreamExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SFStreamConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		if err := resolver.ResolveConfig(step.Config, &cfg, templateResolver.(*resolver.Resolver)); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Account == "" || cfg.User == "" || cfg.Password == "" {
		return nil, fmt.Errorf("account, user, and password are required")
	}

	if cfg.StreamName == "" {
		return nil, fmt.Errorf("streamName is required")
	}

	// Build DSN
	dsn, err := buildDSN(cfg.Account, cfg.User, cfg.Password, cfg.Database, cfg.Schema, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to build connection string: %w", err)
	}

	// Get connection
	db, err := getConnection(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	var query string
	var resultMsg string

	switch cfg.Action {
	case "create":
		if cfg.TableName == "" {
			return nil, fmt.Errorf("tableName is required for creating a stream")
		}
		query = fmt.Sprintf("CREATE STREAM IF NOT EXISTS %s ON TABLE %s", cfg.StreamName, cfg.TableName)
		if cfg.AppendOnly {
			query += " APPEND_ONLY = TRUE"
		}
		if cfg.ShowInitial {
			query += " SHOW_INITIAL_ROWS = TRUE"
		}
		resultMsg = fmt.Sprintf("Stream %s created successfully on table %s", cfg.StreamName, cfg.TableName)
	case "drop":
		query = fmt.Sprintf("DROP STREAM IF EXISTS %s", cfg.StreamName)
		resultMsg = fmt.Sprintf("Stream %s dropped successfully", cfg.StreamName)
	case "describe":
		query = fmt.Sprintf("DESCRIBE STREAM %s", cfg.StreamName)
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to describe stream: %w", err)
		}
		defer rows.Close()

		columns, _ := rows.Columns()
		var results []map[string]interface{}
		for rows.Next() {
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}
			rows.Scan(valuePtrs...)
			row := make(map[string]interface{})
			for i, col := range columns {
				if b, ok := values[i].([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = values[i]
				}
			}
			results = append(results, row)
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"stream":     cfg.StreamName,
				"properties": results,
				"action":     "describe",
			},
		}, nil
	case "consume":
		// Consume stream by selecting from it
		query = fmt.Sprintf("SELECT * FROM %s", cfg.StreamName)
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to consume stream: %w", err)
		}
		defer rows.Close()

		columns, _ := rows.Columns()
		var results []map[string]interface{}
		for rows.Next() {
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}
			rows.Scan(valuePtrs...)
			row := make(map[string]interface{})
			for i, col := range columns {
				if b, ok := values[i].([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = values[i]
				}
			}
			results = append(results, row)
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"stream":  cfg.StreamName,
				"rows":    results,
				"count":   len(results),
				"action":  "consume",
			},
		}, nil
	default:
		return nil, fmt.Errorf("invalid action: %s", cfg.Action)
	}

	if query != "" {
		_, err = db.ExecContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("stream action failed: %w", err)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"stream":   cfg.StreamName,
			"action":   cfg.Action,
			"message":  resultMsg,
			"success":  true,
		},
	}, nil
}