package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

const (
	outreachSkillID      = "openseal.outreach"
	outreachSkillVersion = "1.0.0"
	outreachPostReply    = "post_reply"

	outreachPolicyDecisionTransportKey = "_opensealOutreachPolicyDecision"
	outreachApprovalPolicyTransportKey = "_opensealOutreachApprovalPolicy"
	outreachActionCallIDTransportKey   = "_opensealOutreachActionCallId"
	outreachRunIDTransportKey          = "_opensealOutreachRunId"
	outreachDeploymentIDTransportKey   = "_opensealOutreachDeploymentId"
	maximumWebhookResponseBytes        = 64 << 10
)

type outreachInvocation struct {
	ActionCallID string
	RunID        string
	DeploymentID string
	Arguments    map[string]interface{}
}

type outreachInvoker interface {
	Invoke(context.Context, outreachInvocation) (map[string]interface{}, error)
}

type outreachInvokerFunc func(context.Context, outreachInvocation) (map[string]interface{}, error)

func (f outreachInvokerFunc) Invoke(ctx context.Context, invocation outreachInvocation) (map[string]interface{}, error) {
	return f(ctx, invocation)
}

type webhookInvoker struct {
	client   *http.Client
	validate func(context.Context, *url.URL) error
	now      func() time.Time
}

func newWebhookInvoker() *webhookInvoker {
	resolver := net.DefaultResolver
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("invalid outreach address: %w", err)
			}
			addresses, err := publicOutreachAddresses(ctx, resolver, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
		},
		MaxIdleConns: 20, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
	}
	return &webhookInvoker{
		client: &http.Client{Transport: transport, Timeout: 30 * time.Second},
		now:    time.Now,
		validate: func(ctx context.Context, target *url.URL) error {
			if target == nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Fragment != "" || (target.Port() != "" && target.Port() != "443") {
				return errors.New("outreach target must be an absolute credential-free HTTPS URL on port 443")
			}
			_, err := publicOutreachAddresses(ctx, resolver, target.Hostname())
			return err
		},
	}
}

func (w *webhookInvoker) Invoke(ctx context.Context, invocation outreachInvocation) (map[string]interface{}, error) {
	if w == nil || w.client == nil || w.validate == nil || w.now == nil || !safeHeaderValue(invocation.ActionCallID) || strings.TrimSpace(invocation.RunID) == "" || strings.TrimSpace(invocation.DeploymentID) == "" {
		return nil, errors.New("governed outreach invocation identity is incomplete")
	}
	targetURI, _ := invocation.Arguments["targetUri"].(string)
	body, _ := invocation.Arguments["body"].(string)
	approvalPolicy, _ := invocation.Arguments[outreachApprovalPolicyTransportKey].(string)
	if strings.TrimSpace(targetURI) == "" || strings.TrimSpace(body) == "" || strings.TrimSpace(approvalPolicy) == "" {
		return nil, errors.New("outreach target, reviewed body, and approval policy are required")
	}
	decision, err := decodeOutreachPolicyDecision(invocation.Arguments[outreachPolicyDecisionTransportKey])
	if err != nil {
		return nil, err
	}
	if err := decision.Authorize(targetURI, len([]byte(body)), approvalPolicy); err != nil {
		return nil, err
	}
	target, err := url.Parse(targetURI)
	if err != nil {
		return nil, errors.New("outreach target is invalid")
	}
	if err := w.validate(ctx, target); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", invocation.ActionCallID)
	request.Header.Set("User-Agent", "OpenSeal-Outreach/1.0")
	client := *w.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("outreach webhook redirects are not allowed")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("post governed outreach: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumWebhookResponseBytes))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("outreach provider returned HTTP %d", response.StatusCode)
	}
	externalID := safeProviderID(response.Header.Get("X-Request-Id"))
	if externalID == "" {
		externalID = "http:" + invocation.ActionCallID
	}
	digest := sha256.Sum256([]byte(body))
	return map[string]interface{}{"outreachReceipt": map[string]interface{}{
		"provider": "http-webhook", "externalId": externalID, "externalUri": target.String(),
		"deliveredAt": w.now().UTC().Format(time.RFC3339Nano), "digest": "sha256:" + hex.EncodeToString(digest[:]),
	}}, nil
}

type OutreachWebhookExecutor struct{ invoker outreachInvoker }

func NewOutreachWebhookExecutor() *OutreachWebhookExecutor {
	return &OutreachWebhookExecutor{invoker: newWebhookInvoker()}
}

func (e *OutreachWebhookExecutor) Type() string { return outreachSkillID }

func (e *OutreachWebhookExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	if e == nil || e.invoker == nil || step == nil || step.Config == nil {
		return nil, errors.New("outreach webhook executor is not configured")
	}
	actionCallID, _ := step.Config[outreachActionCallIDTransportKey].(string)
	runID, _ := step.Config[outreachRunIDTransportKey].(string)
	deploymentID, _ := step.Config[outreachDeploymentIDTransportKey].(string)
	if strings.TrimSpace(actionCallID) == "" || strings.TrimSpace(runID) == "" || strings.TrimSpace(deploymentID) == "" {
		return nil, errors.New("outreach transport requires ActionCall, Run, and deployment identity")
	}
	output, err := e.invoker.Invoke(ctx, outreachInvocation{ActionCallID: actionCallID, RunID: runID, DeploymentID: deploymentID, Arguments: cloneMap(step.Config)})
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: output}, nil
}

type outreachPolicyDecision struct {
	PolicyID       string `json:"policyId"`
	PolicyVersion  string `json:"policyVersion"`
	SourceHost     string `json:"sourceHost"`
	PathPrefix     string `json:"pathPrefix"`
	ApprovalPolicy string `json:"approvalPolicy"`
	MaximumBytes   int    `json:"maximumBytes"`
}

func (d outreachPolicyDecision) Authorize(rawURL string, bodyBytes int, approvalPolicy string) error {
	host := strings.ToLower(strings.TrimSpace(d.SourceHost))
	prefix := strings.TrimSpace(d.PathPrefix)
	if strings.TrimSpace(d.PolicyID) == "" || strings.TrimSpace(d.PolicyVersion) == "" || host == "" || strings.ContainsAny(host, "/:@[]") ||
		!strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "..") || strings.TrimSpace(d.ApprovalPolicy) == "" || d.MaximumBytes < 1 || d.MaximumBytes > 20000 {
		return errors.New("outreach policy decision is invalid")
	}
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" || strings.ToLower(target.Hostname()) != host ||
		(target.Port() != "" && target.Port() != "443") {
		return errors.New("outreach target is outside the authorized policy decision")
	}
	if bodyBytes < 1 || bodyBytes > d.MaximumBytes {
		return errors.New("outreach body exceeds the authorized policy decision")
	}
	if strings.TrimSpace(approvalPolicy) != d.ApprovalPolicy {
		return errors.New("outreach approval policy does not match the authorized policy decision")
	}
	path := target.EscapedPath()
	if path == "" {
		path = "/"
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	if path != trimmed && !strings.HasPrefix(path, trimmed+"/") {
		return errors.New("outreach target path is outside the authorized policy decision")
	}
	return nil
}

func decodeOutreachPolicyDecision(value interface{}) (*outreachPolicyDecision, error) {
	encoded, err := json.Marshal(value)
	if value == nil || err != nil {
		return nil, errors.New("trusted outreach policy decision is required")
	}
	var decision outreachPolicyDecision
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		return nil, errors.New("trusted outreach policy decision is invalid")
	}
	if err := decision.Authorize("https://"+decision.SourceHost+decision.PathPrefix, 1, decision.ApprovalPolicy); err != nil {
		return nil, errors.New("trusted outreach policy decision is invalid")
	}
	return &decision, nil
}

func publicOutreachAddresses(ctx context.Context, resolver *net.Resolver, host string) ([]net.IP, error) {
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve outreach host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("outreach host has no addresses")
	}
	result := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ip := address.IP
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return nil, errors.New("outreach host resolves to a non-public address")
		}
		result = append(result, ip)
	}
	return result, nil
}

func safeHeaderValue(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func safeProviderID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 || !safeHeaderValue(value) {
		return ""
	}
	return value
}

func cloneMap(value map[string]interface{}) map[string]interface{} {
	encoded, _ := json.Marshal(value)
	var result map[string]interface{}
	_ = json.Unmarshal(encoded, &result)
	return result
}
