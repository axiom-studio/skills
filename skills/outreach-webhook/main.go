package main

import (
	"fmt"
	"os"

	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

var outreachSchema = resolver.NewSchemaBuilder(outreachSkillID).
	WithName("Post reviewed reply").
	WithDescription("Post an approved, identity-disclosed reply to an exact evidence-linked endpoint.").
	WithCategory("communication").
	WithIcon("message-circle").
	AddSection("Reply").
	AddTextField("targetUri", "Target URL", resolver.WithRequired()).
	AddTextareaField("body", "Reply", resolver.WithRequired()).
	EndSection().Build()

func main() {
	port := skillPort("50122")
	server := grpc.NewSkillServer(outreachSkillID, outreachSkillVersion)
	server.RegisterExecutorWithSchema(outreachSkillID, NewOutreachWebhookExecutor(), outreachSchema)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "serve outreach Skill: %v\n", err)
		os.Exit(1)
	}
}

func skillPort(fallback string) string {
	if port := os.Getenv("SKILL_PORT"); port != "" {
		return port
	}
	return fallback
}
