package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	skillgrpc "github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	skillID             = "skill-browser"
	skillVersion        = "1.1.6"
	defaultPort         = "50112"
	defaultIdleTimeout  = 15 * time.Minute
	maxCommandTimeout   = 35 * time.Second
	maxSnapshotBytes    = 200 << 10
	maxScreenshotBytes  = 4 << 20
	maxIntentBytes      = 500
	maxIdempotencyItems = 1000
)

var (
	safeNameRE       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	targetRE         = regexp.MustCompile(`^s([1-9][0-9]*):(e[1-9][0-9]*)$`)
	secretNameRE     = regexp.MustCompile(`(?i)(password|passcode|secret|token|api.?key|authorization|cookie|session.?id|private.?key)`)
	challengePattern = map[string]*regexp.Regexp{
		"captcha":  regexp.MustCompile(`(?i)\b(captcha|recaptcha|hcaptcha|verify you are human)\b`),
		"mfa":      regexp.MustCompile(`(?i)\b(multi.factor|two.factor|2fa|one.time (code|password)|authenticator code|verification code)\b`),
		"login":    regexp.MustCompile(`(?i)\b(log[ -]?in|sign[ -]?in|username|email address|password)\b`),
		"consent":  regexp.MustCompile(`(?i)\b(cookie consent|consent preferences|accept (all )?cookies|privacy choices)\b`),
		"anti-bot": regexp.MustCompile(`(?i)\b(access denied|unusual traffic|bot detection|security check|cloudflare ray id)\b`),
	}
)

type commandResult struct {
	Success bool                   `json:"success"`
	Data    map[string]interface{} `json:"data"`
	Error   interface{}            `json:"error"`
	Type    string                 `json:"type,omitempty"`
}

type browserEngine interface {
	Run(ctx context.Context, s *browserSession, args ...string) (map[string]interface{}, error)
	RunBatch(ctx context.Context, s *browserSession, commands [][]string) ([]map[string]interface{}, error)
	Close(ctx context.Context, s *browserSession) error
	Ready(context.Context) error
}

type agentBrowserEngine struct {
	binary      string
	executable  string
	proxyURL    string
	idleTimeout time.Duration
}

func (e *agentBrowserEngine) environment(s *browserSession) []string {
	commandTimeout := strconv.Itoa(int(maxCommandTimeout.Milliseconds() - 5000))
	idleTimeout := e.idleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	environment := append(os.Environ(),
		"HOME="+s.runtimeDir,
		"XDG_CONFIG_HOME="+filepath.Join(s.runtimeDir, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(s.runtimeDir, ".cache"),
		"AGENT_BROWSER_EXECUTABLE_PATH="+e.executable,
		"AGENT_BROWSER_PROXY="+e.proxyURL,
		"AGENT_BROWSER_PROXY_BYPASS=<-loopback>",
		"AGENT_BROWSER_DEFAULT_TIMEOUT="+commandTimeout,
		"AGENT_BROWSER_IDLE_TIMEOUT="+strconv.FormatInt(idleTimeout.Milliseconds(), 10),
		"AGENT_BROWSER_NO_XVFB=1",
	)
	if s.profileDir != "" {
		environment = append(environment, "AGENT_BROWSER_PROFILE="+s.profileDir)
	}
	return environment
}

func (e *agentBrowserEngine) command(ctx context.Context, s *browserSession, stdin io.Reader, args ...string) ([]byte, error) {
	all := append([]string{"--session", s.id}, args...)
	cmd := exec.CommandContext(ctx, e.binary, all...)
	cmd.Env = e.environment(s)
	cmd.Dir = s.runtimeDir
	cmd.Stdin = stdin
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("browser engine command failed: %s", msg)
	}
	return []byte(strings.TrimSpace(stdout.String())), nil
}

func (e *agentBrowserEngine) Run(ctx context.Context, s *browserSession, args ...string) (map[string]interface{}, error) {
	out, err := e.command(ctx, s, nil, append(args, "--json")...)
	if err != nil {
		return nil, err
	}
	var result commandResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("browser engine returned invalid JSON")
	}
	if !result.Success {
		return nil, fmt.Errorf("browser engine rejected command: %v", result.Error)
	}
	if result.Data == nil {
		result.Data = map[string]interface{}{}
	}
	return result.Data, nil
}

func (e *agentBrowserEngine) RunBatch(ctx context.Context, s *browserSession, commands [][]string) ([]map[string]interface{}, error) {
	encoded, err := json.Marshal(commands)
	if err != nil {
		return nil, err
	}
	out, err := e.command(ctx, s, strings.NewReader(string(encoded)), "batch", "--json", "--bail")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Success bool                   `json:"success"`
		Result  map[string]interface{} `json:"result"`
		Error   interface{}            `json:"error"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("browser engine returned invalid batch JSON")
	}
	results := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if !item.Success {
			return nil, fmt.Errorf("browser engine rejected batch command: %v", item.Error)
		}
		results = append(results, item.Result)
	}
	return results, nil
}

func (e *agentBrowserEngine) Close(ctx context.Context, s *browserSession) error {
	_, err := e.Run(ctx, s, "close")
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not running") {
		return err
	}
	return nil
}

func (e *agentBrowserEngine) Ready(ctx context.Context) error {
	for label, path := range map[string]string{"agent-browser": e.binary, "Chromium": e.executable} {
		if !filepath.IsAbs(path) {
			resolved, err := exec.LookPath(path)
			if err != nil {
				return fmt.Errorf("%s executable is unavailable", label)
			}
			path = resolved
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			return fmt.Errorf("%s executable is unavailable", label)
		}
	}
	return nil
}

type idempotencyRecord struct {
	Fingerprint string                 `json:"fingerprint"`
	Output      map[string]interface{} `json:"output"`
	CreatedAt   time.Time              `json:"createdAt"`
}

type sessionMetadata struct {
	ID             string                       `json:"id"`
	ProfileName    string                       `json:"profileName,omitempty"`
	CurrentURL     string                       `json:"currentUrl,omitempty"`
	Title          string                       `json:"title,omitempty"`
	ViewportWidth  int                          `json:"viewportWidth,omitempty"`
	ViewportHeight int                          `json:"viewportHeight,omitempty"`
	Generation     int                          `json:"generation"`
	SnapshotValid  bool                         `json:"snapshotValid"`
	SecretTainted  bool                         `json:"secretTainted"`
	LastActivity   time.Time                    `json:"lastActivity"`
	CreatedAt      time.Time                    `json:"createdAt"`
	ClosedAt       *time.Time                   `json:"closedAt,omitempty"`
	Idempotency    map[string]idempotencyRecord `json:"idempotency,omitempty"`
	LastChallenges []string                     `json:"lastChallenges,omitempty"`
}

type browserSession struct {
	mu         sync.Mutex
	id         string
	runtimeDir string
	profileDir string
	metaPath   string
	meta       sessionMetadata
	refs       map[string]map[string]interface{}
}

type destinationPolicy struct {
	resolver     *net.Resolver
	allowedHosts []string
	allowedPorts map[string]bool
	allowPrivate bool
}

func newDestinationPolicy(allowlist string, allowPrivate bool) *destinationPolicy {
	var hosts []string
	for _, item := range strings.Split(allowlist, ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			hosts = append(hosts, strings.TrimSuffix(item, "."))
		}
	}
	return &destinationPolicy{
		resolver: net.DefaultResolver, allowedHosts: hosts, allowPrivate: allowPrivate,
		allowedPorts: map[string]bool{"80": true, "443": true},
	}
}

func (p *destinationPolicy) addAllowedPorts(raw string) error {
	for _, item := range strings.Split(raw, ",") {
		port := strings.TrimSpace(item)
		if port == "" {
			continue
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return fmt.Errorf("invalid allowed browser port %q", port)
		}
		p.allowedPorts[port] = true
	}
	return nil
}

func (p *destinationPolicy) hostAllowed(host string) bool {
	if len(p.allowedHosts) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range p.allowedHosts {
		if allowed == host {
			return true
		}
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
		}
	}
	return false
}

func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// Benchmarking, carrier-grade NAT, documentation, multicast/reserved, and metadata.
		return v4[0] == 0 || v4[0] >= 224 ||
			(v4[0] == 100 && v4[1]&0xc0 == 64) ||
			(v4[0] == 192 && v4[1] == 0 && v4[2] == 0) ||
			(v4[0] == 192 && v4[1] == 0 && v4[2] == 2) ||
			(v4[0] == 198 && (v4[1] == 18 || v4[1] == 19)) ||
			(v4[0] == 198 && v4[1] == 51 && v4[2] == 100) ||
			(v4[0] == 203 && v4[1] == 0 && v4[2] == 113) ||
			(v4[0] == 169 && v4[1] == 254)
	}
	if len(ip) == net.IPv6len && ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
		return true
	}
	return false
}

func (p *destinationPolicy) resolve(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return nil, errors.New("destination host is required")
	}
	switch host {
	case "localhost", "localhost.localdomain", "metadata.google.internal", "metadata.goog",
		"kubernetes.default", "kubernetes.default.svc", "kubernetes.default.svc.cluster.local":
		if !p.allowPrivate {
			return nil, fmt.Errorf("destination host %q is blocked", host)
		}
	}
	if !p.allowPrivate && (strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".svc") ||
		strings.HasSuffix(host, ".svc.cluster.local") || strings.HasSuffix(host, ".cluster.local")) {
		return nil, fmt.Errorf("destination host %q is blocked", host)
	}
	if !p.hostAllowed(host) {
		return nil, fmt.Errorf("destination host %q is not in the configured allowlist", host)
	}
	var ips []net.IP
	if parsed := net.ParseIP(host); parsed != nil {
		ips = []net.IP{parsed}
	} else {
		resolved, err := p.resolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("destination DNS resolution failed")
		}
		ips = resolved
	}
	if len(ips) == 0 {
		return nil, errors.New("destination resolved to no addresses")
	}
	for _, ip := range ips {
		if !p.allowPrivate && blockedIP(ip) {
			return nil, fmt.Errorf("destination resolved to blocked address class")
		}
	}
	return ips, nil
}

func (p *destinationPolicy) validateURL(ctx context.Context, raw string) (*url.URL, error) {
	if len(raw) > 4096 {
		return nil, errors.New("URL exceeds 4096 characters")
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("only http and https URLs are allowed")
	}
	if parsed.User != nil {
		return nil, errors.New("embedded URL credentials are forbidden")
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("URL hostname is required")
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if !p.allowedPorts[port] {
		return nil, fmt.Errorf("destination port %s is not allowed", port)
	}
	if _, err := p.resolve(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	return parsed, nil
}

type safeProxy struct {
	listener net.Listener
	server   *http.Server
	policy   *destinationPolicy
}

func startSafeProxy(policy *destinationPolicy) (*safeProxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	proxy := &safeProxy{listener: listener, policy: policy}
	proxy.server = &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() { _ = proxy.server.Serve(listener) }()
	return proxy, nil
}

func (p *safeProxy) URL() string { return "http://" + p.listener.Addr().String() }

func (p *safeProxy) Close(ctx context.Context) error { return p.server.Shutdown(ctx) }

func splitDestination(authority, scheme string) (string, string, error) {
	defaultPort := "80"
	if scheme == "https" {
		defaultPort = "443"
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		if strings.Contains(err.Error(), "missing port") {
			host, port = authority, defaultPort
		} else {
			return "", "", errors.New("invalid destination authority")
		}
	}
	if host == "" {
		return "", "", errors.New("destination host is required")
	}
	number, parseErr := strconv.Atoi(port)
	if parseErr != nil || number < 1 || number > 65535 {
		return "", "", errors.New("invalid destination port")
	}
	return strings.Trim(host, "[]"), port, nil
}

func dialResolved(ctx context.Context, policy *destinationPolicy, host, port string) (net.Conn, error) {
	ips, err := policy.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	var last error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, fmt.Errorf("destination connection failed: %w", last)
}

func (p *safeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.serveConnect(w, r)
		return
	}
	if r.URL == nil || (r.URL.Scheme != "http" && r.URL.Scheme != "https") {
		http.Error(w, "invalid proxy destination", http.StatusBadRequest)
		return
	}
	host, port, err := splitDestination(r.URL.Host, r.URL.Scheme)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if !p.policy.allowedPorts[port] {
		http.Error(w, "destination port is not allowed", http.StatusForbidden)
		return
	}
	if _, err := p.policy.resolve(r.Context(), host); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header = r.Header.Clone()
	out.Header.Del("Proxy-Authorization")
	out.Header.Del("Proxy-Connection")
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialResolved(ctx, p.policy, host, port)
		},
		DisableCompression: false,
	}
	defer transport.CloseIdleConnections()
	resp, err := transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "destination request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 20<<20))
}

func (p *safeProxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	host, port, err := splitDestination(r.Host, "https")
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if !p.policy.allowedPorts[port] {
		http.Error(w, "destination port is not allowed", http.StatusForbidden)
		return
	}
	upstream, err := dialResolved(r.Context(), p.policy, host, port)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "proxy tunnel unavailable", http.StatusInternalServerError)
		return
	}
	downstream, buffered, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	_, _ = downstream.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if buffered.Reader.Buffered() > 0 {
		_, _ = io.CopyN(upstream, buffered, int64(buffered.Reader.Buffered()))
	}
	go tunnel(upstream, downstream)
	go tunnel(downstream, upstream)
}

func tunnel(dst, src net.Conn) {
	defer dst.Close()
	defer src.Close()
	_, _ = io.Copy(dst, src)
}

type browserService struct {
	mu          sync.RWMutex
	workspace   string
	engine      browserEngine
	policy      *destinationPolicy
	proxy       *safeProxy
	sessions    map[string]*browserSession
	idleTimeout time.Duration
	stopCleanup chan struct{}
}

func newBrowserService(workspace string, policy *destinationPolicy, idle time.Duration, engine browserEngine) (*browserService, error) {
	if workspace == "" || !filepath.IsAbs(workspace) {
		return nil, errors.New("BROWSER_WORKSPACE must be an absolute path")
	}
	for _, dir := range []string{workspace, filepath.Join(workspace, "sessions"), filepath.Join(workspace, "profiles"), filepath.Join(workspace, "screenshots")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create browser workspace: %w", err)
		}
	}
	if idle <= 0 {
		idle = defaultIdleTimeout
	}
	service := &browserService{
		workspace: workspace, engine: engine, policy: policy, sessions: map[string]*browserSession{},
		idleTimeout: idle, stopCleanup: make(chan struct{}),
	}
	if err := service.loadSessions(); err != nil {
		return nil, err
	}
	go service.cleanupLoop()
	return service, nil
}

func (s *browserService) loadSessions() error {
	entries, err := os.ReadDir(filepath.Join(s.workspace, "sessions"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !safeNameRE.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.workspace, "sessions", entry.Name())
		metaPath := filepath.Join(dir, "session.json")
		encoded, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta sessionMetadata
		if json.Unmarshal(encoded, &meta) != nil || meta.ID != entry.Name() {
			continue
		}
		meta.SnapshotValid = false
		session := &browserSession{id: meta.ID, runtimeDir: dir, metaPath: metaPath, meta: meta, refs: map[string]map[string]interface{}{}}
		if meta.ProfileName != "" {
			session.profileDir = filepath.Join(s.workspace, "profiles", meta.ProfileName)
		}
		s.sessions[meta.ID] = session
	}
	return nil
}

func (s *browserService) cleanupLoop() {
	interval := s.idleTimeout / 2
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.cleanupIdle()
		case <-s.stopCleanup:
			return
		}
	}
}

func (s *browserService) cleanupIdle() {
	s.mu.RLock()
	list := make([]*browserSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		list = append(list, session)
	}
	s.mu.RUnlock()
	for _, session := range list {
		session.mu.Lock()
		if session.meta.ClosedAt == nil && time.Since(session.meta.LastActivity) >= s.idleTimeout {
			_ = s.closeLocked(context.Background(), session)
		}
		session.mu.Unlock()
	}
}

func (s *browserService) Shutdown(ctx context.Context) {
	select {
	case <-s.stopCleanup:
	default:
		close(s.stopCleanup)
	}
	s.mu.RLock()
	list := make([]*browserSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		list = append(list, session)
	}
	s.mu.RUnlock()
	for _, session := range list {
		session.mu.Lock()
		if session.meta.ClosedAt == nil {
			_ = s.closeLocked(ctx, session)
		}
		session.mu.Unlock()
	}
	if s.proxy != nil {
		_ = s.proxy.Close(ctx)
	}
}

func validateName(kind, value string) error {
	if !safeNameRE.MatchString(value) {
		return fmt.Errorf("%s must match %s", kind, safeNameRE.String())
	}
	return nil
}

func (s *browserService) session(id string) (*browserSession, error) {
	if err := validateName("sessionId", id); err != nil {
		return nil, err
	}
	s.mu.RLock()
	session := s.sessions[id]
	s.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("browser session %q does not exist; call browser-open first", id)
	}
	return session, nil
}

func (s *browserService) createSession(id, profile string) (*browserSession, error) {
	if err := validateName("sessionId", id); err != nil {
		return nil, err
	}
	if profile != "" {
		if err := validateName("profileName", profile); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.sessions[id]; existing != nil {
		if existing.meta.ProfileName != profile {
			return nil, errors.New("a browser session cannot change its persistent profile")
		}
		if profile != "" && existing.meta.ClosedAt != nil {
			for _, other := range s.sessions {
				if other != existing && other.meta.ClosedAt == nil && other.meta.ProfileName == profile {
					return nil, fmt.Errorf("profile %q is already in use by another active session", profile)
				}
			}
		}
		return existing, nil
	}
	if profile != "" {
		for _, existing := range s.sessions {
			if existing.meta.ClosedAt == nil && existing.meta.ProfileName == profile {
				return nil, fmt.Errorf("profile %q is already in use by another active session", profile)
			}
		}
	}
	dir := filepath.Join(s.workspace, "sessions", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	session := &browserSession{
		id: id, runtimeDir: dir, metaPath: filepath.Join(dir, "session.json"), refs: map[string]map[string]interface{}{},
		meta: sessionMetadata{ID: id, ProfileName: profile, CreatedAt: now, LastActivity: now, Idempotency: map[string]idempotencyRecord{}},
	}
	if profile != "" {
		session.profileDir = filepath.Join(s.workspace, "profiles", profile)
		if err := os.MkdirAll(session.profileDir, 0700); err != nil {
			return nil, err
		}
	}
	s.sessions[id] = session
	return session, session.persist()
}

func (s *browserSession) persist() error {
	encoded, err := json.MarshalIndent(s.meta, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.metaPath + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.metaPath)
}

func (s *browserSession) touch() {
	s.meta.LastActivity = time.Now().UTC()
}

func cloneOutput(in map[string]interface{}) map[string]interface{} {
	encoded, _ := json.Marshal(in)
	var out map[string]interface{}
	_ = json.Unmarshal(encoded, &out)
	return out
}

func (s *browserSession) duplicate(action, key, fingerprint string) (map[string]interface{}, error) {
	if len(key) < 8 || len(key) > 128 {
		return nil, errors.New("idempotencyKey must contain 8 to 128 characters")
	}
	record, ok := s.meta.Idempotency[action+":"+key]
	if !ok {
		return nil, nil
	}
	if record.Fingerprint != fingerprint {
		return nil, errors.New("idempotencyKey was already used with different arguments")
	}
	out := cloneOutput(record.Output)
	out["duplicate"] = true
	return out, nil
}

func (s *browserSession) remember(action, key, fingerprint string, output map[string]interface{}) {
	if s.meta.Idempotency == nil {
		s.meta.Idempotency = map[string]idempotencyRecord{}
	}
	if len(s.meta.Idempotency) >= maxIdempotencyItems {
		var oldestKey string
		var oldest time.Time
		for key, record := range s.meta.Idempotency {
			if oldestKey == "" || record.CreatedAt.Before(oldest) {
				oldestKey, oldest = key, record.CreatedAt
			}
		}
		delete(s.meta.Idempotency, oldestKey)
	}
	s.meta.Idempotency[action+":"+key] = idempotencyRecord{Fingerprint: fingerprint, Output: cloneOutput(output), CreatedAt: time.Now().UTC()}
}

func fingerprint(action string, values ...string) string {
	sum := sha256.Sum256([]byte(action + "\x00" + strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func configMap(step *executor.StepDefinition, r executor.TemplateResolver) map[string]interface{} {
	if step == nil || step.Config == nil {
		return map[string]interface{}{}
	}
	return r.ResolveMap(step.Config)
}

func stringValue(config map[string]interface{}, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func boolValue(config map[string]interface{}, key string) bool {
	switch value := config[key].(type) {
	case bool:
		return value
	case string:
		parsed, _ := strconv.ParseBool(value)
		return parsed
	default:
		return false
	}
}

func intValue(config map[string]interface{}, key string, fallback int) int {
	switch value := config[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func commandContext(ctx context.Context, seconds int) (context.Context, context.CancelFunc, error) {
	if seconds < 1 || seconds > 30 {
		return nil, nil, errors.New("timeoutSeconds must be between 1 and 30")
	}
	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	return commandCtx, cancel, nil
}

func (s *browserService) refreshPageState(ctx context.Context, session *browserSession) {
	if data, err := s.engine.Run(ctx, session, "get", "url"); err == nil {
		session.meta.CurrentURL, _ = data["url"].(string)
	}
	if data, err := s.engine.Run(ctx, session, "get", "title"); err == nil {
		session.meta.Title, _ = data["title"].(string)
	}
	session.touch()
}

func challenges(text string) []string {
	var found []string
	for _, kind := range []string{"login", "captcha", "mfa", "consent", "anti-bot"} {
		if challengePattern[kind].MatchString(text) {
			found = append(found, kind)
		}
	}
	return found
}

func blockedChallenge(values []string) bool {
	for _, value := range values {
		if value == "captcha" || value == "mfa" || value == "anti-bot" {
			return true
		}
	}
	return false
}

func boundedString(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	return value[:limit], true
}

func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		if len(secret) >= 3 {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

func bindingSecrets(config map[string]interface{}, r executor.TemplateResolver) (map[string]string, error) {
	result := map[string]string{}
	bindings, hasBindings := r.(executor.BindingResolver)
	for _, field := range []string{"username", "password"} {
		if hasBindings {
			if value, ok := bindings.GetBinding(field).(string); ok && value != "" {
				result[field] = value
			}
		}
		if value, ok := config[field].(string); ok && value != "" {
			if existing := result[field]; existing != "" && existing != value {
				return nil, fmt.Errorf("conflicting bound credential field %q", field)
			}
			result[field] = value
		}
		delete(config, field)
	}
	return result, nil
}

func secretValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func (s *browserService) open(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	id, profile, rawURL := stringValue(config, "sessionId"), stringValue(config, "profileName"), stringValue(config, "url")
	parsed, err := s.policy.validateURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	session, err := s.createSession(id, profile)
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.meta.ClosedAt = nil
	width, height := intValue(config, "viewportWidth", 1440), intValue(config, "viewportHeight", 900)
	if width < 320 || width > 1920 || height < 320 || height > 1080 {
		return nil, errors.New("viewport must be between 320x320 and 1920x1080")
	}
	commandCtx, cancel, err := commandContext(ctx, intValue(config, "timeoutSeconds", 30))
	if err != nil {
		return nil, err
	}
	defer cancel()
	args := []string{"open", parsed.String()}
	data, err := s.openEnginePageLocked(commandCtx, session, args)
	if err != nil {
		return nil, err
	}
	if _, err := s.engine.Run(commandCtx, session, "set", "viewport", strconv.Itoa(width), strconv.Itoa(height)); err != nil {
		_ = s.engine.Close(context.Background(), session)
		return nil, err
	}
	session.meta.CurrentURL, _ = data["url"].(string)
	session.meta.Title, _ = data["title"].(string)
	session.meta.ViewportWidth = width
	session.meta.ViewportHeight = height
	if session.meta.CurrentURL == "" {
		s.refreshPageState(commandCtx, session)
	}
	if _, err := s.policy.validateURL(commandCtx, session.meta.CurrentURL); err != nil {
		_ = s.engine.Close(context.Background(), session)
		return nil, fmt.Errorf("browser navigation reached a blocked destination: %w", err)
	}
	session.meta.Generation++
	session.meta.SnapshotValid = false
	session.meta.SecretTainted = false
	session.meta.LastChallenges = challenges(session.meta.Title + " " + session.meta.CurrentURL)
	session.touch()
	if err := session.persist(); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"success": true, "sessionId": id, "currentUrl": session.meta.CurrentURL, "title": session.meta.Title,
		"profileName": profile, "viewport": map[string]interface{}{"width": width, "height": height},
		"challenges": session.meta.LastChallenges, "requiresHuman": blockedChallenge(session.meta.LastChallenges),
	}, nil
}

func recoverableBrowserDisconnect(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cdp response channel closed") ||
		strings.Contains(message, "target page, context or browser has been closed")
}

func (s *browserService) openEnginePageLocked(ctx context.Context, session *browserSession, args []string) (map[string]interface{}, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		data, err := s.engine.Run(ctx, session, args...)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !recoverableBrowserDisconnect(err) {
			return nil, err
		}
		_ = s.engine.Close(context.Background(), session)
	}
	return nil, lastErr
}

func (s *browserService) recoverPageLocked(ctx context.Context, session *browserSession) error {
	parsed, err := s.policy.validateURL(ctx, session.meta.CurrentURL)
	if err != nil {
		return fmt.Errorf("recover browser session destination: %w", err)
	}
	args := []string{"open", parsed.String()}
	_ = s.engine.Close(context.Background(), session)
	data, err := s.openEnginePageLocked(ctx, session, args)
	if err != nil {
		return fmt.Errorf("recover browser session: %w", err)
	}
	width, height := session.meta.ViewportWidth, session.meta.ViewportHeight
	if width == 0 {
		width = 1440
	}
	if height == 0 {
		height = 900
	}
	if _, err = s.engine.Run(ctx, session, "set", "viewport", strconv.Itoa(width), strconv.Itoa(height)); err != nil {
		return fmt.Errorf("recover browser session viewport: %w", err)
	}
	if current, ok := data["url"].(string); ok && current != "" {
		session.meta.CurrentURL = current
	}
	if title, ok := data["title"].(string); ok {
		session.meta.Title = title
	}
	if _, err = s.policy.validateURL(ctx, session.meta.CurrentURL); err != nil {
		_ = s.engine.Close(context.Background(), session)
		return fmt.Errorf("recovered browser navigation reached a blocked destination: %w", err)
	}
	session.meta.Generation++
	session.meta.SnapshotValid = false
	session.meta.SecretTainted = false
	session.touch()
	return session.persist()
}

func stableSnapshot(generation int, snapshot string, refs map[string]interface{}) (string, map[string]map[string]interface{}) {
	stable := make(map[string]map[string]interface{}, len(refs))
	for raw, value := range refs {
		ref := raw
		if strings.HasPrefix(ref, "@") {
			ref = strings.TrimPrefix(ref, "@")
		}
		if !regexp.MustCompile(`^e[1-9][0-9]*$`).MatchString(ref) {
			continue
		}
		key := fmt.Sprintf("s%d:%s", generation, ref)
		entry, _ := value.(map[string]interface{})
		copied := cloneOutput(entry)
		if copied == nil {
			copied = map[string]interface{}{}
		}
		copied["reference"] = key
		if editableControlRole(fmt.Sprint(copied["role"])) {
			delete(copied, "value")
			valuePattern := regexp.MustCompile(`(\[ref=` + regexp.QuoteMeta(ref) + `\])(?::[^\r\n]*)`)
			snapshot = valuePattern.ReplaceAllString(snapshot, `$1`)
		} else if secretNameRE.MatchString(fmt.Sprint(copied["name"])) {
			delete(copied, "value")
		}
		stable[key] = copied
		snapshot = strings.ReplaceAll(snapshot, "ref="+ref, "ref="+key)
	}
	return snapshot, stable
}

func editableControlRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "textbox", "searchbox", "combobox", "spinbutton", "slider":
		return true
	default:
		return false
	}
}

func (s *browserService) snapshot(ctx context.Context, config map[string]interface{}, secrets []string) (map[string]interface{}, error) {
	session, err := s.session(stringValue(config, "sessionId"))
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.meta.ClosedAt != nil {
		return nil, errors.New("browser session is closed")
	}
	depth := intValue(config, "maxDepth", 8)
	if depth < 1 || depth > 12 {
		return nil, errors.New("maxDepth must be between 1 and 12")
	}
	args := []string{"snapshot", "-c", "-d", strconv.Itoa(depth)}
	if boolValue(config, "interactiveOnly") {
		args = append(args, "-i")
	}
	data, err := s.engine.Run(ctx, session, args...)
	if recoverableBrowserDisconnect(err) {
		if recoverErr := s.recoverPageLocked(ctx, session); recoverErr != nil {
			return nil, recoverErr
		}
		data, err = s.engine.Run(ctx, session, args...)
	}
	if err != nil {
		return nil, err
	}
	rawSnapshot, _ := data["snapshot"].(string)
	rawRefs, _ := data["refs"].(map[string]interface{})
	session.meta.Generation++
	rawSnapshot = redact(rawSnapshot, secrets)
	text, truncated := boundedString(rawSnapshot, maxSnapshotBytes)
	text, refs := stableSnapshot(session.meta.Generation, text, rawRefs)
	session.refs = refs
	session.meta.SnapshotValid = true
	session.meta.LastChallenges = challenges(session.meta.Title + " " + session.meta.CurrentURL + " " + text)
	session.touch()
	if err := session.persist(); err != nil {
		return nil, err
	}
	elements := make([]map[string]interface{}, 0, len(refs))
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		elements = append(elements, refs[key])
	}
	return map[string]interface{}{
		"success": true, "sessionId": session.id, "generation": session.meta.Generation,
		"currentUrl": session.meta.CurrentURL, "title": session.meta.Title, "snapshot": text,
		"elements": elements, "truncated": truncated, "challenges": session.meta.LastChallenges,
		"requiresHuman": blockedChallenge(session.meta.LastChallenges),
	}, nil
}

func (s *browserService) read(ctx context.Context, config map[string]interface{}, secrets []string) (map[string]interface{}, error) {
	session, err := s.session(stringValue(config, "sessionId"))
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.meta.ClosedAt != nil {
		return nil, errors.New("browser session is closed")
	}
	limit := intValue(config, "maxCharacters", 50000)
	if limit < 1 || limit > 100000 {
		return nil, errors.New("maxCharacters must be between 1 and 100000")
	}
	data, err := s.engine.Run(ctx, session, "get", "text", "body")
	if recoverableBrowserDisconnect(err) {
		if recoverErr := s.recoverPageLocked(ctx, session); recoverErr != nil {
			return nil, recoverErr
		}
		data, err = s.engine.Run(ctx, session, "get", "text", "body")
	}
	if err != nil {
		return nil, err
	}
	text := redact(fmt.Sprint(data["text"]), secrets)
	text, truncated := boundedString(text, limit)
	session.meta.LastChallenges = challenges(session.meta.Title + " " + session.meta.CurrentURL + " " + text)
	session.touch()
	_ = session.persist()
	return map[string]interface{}{
		"success": true, "sessionId": session.id, "currentUrl": session.meta.CurrentURL,
		"title": session.meta.Title, "text": text, "truncated": truncated,
		"challenges": session.meta.LastChallenges, "requiresHuman": blockedChallenge(session.meta.LastChallenges),
	}, nil
}

func resolveTarget(session *browserSession, target string) (string, map[string]interface{}, error) {
	match := targetRE.FindStringSubmatch(target)
	if len(match) != 3 {
		return "", nil, errors.New("target must be an exact reference returned by browser-snapshot")
	}
	generation, _ := strconv.Atoi(match[1])
	if !session.meta.SnapshotValid || generation != session.meta.Generation {
		return "", nil, errors.New("stale element reference; take a new browser-snapshot")
	}
	ref := session.refs[target]
	if ref == nil {
		return "", nil, errors.New("element reference is not present in the latest snapshot")
	}
	return "@" + match[2], ref, nil
}

func validateMutation(config map[string]interface{}) error {
	if !boolValue(config, "writeAuthorized") {
		return errors.New("writeAuthorized must be true for browser mutations")
	}
	intent := stringValue(config, "intent")
	if len(intent) < 3 || len(intent) > maxIntentBytes {
		return errors.New("intent must contain 3 to 500 characters")
	}
	if stringValue(config, "idempotencyKey") == "" {
		return errors.New("idempotencyKey is required")
	}
	return nil
}

func (s *browserService) mutate(ctx context.Context, action string, config map[string]interface{}, secrets map[string]string) (map[string]interface{}, error) {
	if err := validateMutation(config); err != nil {
		return nil, err
	}
	session, err := s.session(stringValue(config, "sessionId"))
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.meta.ClosedAt != nil {
		return nil, errors.New("browser session is closed")
	}
	if blockedChallenge(session.meta.LastChallenges) {
		return nil, fmt.Errorf("automation stopped at %s challenge; human intervention is required", strings.Join(session.meta.LastChallenges, ", "))
	}
	value := stringValue(config, "value")
	credentialField := stringValue(config, "credentialField")
	isSecret := false
	if action == "fill-secret" {
		if value != "" || (credentialField != "username" && credentialField != "password") {
			return nil, errors.New("browser-fill-secret requires credentialField username or password and never accepts a literal value")
		}
		var ok bool
		value, ok = secrets[credentialField]
		if !ok || value == "" {
			return nil, fmt.Errorf("bound HTTP basic authentication credential does not contain field %q", credentialField)
		}
		isSecret = true
	} else if action == "fill" {
		if credentialField != "" {
			return nil, errors.New("browser-fill does not accept credentialField; use browser-fill-secret")
		}
		if value == "" {
			return nil, errors.New("value is required")
		}
	} else if secretNameRE.MatchString(value) {
		return nil, errors.New("browser-type and browser-select do not accept secret-like values; use browser-fill-secret")
	}
	if !isSecret {
		for _, boundSecret := range secrets {
			if value != "" && value == boundSecret {
				return nil, errors.New("bound credential values must be supplied through browser-fill-secret")
			}
		}
	}
	key := stringValue(config, "idempotencyKey")
	fingerprintValue := value
	if isSecret {
		fingerprintValue = "credential:" + credentialField
	}
	fp := fingerprint(action, stringValue(config, "target"), fingerprintValue, stringValue(config, "intent"))
	if duplicate, err := session.duplicate(action, key, fp); duplicate != nil || err != nil {
		return duplicate, err
	}
	rawRef, ref, err := resolveTarget(session, stringValue(config, "target"))
	if err != nil {
		return nil, err
	}
	if action == "fill" && secretNameRE.MatchString(fmt.Sprint(ref["name"])) {
		return nil, errors.New("secret-like controls must be filled through browser-fill-secret")
	}
	timeout := intValue(config, "timeoutSeconds", 25)
	commandCtx, cancel, err := commandContext(ctx, timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	engineAction := strings.TrimSuffix(action, "-secret")
	command := []string{engineAction, rawRef}
	if engineAction != "click" {
		command = append(command, value)
	}
	// Batch input keeps all entered text, including credential material, out of process argv.
	if _, err := s.engine.RunBatch(commandCtx, session, [][]string{command}); err != nil {
		return nil, errors.New(redact(err.Error(), secretValues(secrets)))
	}
	session.meta.SnapshotValid = false
	session.refs = map[string]map[string]interface{}{}
	if isSecret {
		session.meta.SecretTainted = true
	}
	s.refreshPageState(commandCtx, session)
	output := map[string]interface{}{
		"success": true, "duplicate": false, "sessionId": session.id, "action": "browser-" + action,
		"target": stringValue(config, "target"), "intent": stringValue(config, "intent"),
		"currentUrl": session.meta.CurrentURL, "title": session.meta.Title,
		"verificationRequired": true, "nextStep": "Take a new snapshot or read the page to verify the exact intended outcome.",
	}
	session.remember(action, key, fp, output)
	if err := session.persist(); err != nil {
		return nil, err
	}
	return output, nil
}

func (s *browserService) wait(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	session, err := s.session(stringValue(config, "sessionId"))
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.meta.ClosedAt != nil {
		return nil, errors.New("browser session is closed")
	}
	timeout := intValue(config, "timeoutSeconds", 25)
	commandCtx, cancel, err := commandContext(ctx, timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	var args []string
	switch stringValue(config, "condition") {
	case "duration":
		milliseconds := intValue(config, "milliseconds", 0)
		if milliseconds < 1 || milliseconds > 30000 {
			return nil, errors.New("milliseconds must be between 1 and 30000")
		}
		args = []string{"wait", strconv.Itoa(milliseconds)}
	case "text":
		text := stringValue(config, "text")
		if text == "" || len(text) > 2000 {
			return nil, errors.New("text must contain 1 to 2000 characters")
		}
		args = []string{"wait", "--text", text}
	case "load":
		state := stringValue(config, "loadState")
		if state == "" {
			state = "domcontentloaded"
		}
		if state != "load" && state != "domcontentloaded" && state != "networkidle" {
			return nil, errors.New("loadState must be load, domcontentloaded, or networkidle")
		}
		args = []string{"wait", "--load", state}
	default:
		return nil, errors.New("condition must be load, text, or duration")
	}
	if _, err := s.engine.Run(commandCtx, session, args...); err != nil {
		return nil, err
	}
	s.refreshPageState(commandCtx, session)
	_ = session.persist()
	return map[string]interface{}{"success": true, "sessionId": session.id, "condition": stringValue(config, "condition"), "currentUrl": session.meta.CurrentURL, "title": session.meta.Title}, nil
}

func (s *browserService) screenshot(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	session, err := s.session(stringValue(config, "sessionId"))
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.meta.ClosedAt != nil {
		return nil, errors.New("browser session is closed")
	}
	if session.meta.SecretTainted {
		return nil, errors.New("screenshots are blocked after credential filling in this session to prevent visual secret disclosure; navigate to a fresh page or close the session")
	}
	filename := fmt.Sprintf("%s-%d.png", session.id, time.Now().UTC().UnixNano())
	path := filepath.Join(s.workspace, "screenshots", filename)
	if _, err := s.engine.Run(ctx, session, "screenshot", path); err != nil {
		return nil, err
	}
	encoded, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxScreenshotBytes {
		return nil, fmt.Errorf("screenshot exceeds %d bytes", maxScreenshotBytes)
	}
	session.touch()
	_ = session.persist()
	return map[string]interface{}{
		"success": true, "sessionId": session.id, "mediaType": "image/png",
		"contentBase64": base64.StdEncoding.EncodeToString(encoded), "sizeBytes": len(encoded),
		"currentUrl": session.meta.CurrentURL, "title": session.meta.Title,
	}, nil
}

func (s *browserService) status(config map[string]interface{}) (map[string]interface{}, error) {
	session, err := s.session(stringValue(config, "sessionId"))
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.touch()
	_ = session.persist()
	state := "active"
	if session.meta.ClosedAt != nil {
		state = "closed"
	}
	return map[string]interface{}{
		"success": true, "sessionId": session.id, "state": state, "profileName": session.meta.ProfileName,
		"currentUrl": session.meta.CurrentURL, "title": session.meta.Title, "createdAt": session.meta.CreatedAt,
		"lastActivity": session.meta.LastActivity, "idleTimeoutSeconds": int(s.idleTimeout.Seconds()),
		"snapshotGeneration": session.meta.Generation, "snapshotValid": session.meta.SnapshotValid,
		"secretTainted": session.meta.SecretTainted, "challenges": session.meta.LastChallenges,
	}, nil
}

func (s *browserService) closeLocked(ctx context.Context, session *browserSession) error {
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	engineErr := s.engine.Close(commandCtx, session)
	now := time.Now().UTC()
	session.meta.ClosedAt = &now
	session.meta.SnapshotValid = false
	session.refs = map[string]map[string]interface{}{}
	session.touch()
	persistErr := session.persist()
	if session.meta.ProfileName == "" {
		// Keep only the non-secret receipt for status/idempotency; engine state is removed.
		for _, name := range []string{".cache", ".config", ".agent-browser"} {
			_ = os.RemoveAll(filepath.Join(session.runtimeDir, name))
		}
	}
	if engineErr != nil {
		return engineErr
	}
	return persistErr
}

func (s *browserService) close(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	session, err := s.session(stringValue(config, "sessionId"))
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	already := session.meta.ClosedAt != nil
	if !already {
		if err := s.closeLocked(ctx, session); err != nil {
			return nil, err
		}
	}
	return map[string]interface{}{"success": true, "sessionId": session.id, "closed": true, "alreadyClosed": already, "profilePreserved": session.meta.ProfileName != ""}, nil
}

type actionExecutor struct {
	action  string
	service *browserService
}

func (e *actionExecutor) Type() string { return e.action }

func (e *actionExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (result *executor.StepResult, err error) {
	config := configMap(step, r)
	secrets, secretErr := bindingSecrets(config, r)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("browser action failed safely")
		}
		if err != nil {
			err = errors.New(redact(err.Error(), secretValues(secrets)))
		}
	}()
	if secretErr != nil {
		return nil, secretErr
	}
	var output map[string]interface{}
	switch e.action {
	case "browser-health":
		engineErr := e.service.engine.Ready(ctx)
		status := "ready"
		if engineErr != nil {
			status = "not_ready"
		}
		output = map[string]interface{}{
			"status": status, "skillId": skillID, "version": skillVersion,
			"engine": "agent-browser", "chromium": true, "workspaceConfigured": e.service.workspace != "",
			"networkProxyReady": e.service.proxy != nil, "idleCleanupSeconds": int(e.service.idleTimeout.Seconds()),
		}
		if engineErr != nil {
			output["error"] = engineErr.Error()
		}
	case "browser-open":
		output, err = e.service.open(ctx, config)
	case "browser-snapshot":
		output, err = e.service.snapshot(ctx, config, secretValues(secrets))
	case "browser-read":
		output, err = e.service.read(ctx, config, secretValues(secrets))
	case "browser-click":
		output, err = e.service.mutate(ctx, "click", config, secrets)
	case "browser-fill":
		output, err = e.service.mutate(ctx, "fill", config, secrets)
	case "browser-fill-secret":
		output, err = e.service.mutate(ctx, "fill-secret", config, secrets)
	case "browser-type":
		output, err = e.service.mutate(ctx, "type", config, secrets)
	case "browser-select":
		output, err = e.service.mutate(ctx, "select", config, secrets)
	case "browser-wait":
		output, err = e.service.wait(ctx, config)
	case "browser-screenshot":
		output, err = e.service.screenshot(ctx, config)
	case "browser-session-status":
		output, err = e.service.status(config)
	case "browser-close":
		output, err = e.service.close(ctx, config)
	default:
		err = fmt.Errorf("unsupported browser action %q", e.action)
	}
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: output}, nil
}

func schema(action, name, description string) *resolver.NodeSchema {
	return resolver.NewSchemaBuilder(action).
		WithName(name).
		WithCategory("browser").
		WithIcon("globe").
		WithDescription(description).
		Build()
}

var actionSchemas = map[string]*resolver.NodeSchema{
	"browser-health":         schema("browser-health", "Browser Health", "Check browser engine and workspace readiness"),
	"browser-open":           schema("browser-open", "Open Browser Page", "Open a validated URL in an isolated session"),
	"browser-snapshot":       schema("browser-snapshot", "Snapshot Browser Page", "Capture stable accessible element references"),
	"browser-read":           schema("browser-read", "Read Browser Page", "Read bounded rendered page text"),
	"browser-click":          schema("browser-click", "Click Browser Element", "Click an exact authorized snapshot reference"),
	"browser-fill":           schema("browser-fill", "Fill Browser Control", "Fill an exact non-secret form control"),
	"browser-fill-secret":    schema("browser-fill-secret", "Fill Browser Login", "Fill an exact login control from an authorized credential"),
	"browser-type":           schema("browser-type", "Type in Browser Control", "Type non-secret text into an exact control"),
	"browser-select":         schema("browser-select", "Select Browser Option", "Select an exact option in a control"),
	"browser-wait":           schema("browser-wait", "Wait in Browser", "Wait for a bounded page condition"),
	"browser-screenshot":     schema("browser-screenshot", "Capture Browser Screenshot", "Capture a bounded non-secret PNG"),
	"browser-session-status": schema("browser-session-status", "Browser Session Status", "Inspect bounded non-secret session state"),
	"browser-close":          schema("browser-close", "Close Browser Session", "Close a browser session and child processes"),
}

func envDuration(name string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func newProductionService() (*browserService, error) {
	workspace := strings.TrimSpace(os.Getenv("BROWSER_WORKSPACE"))
	idleTimeout := envDuration("BROWSER_IDLE_TIMEOUT", defaultIdleTimeout)
	policy := newDestinationPolicy(os.Getenv("BROWSER_ALLOWED_HOSTS"), strings.EqualFold(os.Getenv("BROWSER_ALLOW_PRIVATE_NETWORKS"), "true"))
	if err := policy.addAllowedPorts(os.Getenv("BROWSER_ALLOWED_PORTS")); err != nil {
		return nil, err
	}
	proxy, err := startSafeProxy(policy)
	if err != nil {
		return nil, fmt.Errorf("start browser network guard: %w", err)
	}
	engine := &agentBrowserEngine{
		binary: strings.TrimSpace(os.Getenv("AGENT_BROWSER_BINARY")), executable: strings.TrimSpace(os.Getenv("CHROMIUM_EXECUTABLE")), proxyURL: proxy.URL(), idleTimeout: idleTimeout,
	}
	if engine.binary == "" {
		engine.binary = "agent-browser"
	}
	if engine.executable == "" {
		engine.executable = "/usr/bin/chromium"
	}
	service, err := newBrowserService(workspace, policy, idleTimeout, engine)
	if err != nil {
		_ = proxy.Close(context.Background())
		return nil, err
	}
	service.proxy = proxy
	return service, nil
}

func main() {
	service, err := newProductionService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "browser skill configuration error: %v\n", err)
		os.Exit(1)
	}
	defer service.Shutdown(context.Background())
	port := strings.TrimSpace(os.Getenv("SKILL_PORT"))
	if port == "" {
		port = defaultPort
	}
	server := skillgrpc.NewSkillServer(skillID, skillVersion)
	for _, action := range []string{
		"browser-health", "browser-open", "browser-snapshot", "browser-read", "browser-click", "browser-fill", "browser-fill-secret",
		"browser-type", "browser-select", "browser-wait", "browser-screenshot", "browser-session-status", "browser-close",
	} {
		server.RegisterExecutorWithSchema(action, &actionExecutor{action: action, service: service}, actionSchemas[action])
	}
	fmt.Printf("Starting %s gRPC server on port %s\n", skillID, port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "browser skill server failed: %v\n", err)
		os.Exit(1)
	}
}
