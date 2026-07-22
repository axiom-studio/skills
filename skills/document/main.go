package main

import (
	"fmt"
	"os"

	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

var documentSchema = resolver.NewSchemaBuilder(documentSkillID).
	WithName("Render PDF").
	WithDescription("Render a cited report into a durable PDF artifact.").
	WithCategory("documents").
	WithIcon("file-text").
	AddSection("Document").
	AddTextField("title", "Title", resolver.WithRequired()).
	AddTextareaField("body", "Body", resolver.WithRequired(), resolver.WithDescription("Include source citations in the report body.")).
	AddTextField("filename", "Filename", resolver.WithRequired(), resolver.WithPlaceholder("report.pdf")).
	AddTextField("requirementName", "Deliverable", resolver.WithRequired()).
	AddTagsField("sources", "Sources", resolver.WithDescription("Optional source URLs represented in the report.")).
	EndSection().Build()

func main() {
	port := skillPort("50120")
	server := grpc.NewSkillServer(documentSkillID, documentSkillVersion)
	server.RegisterExecutorWithSchema(documentSkillID, NewDocumentPDFExecutor(plainTextPDFRenderer{}), documentSchema)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "serve document Skill: %v\n", err)
		os.Exit(1)
	}
}

func skillPort(fallback string) string {
	if port := os.Getenv("SKILL_PORT"); port != "" {
		return port
	}
	return fallback
}
