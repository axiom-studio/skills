// Package grpc provides helpers for implementing gRPC-based skills
package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/axiom-studio/skills.sdk/executor"
	skillpb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
	"github.com/axiom-studio/skills.sdk/resolver"
	"google.golang.org/grpc"
)

// SkillServer implements the gRPC skill service
type SkillServer struct {
	skillpb.UnimplementedSkillServiceServer

	skillID   string
	version   string
	executors map[string]executor.StepExecutor
	schemas   map[string]*resolver.NodeSchema
}

// NewSkillServer creates a new skill server
func NewSkillServer(skillID, version string) *SkillServer {
	return &SkillServer{
		skillID:   skillID,
		version:   version,
		executors: make(map[string]executor.StepExecutor),
		schemas:   make(map[string]*resolver.NodeSchema),
	}
}

// RegisterExecutor registers an executor for a node type.
// Optionally pass a config struct to auto-generate the schema.
func (s *SkillServer) RegisterExecutor(nodeType string, exec executor.StepExecutor, configType ...interface{}) {
	s.executors[nodeType] = exec

	// Auto-generate schema from config type if provided
	if len(configType) > 0 && configType[0] != nil {
		s.schemas[nodeType] = resolver.GenerateSchema(nodeType, configType[0])
	}
}

// RegisterExecutorWithSchema registers an executor with a manually defined schema
func (s *SkillServer) RegisterExecutorWithSchema(nodeType string, exec executor.StepExecutor, schema *resolver.NodeSchema) {
	s.executors[nodeType] = exec
	s.schemas[nodeType] = schema
}

// Execute implements skillpb.SkillServer
func (s *SkillServer) Execute(ctx context.Context, req *skillpb.ExecuteRequest) (*skillpb.ExecuteResponse, error) {
	exec, ok := s.executors[req.NodeType]
	if !ok {
		return &skillpb.ExecuteResponse{
			Error: &skillpb.Error{
				Message: fmt.Sprintf("unknown node type: %s", req.NodeType),
				Type:    "validation",
			},
		}, nil
	}

	// Deserialize config and input
	config := make(map[string]interface{})
	for k, v := range req.Config {
		var val interface{}
		if err := json.Unmarshal(v, &val); err == nil {
			config[k] = val
		}
	}

	input := make(map[string]interface{})
	for k, v := range req.Input {
		var val interface{}
		if err := json.Unmarshal(v, &val); err == nil {
			input[k] = val
		}
	}

	// Deserialize bindings
	bindings := make(map[string]interface{})
	for k, v := range req.Bindings {
		var val interface{}
		if err := json.Unmarshal(v, &val); err == nil {
			bindings[k] = val
		}
	}

	// Create step definition
	step := &executor.StepDefinition{
		Id:     req.NodeId,
		Type:   req.NodeType,
		Config: config,
	}

	// Create resolver using the shared implementation
	res := resolver.New(resolver.Config{
		Bindings: bindings,
		Nodes:    map[string]interface{}{"prev": input},
		Prev:     input,
	})

	// Execute
	result, err := exec.Execute(ctx, step, res)
	if err != nil {
		return &skillpb.ExecuteResponse{
			Error: &skillpb.Error{
				Message: err.Error(),
				Type:    "execution",
			},
		}, nil
	}

	// Serialize output
	output := make(map[string][]byte)
	for k, v := range result.Output {
		data, _ := json.Marshal(v)
		output[k] = data
	}

	return &skillpb.ExecuteResponse{
		Output:   output,
		NextStep: result.NextStep,
	}, nil
}

// GetNodeTypes implements skillpb.SkillServer
func (s *SkillServer) GetNodeTypes(ctx context.Context, req *skillpb.GetNodeTypesRequest) (*skillpb.GetNodeTypesResponse, error) {
	types := make([]string, 0, len(s.executors))
	for t := range s.executors {
		types = append(types, t)
	}
	return &skillpb.GetNodeTypesResponse{NodeTypes: types}, nil
}

// GetNodeSchema implements skillpb.SkillServer
func (s *SkillServer) GetNodeSchema(ctx context.Context, req *skillpb.GetNodeSchemaRequest) (*skillpb.GetNodeSchemaResponse, error) {
	schema, ok := s.schemas[req.NodeType]
	if !ok {
		return nil, fmt.Errorf("unknown node type: %s", req.NodeType)
	}

	jsonSchema, err := schema.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema: %w", err)
	}

	return &skillpb.GetNodeSchemaResponse{Schema: jsonSchema}, nil
}

// Health implements skillpb.SkillServer
func (s *SkillServer) Health(ctx context.Context, req *skillpb.HealthRequest) (*skillpb.HealthResponse, error) {
	return &skillpb.HealthResponse{
		Healthy: true,
		SkillId: s.skillID,
		Version: s.version,
	}, nil
}

// Serve starts the Skill's gRPC endpoint. Discovery and lifecycle ownership
// belong to the installing runtime; a Skill never phones home to a product.
func (s *SkillServer) Serve(port string) error {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	grpcServer := grpc.NewServer()
	skillpb.RegisterSkillServiceServer(grpcServer, s)

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		grpcServer.GracefulStop()
	}()

	return grpcServer.Serve(lis)
}
