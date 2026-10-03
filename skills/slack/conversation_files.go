package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumSlackFileBytes = 2 << 20

type conversationFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
}
type conversationFileContent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType,omitempty"`
	Data      []byte `json:"data,omitempty"`
	Status    string `json:"status,omitempty"`
}
type slackFile struct {
	ID         string                                 `json:"id"`
	Name       string                                 `json:"name"`
	MediaType  string                                 `json:"mimetype"`
	Size       int64                                  `json:"size"`
	PrivateURL string                                 `json:"url_private_download"`
	PreviewURL string                                 `json:"url_private"`
	Shares     map[string]map[string][]slackFileShare `json:"shares"`
}
type slackFileShare struct {
	Timestamp       string `json:"ts"`
	ThreadTimestamp string `json:"thread_ts"`
}
type slackFileResponse struct {
	OK        bool      `json:"ok"`
	Error     string    `json:"error"`
	File      slackFile `json:"file"`
	UploadURL string    `json:"upload_url"`
	FileID    string    `json:"file_id"`
}

func normalizedSlackFiles(files []slackFile) []conversationFile {
	var result []conversationFile
	seen := map[string]bool{}
	for _, file := range files {
		if file.ID == "" || seen[file.ID] || len(result) >= 8 {
			continue
		}
		seen[file.ID] = true
		name := file.Name
		if name == "" {
			name = file.ID
		}
		mediaType := file.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		result = append(result, conversationFile{ID: file.ID, Name: name, MediaType: mediaType, SizeBytes: file.Size})
	}
	return result
}

// Signed event descriptors contain no private URLs; resolve them only with the
// exact bound installation's bot token inside the background inbox worker.
func (a *slackAdapter) readAttachment(ctx context.Context, token string, envelope *adapterEnvelope) (map[string]interface{}, error) {
	if envelope.Attachment == nil || envelope.Event == nil || envelope.Endpoint == nil || envelope.Event.ExternalConversationID != envelope.Endpoint.Address {
		return nil, errors.New("Slack attachment origin is invalid")
	}
	file := envelope.Attachment
	present := false
	for _, candidate := range envelope.Event.Attachments {
		present = present || candidate == *file
	}
	if !present || file.ID == "" {
		return nil, errors.New("Slack attachment was not in the verified event")
	}
	result := map[string]interface{}{"id": file.ID, "status": "unavailable"}
	raw, status, _, err := a.slackJSON(ctx, token, http.MethodGet, "/files.info", url.Values{"file": {file.ID}}, nil)
	if err != nil || status == 429 || status >= 500 {
		return nil, errors.New("Slack file lookup temporarily unavailable")
	}
	var response slackFileResponse
	if json.Unmarshal(raw, &response) != nil {
		return nil, errors.New("Slack file lookup invalid")
	}
	if !response.OK {
		if response.Error == "missing_scope" {
			result["status"] = "missing_scope"
		}
		return result, nil
	}
	if response.File.ID != file.ID {
		return nil, errors.New("Slack returned a different file")
	}
	if response.File.Size > maximumSlackFileBytes {
		result["status"] = "size_limit"
		return result, nil
	}
	link := firstNonEmpty(response.File.PrivateURL, response.File.PreviewURL)
	if !a.allowedFileURL(link) {
		return result, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return result, nil
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client := *a.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !a.allowedFileURL(req.URL.String()) {
			return errors.New("invalid Slack file redirect")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
	download, err := client.Do(request)
	if err != nil {
		return nil, errors.New("Slack file download temporarily unavailable")
	}
	defer download.Body.Close()
	if download.StatusCode == 429 || download.StatusCode >= 500 {
		return nil, errors.New("Slack file download temporarily unavailable")
	}
	if download.StatusCode != http.StatusOK {
		return result, nil
	}
	data, err := io.ReadAll(io.LimitReader(download.Body, maximumSlackFileBytes+1))
	if err != nil {
		return nil, errors.New("Slack file download interrupted")
	}
	if len(data) > maximumSlackFileBytes {
		result["status"] = "size_limit"
		return result, nil
	}
	if len(data) == 0 {
		return result, nil
	}
	result["status"] = "supplied"
	result["data"] = data
	result["name"] = file.Name
	result["mediaType"] = firstNonEmpty(response.File.MediaType, file.MediaType, "application/octet-stream")
	return result, nil
}
func (a *slackAdapter) allowedFileURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return false
	}
	// Custom base URLs are injected only by the host/test harness, never by events.
	base, _ := url.Parse(a.baseURL)
	if base != nil && base.Hostname() != "slack.com" && parsed.Host == base.Host && parsed.Scheme == base.Scheme {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	return parsed.Scheme == "https" && (parsed.Port() == "" || parsed.Port() == "443") && (host == "slack.com" || strings.HasSuffix(host, ".slack.com") || host == "slack-files.com" || strings.HasSuffix(host, ".slack-files.com"))
}

type slackUploadCheckpoint struct {
	DeliveryID string              `json:"deliveryId"`
	Channel    string              `json:"channel"`
	Thread     string              `json:"thread"`
	Files      []slackUploadedFile `json:"files"`
}
type slackUploadedFile struct {
	ArtifactID string `json:"artifactId"`
	Digest     string `json:"digest"`
	FileID     string `json:"fileId"`
	Title      string `json:"title"`
}

func uploadCheckpoint(envelope *adapterEnvelope) (slackUploadCheckpoint, error) {
	checkpoint := slackUploadCheckpoint{DeliveryID: envelope.Delivery.ID, Channel: envelope.Endpoint.Address, Thread: envelope.Delivery.ExternalThreadID}
	if raw, ok := envelope.Delivery.Progress["slackFiles"]; ok {
		encoded, _ := json.Marshal(raw)
		if json.Unmarshal(encoded, &checkpoint) != nil || checkpoint.DeliveryID != envelope.Delivery.ID || checkpoint.Channel != envelope.Endpoint.Address || checkpoint.Thread != envelope.Delivery.ExternalThreadID || len(checkpoint.Files) > len(envelope.Attachments) {
			return checkpoint, errors.New("Slack upload checkpoint drifted")
		}
		for i, file := range checkpoint.Files {
			source := envelope.Attachments[i]
			if file.ArtifactID != source.ID || file.Digest != fmt.Sprintf("%x", sha256.Sum256(source.Data)) || file.Title != source.Name || file.FileID == "" {
				return checkpoint, errors.New("Slack upload checkpoint drifted")
			}
		}
	}
	return checkpoint, nil
}
func fileRetry(checkpoint slackUploadCheckpoint, code string) map[string]interface{} {
	result := retryDelivery(code, "Slack file delivery is pending.", time.Second)
	result["progress"] = map[string]interface{}{"slackFiles": checkpoint}
	return result
}
func (a *slackAdapter) lookupFileDelivery(ctx context.Context, token string, envelope *adapterEnvelope) (map[string]interface{}, error) {
	checkpoint, err := uploadCheckpoint(envelope)
	if err != nil {
		return nil, err
	}
	if len(checkpoint.Files) != len(envelope.Attachments) {
		return map[string]interface{}{"status": "not_found"}, nil
	}
	receipt := ""
	for _, uploaded := range checkpoint.Files {
		raw, status, _, err := a.slackJSON(ctx, token, http.MethodGet, "/files.info", url.Values{"file": {uploaded.FileID}}, nil)
		var response slackFileResponse
		if err != nil || status != http.StatusOK || json.Unmarshal(raw, &response) != nil || !response.OK || response.File.ID != uploaded.FileID {
			return map[string]interface{}{"status": "unknown"}, nil
		}
		found := false
		for _, visibility := range response.File.Shares {
			for _, share := range visibility[checkpoint.Channel] {
				if share.Timestamp != "" && (checkpoint.Thread == "" || share.ThreadTimestamp == checkpoint.Thread || share.Timestamp == checkpoint.Thread) {
					found = true
					receipt = share.Timestamp
				}
			}
		}
		if !found {
			return map[string]interface{}{"status": "not_found"}, nil
		}
	}
	return map[string]interface{}{"status": "found", "providerMessageId": receipt}, nil
}
func (a *slackAdapter) deliverFiles(ctx context.Context, token string, envelope *adapterEnvelope) (map[string]interface{}, error) {
	if len(envelope.Attachments) > 8 {
		return failedDelivery("size_limit", "Too many files for Slack."), nil
	}
	checkpoint, err := uploadCheckpoint(envelope)
	if err != nil {
		return nil, err
	}
	size := 0
	for _, file := range envelope.Attachments {
		size += len(file.Data)
		if file.ID == "" || file.Name == "" || len(file.Data) == 0 || size > maximumSlackFileBytes {
			return failedDelivery("size_limit", "Slack attachments exceed the supported size."), nil
		}
	}
	if len(checkpoint.Files) < len(envelope.Attachments) {
		for _, file := range envelope.Attachments[len(checkpoint.Files):] {
			raw, status, _, err := a.slackJSON(ctx, token, http.MethodPost, "/files.getUploadURLExternal", nil, map[string]interface{}{"filename": file.Name, "length": len(file.Data)})
			var response slackFileResponse
			if err != nil || status == 429 || status >= 500 {
				return fileRetry(checkpoint, "slack_unavailable"), nil
			}
			if json.Unmarshal(raw, &response) != nil || !response.OK {
				if response.Error == "missing_scope" {
					return failedDelivery("missing_scope", "Slack requires files:write to send attachments."), nil
				}
				return fileRetry(checkpoint, "slack_unavailable"), nil
			}
			if !a.allowedFileURL(response.UploadURL) || response.FileID == "" {
				return failedDelivery("invalid_response", "Slack returned an invalid file upload target."), nil
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, response.UploadURL, bytes.NewReader(file.Data))
			req.Header.Set("Content-Type", "application/octet-stream")
			client := *a.client
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			uploaded, err := client.Do(req)
			if err != nil {
				return fileRetry(checkpoint, "slack_unavailable"), nil
			}
			io.Copy(io.Discard, io.LimitReader(uploaded.Body, 4096))
			uploaded.Body.Close()
			if uploaded.StatusCode < 200 || uploaded.StatusCode >= 300 {
				return fileRetry(checkpoint, "slack_unavailable"), nil
			}
			checkpoint.Files = append(checkpoint.Files, slackUploadedFile{ArtifactID: file.ID, Digest: fmt.Sprintf("%x", sha256.Sum256(file.Data)), FileID: response.FileID, Title: file.Name})
		}
		// Persist IDs before making any file visible. Retries reuse these uploads.
		return fileRetry(checkpoint, "file_upload_ready"), nil
	}
	lookup, err := a.lookupFileDelivery(ctx, token, envelope)
	if err != nil {
		return nil, err
	}
	if lookup["status"] == "found" {
		return map[string]interface{}{"outcome": "delivered", "providerMessageId": lookup["providerMessageId"]}, nil
	}
	if lookup["status"] == "unknown" {
		return fileRetry(checkpoint, "confirmation_pending"), nil
	}
	files := make([]map[string]string, 0, len(checkpoint.Files))
	for _, file := range checkpoint.Files {
		files = append(files, map[string]string{"id": file.FileID, "title": file.Title})
	}
	raw, status, _, err := a.slackJSON(ctx, token, http.MethodPost, "/files.completeUploadExternal", nil, map[string]interface{}{"files": files, "channel_id": checkpoint.Channel, "thread_ts": checkpoint.Thread, "initial_comment": markdownToSlack(envelope.Message.Content)})
	var response slackFileResponse
	if err != nil || status == 429 || status >= 500 || json.Unmarshal(raw, &response) != nil {
		return fileRetry(checkpoint, "confirmation_pending"), nil
	}
	if !response.OK {
		return fileRetry(checkpoint, "confirmation_pending"), nil
	}
	return map[string]interface{}{"outcome": "delivered", "providerMessageId": "file:" + checkpoint.Files[0].FileID}, nil
}
