package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

type testResolver struct{}

func (*testResolver) ResolveString(value string) string                              { return value }
func (*testResolver) ResolveMap(value map[string]interface{}) map[string]interface{} { return value }
func (*testResolver) EvaluateCondition(string) bool                                  { return false }
func (*testResolver) SetVariable(string, interface{})                                {}
func (*testResolver) GetStepOutput(string) interface{}                               { return nil }
func (*testResolver) SetStepOutput(string, interface{})                              {}

func step(config map[string]interface{}) *executor.StepDefinition {
	return &executor.StepDefinition{Config: config}
}

func withServers(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	oldOAuth, oldWeb, oldClient := redditOAuthBase, redditWebBase, httpClient
	redditOAuthBase, redditWebBase, httpClient = server.URL, server.URL, server.Client()
	t.Cleanup(func() { redditOAuthBase, redditWebBase, httpClient = oldOAuth, oldWeb, oldClient })
}

func TestAuthorizeURL(t *testing.T) {
	old := redditWebBase
	redditWebBase = "https://www.reddit.com"
	t.Cleanup(func() { redditWebBase = old })
	result, err := (&AuthorizeURLExecutor{}).Execute(context.Background(), step(map[string]interface{}{
		"clientId": "client", "redirectUri": "https://app.example/callback", "state": "csrf", "duration": "permanent", "scope": "identity read submit",
	}), &testResolver{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result.Output["authorizeUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "www.reddit.com" || parsed.Path != "/api/v1/authorize" {
		t.Fatalf("unexpected authorization URL: %s", parsed)
	}
	if got := parsed.Query().Get("scope"); got != "identity read submit" {
		t.Fatalf("scope = %q", got)
	}
}

func TestTokenGrants(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
		want   url.Values
	}{
		{"authorization code", map[string]interface{}{"grantType": "authorization_code", "code": "the-code", "redirectUri": "https://app/cb"}, url.Values{"grant_type": {"authorization_code"}, "code": {"the-code"}, "redirect_uri": {"https://app/cb"}}},
		{"refresh", map[string]interface{}{"grantType": "refresh_token", "refreshToken": "refresh"}, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"refresh"}}},
		{"password", map[string]interface{}{"grantType": "password", "username": "bot", "password": "secret"}, url.Values{"grant_type": {"password"}, "username": {"bot"}, "password": {"secret"}}},
		{"client credentials", map[string]interface{}{"grantType": "client_credentials"}, url.Values{"grant_type": {"client_credentials"}}},
		{"installed client", map[string]interface{}{"grantType": "https://oauth.reddit.com/grants/installed_client", "deviceId": "DO_NOT_TRACK_THIS_DEVICE"}, url.Values{"grant_type": {"https://oauth.reddit.com/grants/installed_client"}, "device_id": {"DO_NOT_TRACK_THIS_DEVICE"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withServers(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/access_token" || r.Method != http.MethodPost {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				id, secret, ok := r.BasicAuth()
				if !ok || id != "client" || secret != "client-secret" {
					t.Fatalf("unexpected basic auth: %q %q %v", id, secret, ok)
				}
				if r.Header.Get("User-Agent") != "atlas-test/1.0" {
					t.Fatalf("unexpected User-Agent: %q", r.Header.Get("User-Agent"))
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Encode() != test.want.Encode() {
					t.Fatalf("form = %q, want %q", r.Form.Encode(), test.want.Encode())
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"token","token_type":"bearer","expires_in":3600,"scope":"read"}`)
			})
			config := map[string]interface{}{"clientId": "client", "clientSecret": "client-secret", "userAgent": "atlas-test/1.0"}
			for key, value := range test.config {
				config[key] = value
			}
			result, err := (&TokenExecutor{}).Execute(context.Background(), step(config), &testResolver{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Output["access_token"] != "token" {
				t.Fatalf("unexpected token output: %#v", result.Output)
			}
		})
	}
}

func TestAPIRequestMetadataAndPagination(t *testing.T) {
	withServers(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/r/golang/new" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "25" || strings.Join(r.URL.Query()["raw_json"], ",") != "1,2" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("User-Agent") != "atlas-test/1.0" {
			t.Fatalf("missing managed headers: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ratelimit-Used", "3")
		w.Header().Set("X-Ratelimit-Remaining", "97")
		w.Header().Set("X-Ratelimit-Reset", "42")
		w.Header().Set("X-Reddit-Request", "request-1")
		_, _ = io.WriteString(w, `{"kind":"Listing","data":{"after":"t3_next","before":null,"dist":1,"children":[]}}`)
	})
	result, err := (&APIRequestExecutor{}).Execute(context.Background(), step(map[string]interface{}{
		"accessToken": "access", "userAgent": "atlas-test/1.0", "method": "GET", "path": "/r/golang/new?limit=25", "query": map[string]interface{}{"raw_json": []interface{}{1, 2}},
	}), &testResolver{})
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output
	if output["success"] != true || output["requestId"] != "request-1" {
		t.Fatalf("unexpected output: %#v", output)
	}
	if output["rateLimit"].(map[string]interface{})["remaining"] != "97" {
		t.Fatalf("unexpected rate limit: %#v", output["rateLimit"])
	}
	if output["pagination"].(map[string]interface{})["after"] != "t3_next" {
		t.Fatalf("unexpected pagination: %#v", output["pagination"])
	}
}

func TestAPIRequestBodyEncodings(t *testing.T) {
	png := []byte("not-really-a-png")
	tests := []struct {
		name, bodyType string
		body           interface{}
		contentType    string
		check          func(*testing.T, *http.Request, []byte)
	}{
		{"json", "json", map[string]interface{}{"title": "hello"}, "application/json", func(t *testing.T, _ *http.Request, body []byte) {
			var v map[string]interface{}
			if json.Unmarshal(body, &v) != nil || v["title"] != "hello" {
				t.Fatalf("bad JSON: %s", body)
			}
		}},
		{"form", "form", map[string]interface{}{"api_type": "json", "sr": "golang"}, "application/x-www-form-urlencoded", func(t *testing.T, _ *http.Request, body []byte) {
			values, _ := url.ParseQuery(string(body))
			if values.Get("sr") != "golang" {
				t.Fatalf("bad form: %s", body)
			}
		}},
		{"raw", "raw", "raw-value", "application/octet-stream", func(t *testing.T, _ *http.Request, body []byte) {
			if string(body) != "raw-value" {
				t.Fatalf("bad raw body: %s", body)
			}
		}},
		{"multipart", "multipart", map[string]interface{}{"upload": map[string]interface{}{"filename": "image.png", "contentBase64": base64.StdEncoding.EncodeToString(png)}, "mimetype": "image/png"}, "multipart/form-data", func(t *testing.T, r *http.Request, _ []byte) {
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Fatal(err)
			}
			file, header, err := r.FormFile("upload")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			got, _ := io.ReadAll(file)
			if header.Filename != "image.png" || string(got) != string(png) || r.FormValue("mimetype") != "image/png" {
				t.Fatal("bad multipart body")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withServers(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(r.Header.Get("Content-Type"), test.contentType) {
					t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
				}
				if test.bodyType == "multipart" {
					r.Body = io.NopCloser(strings.NewReader(string(body)))
				}
				test.check(t, r, body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
			})
			config := map[string]interface{}{"accessToken": "access", "method": "POST", "path": "/api/test", "bodyType": test.bodyType, "body": test.body}
			if test.bodyType == "raw" {
				config["rawBody"] = test.body
			}
			result, err := (&APIRequestExecutor{}).Execute(context.Background(), step(config), &testResolver{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Output["success"] != true {
				t.Fatalf("unexpected output: %#v", result.Output)
			}
		})
	}
}

func TestAPIRequestBase64Response(t *testing.T) {
	want := []byte{0, 1, 2, 0xff}
	withServers(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(want)
	})
	result, err := (&APIRequestExecutor{}).Execute(context.Background(), step(map[string]interface{}{"accessToken": "access", "path": "/api/binary", "responseEncoding": "base64"}), &testResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output["body"] != base64.StdEncoding.EncodeToString(want) {
		t.Fatalf("unexpected encoded body: %#v", result.Output["body"])
	}
}

func TestAPIRequestPreservesRedditErrors(t *testing.T) {
	withServers(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"Too Many Requests","error":429}`)
	})
	result, err := (&APIRequestExecutor{}).Execute(context.Background(), step(map[string]interface{}{"accessToken": "access", "path": "/api/v1/me"}), &testResolver{})
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output
	if output["success"] != false || output["statusCode"] != 429 {
		t.Fatalf("unexpected output: %#v", output)
	}
}

func TestAPIRequestRejectsUnsafeRoutingAndHeaders(t *testing.T) {
	tests := []map[string]interface{}{
		{"accessToken": "access", "path": "https://evil.example/steal"},
		{"accessToken": "access", "path": "//evil.example/steal"},
		{"accessToken": "access", "path": "/api/v1/me", "headers": map[string]interface{}{"Authorization": "other"}},
		{"accessToken": "access", "path": "/api/v1/me", "headers": map[string]interface{}{"Cookie": "session=secret"}},
	}
	for _, config := range tests {
		if _, err := (&APIRequestExecutor{}).Execute(context.Background(), step(config), &testResolver{}); err == nil {
			t.Fatalf("expected rejection for %#v", config)
		}
	}
}

func TestTokenRejectsOAuthErrorInSuccessfulResponse(t *testing.T) {
	withServers(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	})
	_, err := (&TokenExecutor{}).Execute(context.Background(), step(map[string]interface{}{"clientId": "client", "grantType": "refresh_token", "refreshToken": "bad"}), &testResolver{})
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("expected OAuth error, got %v", err)
	}
}
