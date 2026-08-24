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
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	slackSocketModeConnectorEndpoint = "slack.callback.socket_mode"
	slackAppTokenCredential          = "slack_app_token"
	slackSigningSecretCredential     = "slack_signing_secret"
	maximumSocketEnvelopeBytes       = 1 << 20
)

type slackSocketModeConfig struct {
	AppToken      string
	SigningSecret string
	CallbackURL   string
	APIBaseURL    string
	HTTPClient    *http.Client
	Dialer        *websocket.Dialer
	Now           func() time.Time
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
	return runSlackSocketModeConnector(ctx, slackSocketModeConfig{
		AppToken: string(appToken), SigningSecret: string(signingSecret),
		CallbackURL: strings.TrimSpace(os.Getenv("OPENSEAL_CALLBACK_URL")),
		APIBaseURL:  strings.TrimSpace(os.Getenv("SLACK_API_BASE_URL")),
	})
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
		strings.TrimSpace(config.CallbackURL) == "" {
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

func forwardSlackSocketInteraction(ctx context.Context, config slackSocketModeConfig, envelope slackSocketModeEnvelope) (slackSocketModeAcknowledgement, error) {
	form := url.Values{"payload": []string{string(envelope.Payload)}}.Encode()
	timestamp := strconv.FormatInt(config.Now().UTC().Unix(), 10)
	signatureBase := "v0:" + timestamp + ":" + form
	mac := hmac.New(sha256.New, []byte(config.SigningSecret))
	_, _ = mac.Write([]byte(signatureBase))
	signature := "v0=" + hex.EncodeToString(mac.Sum(nil))

	requestContext, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, config.CallbackURL, strings.NewReader(form))
	if err != nil {
		return slackSocketModeAcknowledgement{}, errors.New("create callback ingress request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-Slack-Request-Timestamp", timestamp)
	request.Header.Set("X-Slack-Signature", signature)
	response, err := config.HTTPClient.Do(request)
	if err != nil {
		return slackSocketModeAcknowledgement{}, errors.New("deliver callback ingress request")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumSocketEnvelopeBytes+1))
	if err != nil || len(body) > maximumSocketEnvelopeBytes || response.StatusCode < 200 || response.StatusCode >= 300 {
		return slackSocketModeAcknowledgement{}, errors.New("callback ingress rejected Socket Mode envelope")
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
