package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sdkgrpc "github.com/axiom-studio/skills.sdk/grpc"
	skillpb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
	"google.golang.org/grpc"
)

const releaseTestManifest = `apiVersion: openseal.dev/v1alpha1
kind: SkillDefinition
definition:
  id: skill-test
  version: 1.2.3
  name: Example
  transport:
    kind: tool
    endpoint: skill-test
  installers:
    - id: oci
      kind: oci
      package: axiomstudio/skill-test:1.2.3
  actions:
    never-called:
      name: never-called
      risk: external
`

func writeReleaseManifest(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skill.yaml")
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type unhealthySDKService struct{ *sdkgrpc.SkillServer }

func (s unhealthySDKService) Health(context.Context, *skillpb.HealthRequest) (*skillpb.HealthResponse, error) {
	return &skillpb.HealthResponse{Healthy: false, SkillId: "skill-test", Version: "1.2.3"}, nil
}

func startSDKHealthService(t *testing.T, service skillpb.SkillServiceServer) (string, func() []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var methods []string
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		mutex.Lock()
		methods = append(methods, info.FullMethod)
		mutex.Unlock()
		return handler(ctx, req)
	}))
	skillpb.RegisterSkillServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String(), func() []string {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]string(nil), methods...)
	}
}

func TestProbeCallsOnlyRealSDKHealthAndRejectsRuntimeDrift(t *testing.T) {
	cases := []struct {
		name    string
		service skillpb.SkillServiceServer
		wantOK  bool
		message string
	}{
		{"aligned", sdkgrpc.NewSkillServer("skill-test", "1.2.3"), true, ""},
		{"old version", sdkgrpc.NewSkillServer("skill-test", "1.2.2"), false, "identity mismatch"},
		{"wrong skill", sdkgrpc.NewSkillServer("skill-other", "1.2.3"), false, "identity mismatch"},
		{"unhealthy", unhealthySDKService{sdkgrpc.NewSkillServer("skill-test", "1.2.3")}, false, "unhealthy"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			address, methods := startSDKHealthService(t, test.service)
			path := writeReleaseManifest(t, releaseTestManifest)
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), []string{"--manifest", path, "--image", "axiomstudio/skill-test:1.2.3", "--address", address, "--timeout", "3s"}, &stdout, &stderr)
			if (code == 0) != test.wantOK || (!test.wantOK && !strings.Contains(stderr.String(), test.message)) {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			// Identity failures and unhealthy results are not readiness retries.
			if got := methods(); !reflect.DeepEqual(got, []string{"/axiom.skill.v1.SkillService/Health"}) {
				t.Fatalf("probe invoked unexpected or repeated RPCs: %v", got)
			}
			if test.wantOK && stdout.String() != "healthy\tskill-test\t1.2.3\n" {
				t.Fatalf("success output=%q", stdout.String())
			}
		})
	}
}

func TestDescribeEmitsCanonicalOCIIdentityWithoutProviderCalls(t *testing.T) {
	for _, transport := range []string{"tool", "host"} {
		t.Run(transport, func(t *testing.T) {
			path := writeReleaseManifest(t, strings.Replace(releaseTestManifest, "kind: tool", "kind: "+transport, 1))
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), []string{"--manifest", path, "--image", "axiomstudio/skill-test:1.2.3", "--describe"}, &stdout, &stderr)
			if code != 0 || stdout.String() != "skill-test\t1.2.3\taxiomstudio/skill-test:1.2.3\t"+transport+"\n" || stderr.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"--manifest", writeReleaseManifest(t, releaseTestManifest), "--image", "axiomstudio/skill-other:1.2.3", "--describe"}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "image mismatch") || stdout.Len() != 0 {
		t.Fatalf("foreign installer accepted: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDescriptionRejectsAmbiguousInvalidAndUnsafeMetadata(t *testing.T) {
	for name, document := range map[string]string{
		"legacy envelope":      strings.Replace(releaseTestManifest, "openseal.dev/v1alpha1", "skills.axiom.dev/v1", 1),
		"multiple documents":   releaseTestManifest + "\n---\n" + releaseTestManifest,
		"duplicate identity":   strings.Replace(releaseTestManifest, "  id: skill-test", "  id: skill-test\n  id: skill-test", 1),
		"numeric version":      strings.Replace(releaseTestManifest, "version: 1.2.3", "version: 123", 1),
		"whitespace injection": strings.Replace(releaseTestManifest, "id: skill-test", "id: 'skill test'", 1),
		"missing version":      strings.Replace(releaseTestManifest, "  version: 1.2.3\n", "", 1),
		"missing OCI":          strings.Replace(releaseTestManifest, "kind: oci", "kind: pip", 1),
		"multiple OCI":         strings.Replace(releaseTestManifest, "  actions:", "    - id: other\n      kind: oci\n      package: axiomstudio/other:1.0.0\n  actions:", 1),
		"oversized":            strings.Repeat(" ", maximumManifestBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadDescription(writeReleaseManifest(t, document)); err == nil {
				t.Fatal("accepted invalid or ambiguous release metadata")
			}
		})
	}
}

func TestProbeStaysOnLoopbackAndHasBoundedReadinessDeadline(t *testing.T) {
	for _, address := range []string{"example.com:50051", "192.0.2.1:50051", "0.0.0.0:50051", "localhost:50051", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:050051", "unix:///tmp/socket", "127.0.0.1"} {
		if err := validateLocalAddress(address); err == nil {
			t.Fatalf("accepted non-local or invalid address %q", address)
		}
	}
	expected := description{ID: "skill-test", Version: "1.2.3", Image: "axiomstudio/skill-test:1.2.3", Transport: "tool"}
	for _, timeout := range []time.Duration{0, -time.Second, time.Minute + time.Millisecond} {
		if err := probe(t.Context(), expected, "127.0.0.1:50051", timeout); err == nil {
			t.Fatalf("accepted invalid timeout %s", timeout)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	start := time.Now()
	err = probe(t.Context(), expected, address, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > time.Second {
		t.Fatalf("unbounded or accepted readiness: duration=%s err=%v", time.Since(start), err)
	}
	if err := probe(t.Context(), description{Transport: "host"}, "127.0.0.1:50051", time.Second); err == nil {
		t.Fatal("attempted SDK Health against non-tool transport")
	}
}

func TestDescribeMatchesRepositoryProviderManifests(t *testing.T) {
	for _, directory := range []string{"slack", "github", "google-workspace"} {
		t.Run(directory, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "skills", directory, "skill.yaml")
			metadata, err := loadDescription(path)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.ID != "skill-"+directory || metadata.Transport != "tool" || !strings.HasPrefix(metadata.Image, "axiomstudio/skill-"+directory+":") {
				t.Fatalf("unexpected repository identity: %#v", metadata)
			}
			var stdout, stderr bytes.Buffer
			if code := run(t.Context(), []string{"--manifest", path, "--describe"}, &stdout, &stderr); code != 0 {
				t.Fatalf("describe exit=%d stderr=%q", code, stderr.String())
			}
			if stdout.String() != fmt.Sprintf("%s\t%s\t%s\t%s\n", metadata.ID, metadata.Version, metadata.Image, metadata.Transport) {
				t.Fatalf("description output=%q", stdout.String())
			}
		})
	}
}
