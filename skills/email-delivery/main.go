package main

import (
	"fmt"
	"os"

	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

var emailDeliverySchema = resolver.NewSchemaBuilder(emailDeliverySkillID).
	WithName("Send reviewed email").
	WithDescription("Deliver reviewed content and durable artifacts through an authorized email identity.").
	WithCategory("communication").
	WithIcon("mail").
	AddSection("Message").
	AddTagsField("to", "Recipients", resolver.WithRequired()).
	AddTextField("subject", "Subject", resolver.WithRequired()).
	AddTextareaField("body", "Body", resolver.WithRequired()).
	AddJSONField("artifactRefs", "Attachments", resolver.WithDescription("Durable artifact references to attach.")).
	EndSection().Build()

func main() {
	port := skillPort("50123")
	server := grpc.NewSkillServer(emailDeliverySkillID, emailDeliverySkillVersion)
	server.RegisterExecutorWithSchema(emailDeliverySkillID, NewEmailDeliveryExecutor(), emailDeliverySchema)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "serve email-delivery Skill: %v\n", err)
		os.Exit(1)
	}
}

func skillPort(fallback string) string {
	if port := os.Getenv("SKILL_PORT"); port != "" {
		return port
	}
	return fallback
}
