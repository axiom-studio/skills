package main

import (
	"fmt"
	"os"

	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

var sourceFeedSchema = resolver.NewSchemaBuilder(sourceFeedSkillID).
	WithName("Observe source feed").
	WithDescription("Read an authorized RSS or Atom feed and emit provenance-linked observations.").
	WithCategory("research").
	WithIcon("rss").
	AddSection("Source").
	AddTextField("url", "Feed URL", resolver.WithRequired(), resolver.WithPlaceholder("https://community.example/feed.xml")).
	AddNumberField("maxItems", "Maximum items", resolver.WithDefault(25), resolver.WithDescription("Between 1 and 100."), resolver.WithMinMax(1, 100)).
	EndSection().Build()

func main() {
	port := skillPort("50121")
	server := grpc.NewSkillServer(sourceFeedSkillID, sourceFeedSkillVersion)
	server.RegisterExecutorWithSchema(sourceFeedSkillID, NewSourceFeedExecutor(), sourceFeedSchema)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "serve source-feed Skill: %v\n", err)
		os.Exit(1)
	}
}

func skillPort(fallback string) string {
	if port := os.Getenv("SKILL_PORT"); port != "" {
		return port
	}
	return fallback
}
