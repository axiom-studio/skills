// Package isolation checks that a Skill declaring `tenancy: shared` keeps every
// request self-contained. A shared runtime serves all tenants from one process,
// so nothing derived from one request's bindings may be visible to another.
package isolation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Tenant is one caller of a shared runtime. Its Credential is the only value a
// request carries that identifies it; Marker is what the fake upstream returns
// to that tenant so a response can be attributed.
type Tenant struct {
	Name       string
	Credential string
	Marker     string
}

// Tenants are the two callers every isolation test interleaves.
var Tenants = [2]Tenant{
	{Name: "tenant-a", Credential: "credential-for-tenant-a-0001", Marker: "marker-tenant-a"},
	{Name: "tenant-b", Credential: "credential-for-tenant-b-0002", Marker: "marker-tenant-b"},
}

// TenantForCredential reports which tenant a credential belongs to.
func TenantForCredential(credential string) (Tenant, bool) {
	for _, tenant := range Tenants {
		if credential == tenant.Credential {
			return tenant, true
		}
	}
	return Tenant{}, false
}

// Upstream is a fake provider API. credential extracts the credential the
// Skill sent; respond writes the tenant's response. Requests without a known
// credential fail the test and receive 401.
func Upstream(t testing.TB, credential func(*http.Request) string, respond func(http.ResponseWriter, *http.Request, Tenant)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := TenantForCredential(credential(r))
		if !ok {
			t.Errorf("upstream received an unknown or missing credential on %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		respond(w, r, tenant)
	}))
	t.Cleanup(server.Close)
	return server
}

// BearerCredential extracts an `Authorization: Bearer` token.
func BearerCredential(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// Run calls the Skill for both tenants concurrently, interleaved `calls`
// times each, and fails when any result carries the other tenant's marker or
// credential, or lacks its own marker.
func Run(t *testing.T, calls int, call func(Tenant) (interface{}, error)) {
	t.Helper()
	var wg sync.WaitGroup
	for index, tenant := range Tenants {
		other := Tenants[1-index]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range calls {
				result, err := call(tenant)
				if err != nil {
					t.Errorf("%s call failed: %v", tenant.Name, err)
					return
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Errorf("%s result cannot be encoded: %v", tenant.Name, err)
					return
				}
				text := string(encoded)
				if strings.Contains(text, other.Marker) || strings.Contains(text, other.Credential) {
					t.Errorf("%s received %s's data: %s", tenant.Name, other.Name, text)
					return
				}
				if !strings.Contains(text, tenant.Marker) {
					t.Errorf("%s result is not attributable to its own request: %s", tenant.Name, text)
					return
				}
			}
		}()
	}
	wg.Wait()
}
