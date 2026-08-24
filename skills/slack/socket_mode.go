package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	slackSocketModeConnectorEndpoint = "slack.callback.socket_mode"
	slackAppTokenCredential          = "slack_app_token"
	slackSigningSecretCredential     = "slack_signing_secret"
	slackCallbackRoutesFile          = "callback_routes.json"
	maximumSocketEnvelopeBytes       = 1 << 20
)

type slackSocketModeConfig struct {
	AppToken       string
	SigningSecret  string
	CallbackRoutes map[string]string
	APIBaseURL     string
	HTTPClient     *http.Client
	Dialer         *websocket.Dialer
	Now            func() time.Time
}

type slackSocketModeEnvelope struct {
	EnvelopeID             string          `json:"envelope_id"`
	Type                   string          `json:"type"`
	AcceptsResponsePayload bool            `json:"accepts_response_payload"`
	Payload                json.RawMessage `json:"payload"`
}

type slackSocketModeAcknowledgement struct {
	EnvelopeID string          `json:"envelope_id"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

func runSlackSocketModeConnectorFromEnvironment(ctx context.Context) error {
	credentialDir := strings.TrimSpace(os.Getenv("OPENSEAL_CREDENTIAL_DIR"))
	if credentialDir == "" {
		return errors.New("connector credential directory is required")
	}
	appToken, err := readConnectorCredential(credentialDir, slackAppTokenCredential)
	if err != nil {
		return err
	}
	signingSecret, err := readConnectorCredential(credentialDir, slackSigningSecretCredential)
	if err != nil {
		return err
	}
	defer clearBytes(appToken)
	defer clearBytes(signingSecret)
	routesFile := strings.TrimSpace(os.Getenv("OPENSEAL_CALLBACK_ROUTES_FILE"))
	if routesFile == "" {
		routesFile = filepath.Join(credentialDir, slackCallbackRoutesFile)
	}
	routes, err := readSlackCallbackRoutes(routesFile)
	if err != nil {
		return err
	}
	return runSlackSocketModeConnector(ctx, slackSocketModeConfig{
		AppToken: string(appToken), SigningSecret: string(signingSecret),
		CallbackRoutes: routes,
		APIBaseURL:     strings.TrimSpace(os.Getenv("SLACK_API_BASE_URL")),
	})
}

func readSlackCallbackRoutes(path string) (map[string]string, error) {
	encoded, err := os.ReadFile(path)
	if err != nil || len(encoded) > maximumSocketEnvelopeBytes {
		return nil, errors.New("connector callback routes are unavailable")
	}
	var routes map[string]string
	if json.Unmarshal(encoded, &routes) != nil || len(routes) == 0 {
		return nil, errors.New("connector callback routes are invalid")
	}
	for destinationID, callbackURL := range routes {
		parsed, parseErr := url.Parse(strings.TrimSpace(callbackURL))
		if strings.TrimSpace(destinationID) == "" || parseErr != nil || parsed.Host == "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, errors.New("connector callback routes are invalid")
		}
	}
	return routes, nil
}

func readConnectorCredential(directory, name string) ([]byte, error) {
	value, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil || strings.TrimSpace(string(value)) == "" {
		return nil, fmt.Errorf("connector credential %s is unavailable", name)
	}
	return bytes.TrimSpace(value), nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func runSlackSocketModeConnector(ctx context.Context, config slackSocketModeConfig) error {
	if strings.TrimSpace(config.AppToken) == "" || strings.TrimSpace(config.SigningSecret) == "" ||
		len(config.CallbackRoutes) == 0 {
		return errors.New("complete Socket Mode connector configuration is required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 2500 * time.Millisecond}
	}
	if config.Dialer == nil {
		config.Dialer = websocket.DefaultDialer
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if strings.TrimSpace(config.APIBaseURL) == "" {
		config.APIBaseURL = slackBaseURL
	}
	backoff := time.Second
	for ctx.Err() == nil {
		connectionURL, err := openSlackSocketModeConnection(ctx, config)
		if err == nil {
			err = consumeSlackSocketModeConnection(ctx, config, connectionURL)
		}
		if ctx.Err() != nil {
			return nil
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	return nil
}

func openSlackSocketModeConnection(ctx context.Context, config slackSocketModeConfig) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(config.APIBaseURL, "/")+"/apps.connections.open", nil)
	if err != nil {
		return "", errors.New("create Socket Mode connection request")
	}
	request.Header.Set("Authorization", "Bearer "+config.AppToken)
	response, err := config.HTTPClient.Do(request)
	if err != nil {
		return "", errors.New("open Socket Mode connection")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumSocketEnvelopeBytes+1))
	if err != nil || len(body) > maximumSocketEnvelopeBytes {
		return "", errors.New("read Socket Mode connection response")
	}
	var result struct {
		OK    bool   `json:"ok"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || json.Unmarshal(body, &result) != nil ||
		!result.OK || !strings.HasPrefix(result.URL, "wss://") && !strings.HasPrefix(result.URL, "ws://") {
		return "", errors.New("Slack rejected Socket Mode connection")
	}
	return result.URL, nil
}

func consumeSlackSocketModeConnection(ctx context.Context, config slackSocketModeConfig, connectionURL string) error {
	connection, _, err := config.Dialer.DialContext(ctx, connectionURL, nil)
	if err != nil {
		return errors.New("connect Socket Mode websocket")
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetReadDeadline(deadline)
	}
	for ctx.Err() == nil {
		_, body, err := connection.ReadMessage()
		if err != nil {
			return errors.New("read Socket Mode envelope")
		}
		if len(body) == 0 || len(body) > maximumSocketEnvelopeBytes {
			return errors.New("Socket Mode envelope is invalid")
		}
		var envelope slackSocketModeEnvelope
		if json.Unmarshal(body, &envelope) != nil {
			return errors.New("Socket Mode envelope is invalid")
		}
		switch envelope.Type {
		case "hello":
			continue
		case "disconnect":
			return errors.New("Slack requested Socket Mode reconnect")
		case "interactive":
			if strings.TrimSpace(envelope.EnvelopeID) == "" || len(envelope.Payload) == 0 {
				return errors.New("interactive Socket Mode envelope is invalid")
			}
			acknowledgement, err := forwardSlackSocketInteraction(ctx, config, envelope)
			if err != nil {
				// Do not acknowledge failed ingress. Slack will retry the envelope;
				// the callback receipt store deduplicates successful retries.
				continue
			}
			if err := connection.WriteJSON(acknowledgement); err != nil {
				return errors.New("acknowledge Socket Mode envelope")
			}
		case "events_api":
			if strings.TrimSpace(envelope.EnvelopeID) == "" || len(envelope.Payload) == 0 {
				return errors.New("Events API Socket Mode envelope is invalid")
			}
			if err := forwardSlackSocketEvents(ctx, config, envelope); err != nil {
				// A failed durable ingress is deliberately left unacknowledged so
				// Slack retries the envelope. OpenSeal deduplicates accepted events.
				continue
			}
			if err := connection.WriteJSON(slackSocketModeAcknowledgement{EnvelopeID: envelope.EnvelopeID}); err != nil {
				return errors.New("acknowledge Events API Socket Mode envelope")
			}
		default:
			if strings.TrimSpace(envelope.EnvelopeID) != "" {
				if err := connection.WriteJSON(slackSocketModeAcknowledgement{EnvelopeID: envelope.EnvelopeID}); err != nil {
					return errors.New("acknowledge unsupported Socket Mode envelope")
				}
			}
		}
	}
	return nil
}

func forwardSlackSocketEvents(ctx context.Context, config slackSocketModeConfig, envelope slackSocketModeEnvelope) error {
	routes := make([]string, 0)
	for destinationID, callbackURL := range config.CallbackRoutes {
		if strings.HasPrefix(destinationID, "conversation_gateway:") {
			routes = append(routes, callbackURL)
		}
	}
	if len(routes) == 0 {
		return errors.New("Socket Mode conversation gateway is unavailable")
	}
	sort.Strings(routes)
	for _, callbackURL := range routes {
		if _, err := forwardSlackSocketPayload(ctx, config, callbackURL, "application/json", envelope.Payload); err != nil {
			return err
		}
	}
	return nil
}

func forwardSlackSocketInteraction(ctx context.Context, config slackSocketModeConfig, envelope slackSocketModeEnvelope) (slackSocketModeAcknowledgement, error) {
	callbackURL, err := slackSocketCallbackURL(envelope.Payload, config.CallbackRoutes)
	if err != nil {
		return slackSocketModeAcknowledgement{}, err
	}
	form := url.Values{"payload": []string{string(envelope.Payload)}}.Encode()
	body, err := forwardSlackSocketPayload(ctx, config, callbackURL, "application/x-www-form-urlencoded", []byte(form))
	if err != nil {
		return slackSocketModeAcknowledgement{}, err
	}
	acknowledgement := slackSocketModeAcknowledgement{EnvelopeID: envelope.EnvelopeID}
	if envelope.AcceptsResponsePayload && len(bytes.TrimSpace(body)) > 0 {
		if !json.Valid(body) {
			return slackSocketModeAcknowledgement{}, errors.New("callback ingress response is invalid")
		}
		acknowledgement.Payload = append(json.RawMessage(nil), bytes.TrimSpace(body)...)
	}
	return acknowledgement, nil
}

func forwardSlackSocketPayload(ctx context.Context, config slackSocketModeConfig, callbackURL, contentType string, payloadBody []byte) ([]byte, error) {
	timestamp := strconv.FormatInt(config.Now().UTC().Unix(), 10)
	signatureBase := "v0:" + timestamp + ":" + string(payloadBody)
	mac := hmac.New(sha256.New, []byte(config.SigningSecret))
	_, _ = mac.Write([]byte(signatureBase))
	signature := "v0=" + hex.EncodeToString(mac.Sum(nil))

	requestContext, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, callbackURL, bytes.NewReader(payloadBody))
	if err != nil {
		return nil, errors.New("create callback ingress request")
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Slack-Request-Timestamp", timestamp)
	request.Header.Set("X-Slack-Signature", signature)
	response, err := config.HTTPClient.Do(request)
	if err != nil {
		return nil, errors.New("deliver callback ingress request")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maximumSocketEnvelopeBytes+1))
	if err != nil || len(responseBody) > maximumSocketEnvelopeBytes || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("callback ingress rejected Socket Mode envelope")
	}
	return responseBody, nil
}

func slackSocketCallbackURL(payload json.RawMessage, routes map[string]string) (string, error) {
	var interaction struct {
		Type    string `json:"type"`
		Actions []struct {
			Value string `json:"value"`
		} `json:"actions"`
		View struct {
			PrivateMetadata string `json:"private_metadata"`
		} `json:"view"`
	}
	if json.Unmarshal(payload, &interaction) != nil {
		return "", errors.New("Socket Mode interaction is invalid")
	}
	var reviewed slackApprovalValue
	switch interaction.Type {
	case "block_actions":
		if len(interaction.Actions) != 1 || json.Unmarshal([]byte(interaction.Actions[0].Value), &reviewed) != nil {
			return "", errors.New("Socket Mode interaction route is invalid")
		}
	case "view_submission":
		var metadata slackApprovalRevisionMetadata
		if json.Unmarshal([]byte(interaction.View.PrivateMetadata), &metadata) != nil {
			return "", errors.New("Socket Mode interaction route is invalid")
		}
		reviewed = metadata.Reviewed
	default:
		return "", errors.New("Socket Mode interaction type is unsupported")
	}
	if callbackURL := strings.TrimSpace(routes[reviewed.DestinationID]); callbackURL != "" {
		return callbackURL, nil
	}
	// Cards created before destination routing was introduced remain safe only
	// when the shared Slack connection has exactly one possible destination.
	if strings.TrimSpace(reviewed.DestinationID) == "" {
		callbackURL := ""
		for destinationID, candidate := range routes {
			if strings.HasPrefix(destinationID, "conversation_gateway:") {
				continue
			}
			if callbackURL != "" {
				return "", errors.New("Socket Mode interaction destination is unavailable")
			}
			callbackURL = strings.TrimSpace(candidate)
		}
		if callbackURL != "" {
			return callbackURL, nil
		}
	}
	return "", errors.New("Socket Mode interaction destination is unavailable")
}
