package integration

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/axiom-studio/skills.sdk/grpc"
)

type compileConfig struct {
	Profile map[string]interface{} `json:"profile" description:"Learned integration profile; no credentials"`
}
type callConfig struct {
	ProfileHash string                 `json:"profileHash" description:"Hash of the mounted contract"`
	Arguments   map[string]interface{} `json:"arguments" description:"Arguments conforming to the compiled operation schema"`
}
type boundCallConfig struct {
	ProfileHash string                 `json:"profileHash"`
	Operation   string                 `json:"operation"`
	Arguments   map[string]interface{} `json:"arguments"`
}

func Main(transport string) {
	if err := run(transport); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(transport string) error {
	path := flag.String("profile", os.Getenv("INTEGRATION_PROFILE"), "Path to trusted integration profile JSON")
	compile := flag.Bool("compile", false, "Print deterministic manifest/profile bundle; no network calls")
	check := flag.Bool("check", false, "Validate profile and print hash; no network calls")
	flag.Parse()
	var r *Runtime
	if *path != "" {
		p, err := Load(*path)
		if err != nil {
			return err
		}
		if p.Transport != transport {
			return fmt.Errorf("profile transport mismatch")
		}
		r, err = New(p)
		if err != nil {
			return err
		}
	}
	if *compile || *check {
		if r == nil {
			return fmt.Errorf("-profile required")
		}
		var result interface{}
		var err error
		switch {
		case *compile:
			result, err = Compile(r.Profile)
		case *check:
			result = map[string]interface{}{"profileHash": r.Hash}
		}
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	id := "skill-" + transport
	version := RuntimeVersion
	if r != nil {
		id = "skill-" + r.Profile.ID
		version = RuntimeVersion + "-" + r.Hash[:12]
	}
	server := grpc.NewSkillServer(id, version)
	server.RegisterExecutor(transport+"-compile", &CompilerAdapter{Transport: transport}, compileConfig{})
	if r == nil {
		for _, mode := range []string{"read", "write", "inspect"} {
			a := &BoundAdapter{Transport: transport, Mode: mode}
			server.RegisterExecutor(a.Type(), a, boundCallConfig{})
		}
	}
	if r != nil {
		server.RegisterExecutor(transport+"-describe", &Adapter{Runtime: r, Name: transport + "-describe"}, struct{}{})
		for _, op := range r.Profile.Operations {
			server.RegisterExecutor(op.Name, &Adapter{Runtime: r, Name: op.Name, Operation: op.Name}, callConfig{})
		}
	}
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50051"
	}
	return server.Serve(port)
}
