// Package executor defines the interfaces for step executors in Axiom skills.
// Skills implement StepExecutor to provide custom node types.
package executor

import "context"

// StepDefinition defines a single step in an agent pipeline
type StepDefinition struct {
	Id     string                 `json:"id"` // Node ID for graph-aware executors
	Name   string                 `json:"name"`
	Type   string                 `json:"type"`
	Config map[string]interface{} `json:"config"`
}

// StepResult represents the output of a step execution
type StepResult struct {
	Output   map[string]interface{}
	NextStep string // For branching steps (if, switch) - empty means continue to next
	Error    error
}

// StreamUpdate represents a partial output from a streaming executor
type StreamUpdate struct {
	Partial  interface{} // The incremental chunk of data
	Progress float64     // 0.0-1.0 progress indicator
}

// StreamCallback is called by streaming executors to emit partial outputs
type StreamCallback func(update *StreamUpdate)

// StepExecutor defines the interface for executing a single step type
// Skills must implement this interface for each node type they provide.
type StepExecutor interface {
	// Execute runs the step with the given config and returns the result
	Execute(ctx context.Context, step *StepDefinition, resolver TemplateResolver) (*StepResult, error)

	// Type returns the step type this executor handles
	Type() string
}

// StreamingExecutor is an optional interface for executors that support streaming partial outputs
type StreamingExecutor interface {
	StepExecutor

	// ExecuteStreaming runs the step and calls the callback for each partial output
	// The final result is still returned via StepResult
	ExecuteStreaming(ctx context.Context, step *StepDefinition, resolver TemplateResolver, onStream StreamCallback) (*StepResult, error)

	// SupportsStreaming returns true if this executor can stream with the given config
	SupportsStreaming(config map[string]interface{}) bool
}

// TemplateResolver interface for resolving {{}} templates
// Provided by Axiom at runtime to skills.
// See github.com/axiom-studio/skills.sdk/resolver for a standard implementation.
type TemplateResolver interface {
	ResolveString(template string) string
	ResolveMap(input map[string]interface{}) map[string]interface{}
	EvaluateCondition(condition string) bool
	SetVariable(name string, value interface{})
	GetStepOutput(stepName string) interface{}
	SetStepOutput(stepName string, output interface{})
}

// BindingResolver is an optional interface for accessing bindings directly
// Skills can use this to get connection strings, API keys, etc.
type BindingResolver interface {
	GetBinding(name string) interface{}
	GetBindings() map[string]interface{}
}

// ContextProvider provides access to execution context data for code execution
// Executors that need raw context (like code executor) should check if the resolver implements this
type ContextProvider interface {
	GetContextData() map[string]interface{}
}

// GraphProvider provides access to the execution graph for executors that need it
// Executors like AI can use this to discover connected tool nodes
type GraphProvider interface {
	GetGraph() *ExecutionGraph
}

// ExecutionGraph represents the agent execution graph
type ExecutionGraph struct {
	Nodes     map[string]*GraphNode
	Edges     []*GraphEdge
	StartNode string
}

// GetConnectedTools returns tool nodes connected to a specific node
// This is used by AI and code executors to discover available tools
func (g *ExecutionGraph) GetConnectedTools(nodeID string) []*GraphNode {
	var tools []*GraphNode
	for _, edge := range g.Edges {
		if edge.Source == nodeID {
			if target, ok := g.Nodes[edge.Target]; ok {
				// Check if target is a tool node (starts with "tool_")
				if target.Type == "tool_pgvector" || target.Type == "tool_debug" || target.Type == "tool_memory" ||
					len(target.Type) > 5 && target.Type[:5] == "tool_" {
					tools = append(tools, target)
				}
			}
		}
	}
	return tools
}

// GraphNode represents a node in the execution graph
type GraphNode struct {
	Id        string
	Name      string
	Type      string
	PositionX float64
	PositionY float64
	Config    map[string]interface{}
}

// GraphEdge represents a connection between nodes
type GraphEdge struct {
	Id           string
	Source       string
	Target       string
	SourceHandle string
	TargetHandle string
	Label        string
}

// NodePosition represents the visual position of a node
type NodePosition struct {
	X float64
	Y float64
}