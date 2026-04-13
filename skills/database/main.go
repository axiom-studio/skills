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
	_ "github.com/go-sql-driver/mysql" // MySQL driver
	_ "github.com/lib/pq"              // PostgreSQL driver
)

// Database connections cache
var (
	connections = make(map[string]*sql.DB)
	connMutex   sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50052"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-database", "1.0.0")

	// Register database executors with schemas
	server.RegisterExecutorWithSchema("db-query", &DBQueryExecutor{}, DBQuerySchema)
	server.RegisterExecutorWithSchema("db-insert", &DBInsertExecutor{}, DBInsertSchema)
	server.RegisterExecutorWithSchema("db-update", &DBUpdateExecutor{}, DBUpdateSchema)
	server.RegisterExecutorWithSchema("db-delete", &DBDeleteExecutor{}, DBDeleteSchema)
	server.RegisterExecutorWithSchema("db-transaction", &DBTransactionExecutor{}, DBTransactionSchema)

	fmt.Printf("Starting skill-database gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getConnection returns a database connection (cached)
func getConnection(connStr, driver string) (*sql.DB, error) {
	key := fmt.Sprintf("%s:%s", driver, connStr)
	
	connMutex.RLock()
	db, ok := connections[key]
	connMutex.RUnlock()
	
	if ok {
		return db, nil
	}
	
	connMutex.Lock()
	defer connMutex.Unlock()
	
	// Double check
	if db, ok := connections[key]; ok {
		return db, nil
	}
	
	db, err := sql.Open(driver, connStr)
	if err != nil {
		return nil, err
	}
	
	// Test connection
	if err := db.Ping(); err != nil {
		return nil, err
	}
	
	connections[key] = db
	return db, nil
}

// DBQueryExecutor handles db-query node type
type DBQueryExecutor struct{}

func (e *DBQueryExecutor) Type() string { return "db-query" }

// DBQueryConfig defines the typed configuration for db-query
type DBQueryConfig struct {
	ConnectionString string `json:"connectionString" description:"Database connection string, supports {{bindings.xxx}}"`
	Driver           string `json:"driver" default:"postgres" options:"PostgreSQL:postgres,MySQL:mysql" description:"Database driver"`
	Query            string `json:"query" description:"SQL query to execute"`
	Args             []interface{} `json:"args" description:"Query parameters"`
}

// DBQuerySchema is the UI schema for db-query
var DBQuerySchema = resolver.NewSchemaBuilder("db-query").
	WithName("Database Query").
	WithCategory("database").
	WithIcon("database").
	WithDescription("Execute SQL queries against a database").
	AddSection("Connection").
		AddExpressionField("connectionString", "Connection String",
			resolver.WithRequired(),
			resolver.WithPlaceholder("postgresql://user:pass@host:5432/db"),
			resolver.WithHint("Supports {{bindings.xxx}} for secure credential access"),
		).
		AddSelectField("driver", "Driver", []resolver.SelectOption{
			{Label: "PostgreSQL", Value: "postgres", Icon: "database"},
			{Label: "MySQL", Value: "mysql", Icon: "database"},
		}, resolver.WithDefault("postgres")).
		EndSection().
	AddSection("Query").
		AddCodeField("query", "SQL Query", "sql",
			resolver.WithRequired(),
			resolver.WithHeight(150),
			resolver.WithPlaceholder("SELECT * FROM users WHERE id = $1"),
		).
		AddTagsField("args", "Parameters",
			resolver.WithHint("Query parameters for prepared statement"),
		).
		EndSection().
	Build()

func (e *DBQueryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg DBQueryConfig
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

	if cfg.ConnectionString == "" {
		return nil, fmt.Errorf("connectionString is required")
	}

	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	// Get connection
	db, err := getConnection(cfg.ConnectionString, cfg.Driver)
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
			"rows":   results,
			"count":  len(results),
			"driver": cfg.Driver,
		},
	}, nil
}

// DBInsertExecutor handles db-insert node type
type DBInsertExecutor struct{}

// DBInsertConfig defines the typed configuration for db-insert
type DBInsertConfig struct {
	ConnectionString string                 `json:"connectionString" description:"Database connection string"`
	Driver           string                 `json:"driver" default:"postgres" description:"Database driver"`
	Table            string                 `json:"table" description:"Table name to insert into"`
	Data             map[string]interface{} `json:"data" description:"Column-value pairs to insert"`
}

// DBInsertSchema is the UI schema for db-insert
var DBInsertSchema = resolver.NewSchemaBuilder("db-insert").
	WithName("Database Insert").
	WithCategory("database").
	WithIcon("plus-circle").
	WithDescription("Insert rows into a database table").
	AddSection("Connection").
		AddExpressionField("connectionString", "Connection String",
			resolver.WithRequired(),
			resolver.WithPlaceholder("postgresql://user:pass@host:5432/db"),
		).
		AddSelectField("driver", "Driver", []resolver.SelectOption{
			{Label: "PostgreSQL", Value: "postgres"},
			{Label: "MySQL", Value: "mysql"},
		}, resolver.WithDefault("postgres")).
		EndSection().
	AddSection("Data").
		AddTextField("table", "Table Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("users"),
		).
		AddJSONField("data", "Row Data",
			resolver.WithRequired(),
			resolver.WithHeight(150),
			resolver.WithHint("JSON object with column-value pairs"),
		).
		EndSection().
	Build()

func (e *DBInsertExecutor) Type() string { return "db-insert" }

func (e *DBInsertExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	connStr, _ := step.Config["connectionString"].(string)
	driver, _ := step.Config["driver"].(string)
	if driver == "" {
		driver = "postgres"
	}
	table, _ := step.Config["table"].(string)
	data, _ := step.Config["data"].(map[string]interface{})
	
	if table == "" || len(data) == 0 {
		return nil, fmt.Errorf("table and data are required")
	}
	
	db, err := getConnection(connStr, driver)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	
	// Build insert query
	columns := make([]string, 0, len(data))
	placeholders := make([]string, 0, len(data))
	values := make([]interface{}, 0, len(data))
	i := 1
	for col, val := range data {
		columns = append(columns, col)
		if driver == "postgres" {
			placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		} else {
			placeholders = append(placeholders, "?")
		}
		values = append(values, val)
		i++
	}
	
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", 
		table, 
		joinStrings(columns, ", "),
		joinStrings(placeholders, ", "))
	
	result, err := db.ExecContext(ctx, query, values...)
	if err != nil {
		return nil, fmt.Errorf("insert failed: %w", err)
	}
	
	rowsAffected, _ := result.RowsAffected()
	lastInsertID, _ := result.LastInsertId()
	
	return &executor.StepResult{
		Output: map[string]interface{}{
			"rowsAffected": rowsAffected,
			"lastInsertId": lastInsertID,
			"table":        table,
		},
	}, nil
}

// DBUpdateExecutor handles db-update node type
type DBUpdateExecutor struct{}

// DBUpdateConfig defines the typed configuration for db-update
type DBUpdateConfig struct {
	ConnectionString string                 `json:"connectionString" description:"Database connection string"`
	Driver           string                 `json:"driver" default:"postgres" description:"Database driver"`
	Table            string                 `json:"table" description:"Table name to update"`
	Data             map[string]interface{} `json:"data" description:"Column-value pairs to set"`
	Where            string                 `json:"where" description:"WHERE clause (without WHERE keyword)"`
	WhereArgs        []interface{}          `json:"whereArgs" description:"Arguments for WHERE clause placeholders"`
}

// DBUpdateSchema is the UI schema for db-update
var DBUpdateSchema = resolver.NewSchemaBuilder("db-update").
	WithName("Database Update").
	WithCategory("database").
	WithIcon("edit").
	WithDescription("Update rows in a database table").
	AddSection("Connection").
		AddExpressionField("connectionString", "Connection String",
			resolver.WithRequired(),
		).
		AddSelectField("driver", "Driver", []resolver.SelectOption{
			{Label: "PostgreSQL", Value: "postgres"},
			{Label: "MySQL", Value: "mysql"},
		}, resolver.WithDefault("postgres")).
		EndSection().
	AddSection("Update").
		AddTextField("table", "Table Name",
			resolver.WithRequired(),
		).
		AddJSONField("data", "Update Data",
			resolver.WithRequired(),
			resolver.WithHeight(100),
		).
		AddTextareaField("where", "WHERE Clause",
			resolver.WithRequired(),
			resolver.WithRows(2),
			resolver.WithPlaceholder("id = $1 AND status = $2"),
		).
		AddTagsField("whereArgs", "WHERE Parameters").
		EndSection().
	Build()

func (e *DBUpdateExecutor) Type() string { return "db-update" }

func (e *DBUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	connStr, _ := step.Config["connectionString"].(string)
	driver, _ := step.Config["driver"].(string)
	if driver == "" {
		driver = "postgres"
	}
	table, _ := step.Config["table"].(string)
	data, _ := step.Config["data"].(map[string]interface{})
	where, _ := step.Config["where"].(string)
	whereArgs, _ := step.Config["whereArgs"].([]interface{})
	
	if table == "" || len(data) == 0 || where == "" {
		return nil, fmt.Errorf("table, data, and where are required")
	}
	
	db, err := getConnection(connStr, driver)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	
	// Build update query
	sets := make([]string, 0, len(data))
	values := make([]interface{}, 0, len(data)+len(whereArgs))
	i := 1
	for col, val := range data {
		if driver == "postgres" {
			sets = append(sets, fmt.Sprintf("%s = $%d", col, i))
		} else {
			sets = append(sets, fmt.Sprintf("%s = ?", col))
		}
		values = append(values, val)
		i++
	}
	values = append(values, whereArgs...)
	
	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s", 
		table, 
		joinStrings(sets, ", "),
		where)
	
	result, err := db.ExecContext(ctx, query, values...)
	if err != nil {
		return nil, fmt.Errorf("update failed: %w", err)
	}
	
	rowsAffected, _ := result.RowsAffected()
	
	return &executor.StepResult{
		Output: map[string]interface{}{
			"rowsAffected": rowsAffected,
			"table":        table,
		},
	}, nil
}

// DBDeleteExecutor handles db-delete node type
type DBDeleteExecutor struct{}

// DBDeleteConfig defines the typed configuration for db-delete
type DBDeleteConfig struct {
	ConnectionString string        `json:"connectionString" description:"Database connection string"`
	Driver           string        `json:"driver" default:"postgres" description:"Database driver"`
	Table            string        `json:"table" description:"Table name to delete from"`
	Where            string        `json:"where" description:"WHERE clause (without WHERE keyword)"`
	WhereArgs        []interface{} `json:"whereArgs" description:"Arguments for WHERE clause placeholders"`
}

// DBDeleteSchema is the UI schema for db-delete
var DBDeleteSchema = resolver.NewSchemaBuilder("db-delete").
	WithName("Database Delete").
	WithCategory("database").
	WithIcon("trash").
	WithDescription("Delete rows from a database table").
	AddSection("Connection").
		AddExpressionField("connectionString", "Connection String",
			resolver.WithRequired(),
		).
		AddSelectField("driver", "Driver", []resolver.SelectOption{
			{Label: "PostgreSQL", Value: "postgres"},
			{Label: "MySQL", Value: "mysql"},
		}, resolver.WithDefault("postgres")).
		EndSection().
	AddSection("Delete").
		AddTextField("table", "Table Name",
			resolver.WithRequired(),
		).
		AddTextareaField("where", "WHERE Clause",
			resolver.WithRequired(),
			resolver.WithRows(2),
			resolver.WithHint("Required for safety - omitting will delete all rows!"),
		).
		AddTagsField("whereArgs", "WHERE Parameters").
		EndSection().
	Build()

func (e *DBDeleteExecutor) Type() string { return "db-delete" }

func (e *DBDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	connStr, _ := step.Config["connectionString"].(string)
	driver, _ := step.Config["driver"].(string)
	if driver == "" {
		driver = "postgres"
	}
	table, _ := step.Config["table"].(string)
	where, _ := step.Config["where"].(string)
	whereArgs, _ := step.Config["whereArgs"].([]interface{})
	
	if table == "" || where == "" {
		return nil, fmt.Errorf("table and where are required")
	}
	
	db, err := getConnection(connStr, driver)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	
	query := fmt.Sprintf("DELETE FROM %s WHERE %s", table, where)
	
	result, err := db.ExecContext(ctx, query, whereArgs...)
	if err != nil {
		return nil, fmt.Errorf("delete failed: %w", err)
	}
	
	rowsAffected, _ := result.RowsAffected()
	
	return &executor.StepResult{
		Output: map[string]interface{}{
			"rowsAffected": rowsAffected,
			"table":        table,
		},
	}, nil
}

// DBTransactionExecutor handles db-transaction node type
type DBTransactionExecutor struct{}

// DBTransactionConfig defines the typed configuration for db-transaction
type DBTransactionConfig struct {
	ConnectionString string                   `json:"connectionString" description:"Database connection string"`
	Driver           string                   `json:"driver" default:"postgres" description:"Database driver"`
	Steps            []map[string]interface{} `json:"steps" description:"Array of SQL steps to execute in transaction"`
}

// DBTransactionSchema is the UI schema for db-transaction
var DBTransactionSchema = resolver.NewSchemaBuilder("db-transaction").
	WithName("Database Transaction").
	WithCategory("database").
	WithIcon("layers").
	WithDescription("Execute multiple SQL statements in a transaction").
	AddSection("Connection").
		AddExpressionField("connectionString", "Connection String",
			resolver.WithRequired(),
		).
		AddSelectField("driver", "Driver", []resolver.SelectOption{
			{Label: "PostgreSQL", Value: "postgres"},
			{Label: "MySQL", Value: "mysql"},
		}, resolver.WithDefault("postgres")).
		EndSection().
	AddSection("Transaction Steps").
		AddJSONField("steps", "Steps",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint("Array of {query, args} objects to execute in order"),
		).
		EndSection().
	Build()

func (e *DBTransactionExecutor) Type() string { return "db-transaction" }

func (e *DBTransactionExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	// TODO: Implement transaction support with multiple statements
	return &executor.StepResult{
		Output: map[string]interface{}{
			"message": "transaction not yet implemented",
		},
	}, nil
}

func joinStrings(s []string, sep string) string {
	result := ""
	for i, str := range s {
		if i > 0 {
			result += sep
		}
		result += str
	}
	return result
}