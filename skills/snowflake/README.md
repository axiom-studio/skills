# Snowflake Skill

A gRPC-based skill for Snowflake data warehouse operations in Axiom Studio.

## Overview

This skill provides comprehensive Snowflake data warehouse operations including:

- **sf-query**: Execute SQL queries against Snowflake
- **sf-warehouse**: Manage warehouses (start, stop, resize, status)
- **sf-stage**: Manage stages for data loading/unloading
- **sf-copy**: COPY INTO operations for bulk data movement
- **sf-stream**: Stream operations for change data capture

## Node Types

### sf-query

Execute SQL queries against Snowflake data warehouse.

**Configuration:**
- `account`: Snowflake account identifier (e.g., org-account)
- `user`: Snowflake username
- `password`: Snowflake password (supports secure bindings)
- `database`: Database name
- `schema`: Schema name
- `warehouse`: Warehouse to use
- `role`: Role to use
- `query`: SQL query to execute
- `args`: Query parameters

### sf-warehouse

Manage Snowflake warehouses.

**Configuration:**
- `account`, `user`, `password`, `database`: Connection settings
- `warehouse`: Warehouse name
- `action`: start, stop, resume, suspend, status, resize
- `size`: Warehouse size for resize (XSMALL to XXXLARGE)
- `autoSuspend`: Auto-suspend time in minutes
- `autoResume`: Enable auto-resume

### sf-stage

Manage Snowflake stages for data loading/unloading.

**Configuration:**
- `account`, `user`, `password`, `database`, `schema`: Connection settings
- `stageName`: Stage name
- `action`: create, drop, list, describe
- `stageType`: internal or external
- `url`: External stage URL (for external stages)
- `fileFormat`: File format name

### sf-copy

Load or unload data using Snowflake COPY INTO command.

**Configuration:**
- `account`, `user`, `password`, `database`, `schema`, `warehouse`: Connection settings
- `direction`: into_table (load) or from_table (unload)
- `tableName`: Target/source table name
- `stageName`: Stage name
- `filePath`: File path in stage
- `fileFormat`: File format specification
- `onError`: Error handling (CONTINUE, SKIP_FILE, ABORT_STATEMENT)
- `validationMode`: Validation mode

### sf-stream

Manage Snowflake streams for change data capture.

**Configuration:**
- `account`, `user`, `password`, `database`, `schema`: Connection settings
- `streamName`: Stream name
- `action`: create, drop, describe, consume
- `tableName`: Source table name (for create)
- `appendOnly`: Create append-only stream
- `showInitial`: Show initial rows

## Building

```bash
go build -o skill-snowflake .
```

## Running

```bash
./skill-snowflake
```

The server will start on port 50053 by default, or the port specified by the `SKILL_PORT` environment variable.

## Dependencies

- github.com/axiom-studio/skills.sdk
- github.com/snowflakedb/gosnowflake

## License

MIT