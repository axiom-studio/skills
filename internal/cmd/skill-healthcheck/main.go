// Command skill-healthcheck checks an OCI Skill's runtime identity without
// invoking provider actions or loading provider credentials.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	skillpb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

const maximumManifestBytes = 4 << 20

type manifestString string

func (s *manifestString) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return errors.New("manifest identity and transport fields must be strings")
	}
	*s = manifestString(node.Value)
	return nil
}

// This envelope deliberately reads release metadata only. Full canonical
// definition validation remains the installer's responsibility.
type manifestEnvelope struct {
	APIVersion manifestString `yaml:"apiVersion"`
	Kind       manifestString `yaml:"kind"`
	Definition struct {
		ID        manifestString `yaml:"id"`
		Version   manifestString `yaml:"version"`
		Transport struct {
			Kind manifestString `yaml:"kind"`
		} `yaml:"transport"`
		Installers []struct {
			Kind    manifestString `yaml:"kind"`
			Package manifestString `yaml:"package"`
		} `yaml:"installers"`
	} `yaml:"definition"`
}

type description struct {
	ID, Version, Image, Transport string
}

func loadDescription(path string) (description, error) {
	file, err := os.Open(path)
	if err != nil {
		return description{}, fmt.Errorf("open Skill manifest: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumManifestBytes+1))
	if err != nil {
		return description{}, fmt.Errorf("read Skill manifest: %w", err)
	}
	if len(data) > maximumManifestBytes {
		return description{}, errors.New("Skill manifest exceeds 4 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var manifest manifestEnvelope
	if err := decoder.Decode(&manifest); err != nil {
		return description{}, fmt.Errorf("decode Skill release metadata: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return description{}, errors.New("Skill manifest must contain one document")
	}
	if manifest.APIVersion != "openseal.dev/v1alpha1" || manifest.Kind != "SkillDefinition" {
		return description{}, errors.New("release Health check requires a canonical SkillDefinition")
	}
	result := description{ID: string(manifest.Definition.ID), Version: string(manifest.Definition.Version), Transport: string(manifest.Definition.Transport.Kind)}
	ociCount := 0
	for _, installer := range manifest.Definition.Installers {
		if installer.Kind == "oci" {
			ociCount++
			result.Image = string(installer.Package)
		}
	}
	if ociCount != 1 {
		return description{}, errors.New("Skill manifest must declare exactly one OCI installer")
	}
	for _, value := range []string{result.ID, result.Version, result.Image, result.Transport} {
		if value == "" || len(value) > 2048 {
			return description{}, errors.New("Skill release metadata is incomplete or too long")
		}
		for _, character := range value {
			if character <= ' ' || character >= 0x7f {
				return description{}, errors.New("Skill release metadata must be printable ASCII without whitespace")
			}
		}
	}
	return result, nil
}

func validateLocalAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("Health address must be a literal loopback IP and TCP port")
	}
	ip := net.ParseIP(host)
	value, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || value < 1 || value > 65535 || strconv.Itoa(value) != port {
		return errors.New("Health address must be a literal loopback IP and TCP port")
	}
	return nil
}

func probe(ctx context.Context, expected description, address string, timeout time.Duration) error {
	if expected.Transport != "tool" {
		return fmt.Errorf("SDK Health does not apply to transport %q", expected.Transport)
	}
	if err := validateLocalAddress(address); err != nil {
		return err
	}
	if timeout <= 0 || timeout > time.Minute {
		return errors.New("Health timeout must be positive and at most 60s")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dialer := &net.Dialer{}
	connection, err := grpc.NewClient("passthrough:///"+address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// A direct loopback dialer avoids environment proxies and name lookup.
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return dialer.DialContext(ctx, "tcp", address) }),
		grpc.WithDisableRetry(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<10)),
		grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoff.Config{BaseDelay: 50 * time.Millisecond, Multiplier: 1.2, Jitter: 0.2, MaxDelay: 500 * time.Millisecond}, MinConnectTimeout: 500 * time.Millisecond}),
	)
	if err != nil {
		return fmt.Errorf("create SDK Health client: %w", err)
	}
	defer connection.Close()
	client := skillpb.NewSkillServiceClient(connection)
	for {
		attempt, stopAttempt := context.WithTimeout(ctx, 500*time.Millisecond)
		response, err := client.Health(attempt, &skillpb.HealthRequest{})
		stopAttempt()
		if err == nil {
			if response == nil || !response.GetHealthy() {
				return errors.New("Skill runtime reported unhealthy")
			}
			if response.GetSkillId() != expected.ID || response.GetVersion() != expected.Version {
				return fmt.Errorf("Skill runtime identity mismatch: expected %q@%q, got %q@%q", expected.ID, expected.Version, response.GetSkillId(), response.GetVersion())
			}
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("SDK Health readiness timed out: %w", ctx.Err())
		}
		if code := status.Code(err); code != codes.Unavailable && code != codes.DeadlineExceeded {
			return fmt.Errorf("SDK Health RPC failed: %s", code)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("SDK Health readiness timed out: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("skill-healthcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("manifest", "", "canonical SkillDefinition path")
	image := flags.String("image", "", "expected OCI installer image")
	describe := flags.Bool("describe", false, "print id, version, image and transport as TSV")
	address := flags.String("address", "", "literal loopback IP and TCP port for SDK Health")
	timeout := flags.Duration("timeout", 10*time.Second, "total readiness deadline, at most 60s")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*manifest) == "" {
		fmt.Fprintln(stderr, "a --manifest path is required; positional arguments are not accepted")
		return 2
	}
	expected, err := loadDescription(*manifest)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *image != "" && *image != expected.Image {
		fmt.Fprintf(stderr, "OCI installer image mismatch: expected %q, got %q\n", expected.Image, *image)
		return 1
	}
	if *describe {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", expected.ID, expected.Version, expected.Image, expected.Transport)
		return 0
	}
	if err := probe(ctx, expected, *address, *timeout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "healthy\t%s\t%s\n", expected.ID, expected.Version)
	return 0
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
