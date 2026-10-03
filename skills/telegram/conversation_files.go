package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Match the host's canonical artifact import/export budget, below Telegram's
// public getFile20MB/sendPhoto10MB/sendDocument50MB provider limits.
const maximumTelegramFileBytes = 2 << 20
const maximumTelegramAttachments = 8

type telegramConversationFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
}
type telegramConversationFileContent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType,omitempty"`
	Data      []byte `json:"data,omitempty"`
	Status    string `json:"status,omitempty"`
}

func normalizedTelegramFiles(message telegramMessage) []telegramConversationFile {
	result := make([]telegramConversationFile, 0)
	seen := map[string]bool{}
	add := func(file *telegramMedia, fallbackName, fallbackType string) {
		if file == nil || file.FileID == "" || seen[file.FileID] || len(result) >= maximumTelegramAttachments {
			return
		}
		seen[file.FileID] = true
		name := file.FileName
		if name == "" {
			name = fallbackName
		}
		mediaType := file.MediaType
		if mediaType == "" {
			mediaType = fallbackType
		}
		result = append(result, telegramConversationFile{ID: file.FileID, Name: name, MediaType: mediaType, SizeBytes: file.FileSize})
	}
	if len(message.Photo) > 0 {
		largest := message.Photo[0]
		withinBudget := largest.FileSize <= maximumTelegramFileBytes
		for _, photo := range message.Photo[1:] {
			candidateWithin := photo.FileSize <= maximumTelegramFileBytes
			if (candidateWithin && !withinBudget) || (candidateWithin == withinBudget && (photo.Width*photo.Height > largest.Width*largest.Height || (photo.Width*photo.Height == largest.Width*largest.Height && photo.FileSize > largest.FileSize))) {
				largest = photo
				withinBudget = candidateWithin
			}
		}
		add(&largest, "photo.jpg", "image/jpeg")
	}
	add(message.Document, "document", "application/octet-stream")
	add(message.Animation, "animation.mp4", "video/mp4")
	add(message.Audio, "audio.mp3", "audio/mpeg")
	add(message.Voice, "voice.ogg", "audio/ogg")
	add(message.Video, "video.mp4", "video/mp4")
	add(message.VideoNote, "video-note.mp4", "video/mp4")
	if message.Sticker != nil {
		name, mediaType := "sticker.webp", "image/webp"
		if message.Sticker.IsAnimated {
			name = "sticker.tgs"
			mediaType = "application/x-tgsticker"
		} else if message.Sticker.IsVideo {
			name = "sticker.webm"
			mediaType = "video/webm"
		}
		add(message.Sticker, name, mediaType)
	}
	return result
}
func (a *telegramConversationAdapter) readAttachment(ctx context.Context, token string, request *telegramAdapterRequest) (map[string]interface{}, error) {
	if request.Attachment == nil || !telegramEndpointMatchesEvent(request.Endpoint, request.Event) {
		return nil, errors.New("Telegram attachment origin is invalid")
	}
	file := request.Attachment
	present := false
	for _, candidate := range request.Event.Attachments {
		present = present || candidate == *file
	}
	if !present || file.ID == "" {
		return nil, errors.New("Telegram attachment was not in the verified event")
	}
	result := map[string]interface{}{"id": file.ID, "status": "unavailable"}
	if file.SizeBytes > maximumTelegramFileBytes {
		result["status"] = "size_limit"
		return result, nil
	}
	response, status, err := a.telegramJSON(ctx, token, "getFile", map[string]interface{}{"file_id": file.ID})
	if err != nil || status == 429 || status >= 500 {
		return nil, errors.New("Telegram file lookup temporarily unavailable")
	}
	if !response.OK {
		return result, nil
	}
	var found struct {
		ID   string `json:"file_id"`
		Size int64  `json:"file_size"`
		Path string `json:"file_path"`
	}
	if json.Unmarshal(response.Result, &found) != nil || found.ID != file.ID {
		return nil, errors.New("Telegram returned a different file")
	}
	if found.Size > maximumTelegramFileBytes {
		result["status"] = "size_limit"
		return result, nil
	}
	link, valid := a.telegramFileURL(token, found.Path)
	if !valid {
		return result, nil
	}
	downloadRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return result, nil
	}
	client := *a.client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("Telegram file redirects are refused")
	}
	download, err := client.Do(downloadRequest)
	if err != nil {
		return nil, errors.New("Telegram file download temporarily unavailable")
	}
	defer download.Body.Close()
	if download.StatusCode == 429 || download.StatusCode >= 500 {
		return nil, errors.New("Telegram file download temporarily unavailable")
	}
	if download.StatusCode != http.StatusOK {
		return result, nil
	}
	if download.ContentLength > maximumTelegramFileBytes {
		result["status"] = "size_limit"
		return result, nil
	}
	data, err := io.ReadAll(io.LimitReader(download.Body, maximumTelegramFileBytes+1))
	if err != nil {
		return nil, errors.New("Telegram file download interrupted")
	}
	if len(data) > maximumTelegramFileBytes {
		result["status"] = "size_limit"
		return result, nil
	}
	if len(data) == 0 {
		return result, nil
	}
	result["status"] = "supplied"
	result["data"] = data
	result["name"] = file.Name
	result["mediaType"] = file.MediaType
	return result, nil
}
func (a *telegramConversationAdapter) telegramFileURL(token, filePath string) (string, bool) {
	if filePath == "" || strings.ContainsAny(filePath, "\\%?#:\x00\r\n") || strings.HasPrefix(filePath, "/") || path.Clean(filePath) != filePath {
		return "", false
	}
	for _, part := range strings.Split(filePath, "/") {
		if part == "." || part == ".." || part == "" {
			return "", false
		}
	}
	base, err := url.Parse(a.baseURL)
	if err != nil || base.User != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || !strings.HasSuffix(base.Path, "/bot") {
		return "", false
	}
	base.Path = strings.TrimSuffix(base.Path, "/bot") + "/file/bot" + token + "/" + filePath
	return base.String(), true
}

type telegramUploadReceipt struct {
	ArtifactID string `json:"artifactId"`
	Digest     string `json:"digest"`
	Name       string `json:"name"`
	MessageID  string `json:"messageId"`
}
type telegramDeliveryCheckpoint struct {
	SafeRetryAfterAttempt int                     `json:"safeRetryAfterAttempt,omitempty"`
	DeliveryID            string                  `json:"deliveryId"`
	ChatID                string                  `json:"chatId"`
	ThreadID              string                  `json:"threadId"`
	MessageDigest         string                  `json:"messageDigest"`
	TextReceipts          []string                `json:"textReceipts,omitempty"`
	Files                 []telegramUploadReceipt `json:"files,omitempty"`
}

func telegramCheckpoint(request *telegramAdapterRequest) (telegramDeliveryCheckpoint, error) {
	checkpoint := telegramDeliveryCheckpoint{DeliveryID: request.Delivery.ID, ChatID: request.Endpoint.Address, ThreadID: request.Delivery.ExternalThreadID, MessageDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(request.Message.Content)))}
	if raw, ok := request.Delivery.Progress["telegramDelivery"]; ok {
		encoded, _ := json.Marshal(raw)
		var saved telegramDeliveryCheckpoint
		if json.Unmarshal(encoded, &saved) != nil || saved.DeliveryID != checkpoint.DeliveryID || saved.ChatID != checkpoint.ChatID || saved.ThreadID != checkpoint.ThreadID || saved.MessageDigest != checkpoint.MessageDigest || saved.SafeRetryAfterAttempt < 0 || len(saved.Files) > len(request.Attachments) || len(saved.TextReceipts) > len(telegramTextChunks(request.Message.Content)) {
			return checkpoint, errors.New("Telegram delivery checkpoint drifted")
		}
		for _, id := range saved.TextReceipts {
			if n, err := strconv.ParseInt(id, 10, 64); err != nil || n < 1 {
				return checkpoint, errors.New("Telegram delivery checkpoint drifted")
			}
		}
		for i, receipt := range saved.Files {
			file := request.Attachments[i]
			if receipt.ArtifactID != file.ID || receipt.Digest != fmt.Sprintf("%x", sha256.Sum256(file.Data)) || receipt.Name != file.Name {
				return checkpoint, errors.New("Telegram delivery checkpoint drifted")
			}
			if n, err := strconv.ParseInt(receipt.MessageID, 10, 64); err != nil || n < 1 {
				return checkpoint, errors.New("Telegram delivery checkpoint drifted")
			}
		}
		checkpoint = saved
	}
	return checkpoint, nil
}
func telegramDigest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func telegramProgress(output map[string]interface{}, checkpoint telegramDeliveryCheckpoint) map[string]interface{} {
	output["progress"] = map[string]interface{}{"telegramDelivery": checkpoint}
	return output
}
func (a *telegramConversationAdapter) telegramUpload(ctx context.Context, token, method, field string, parameters map[string]interface{}, file telegramConversationFileContent) (TelegramResponse, int, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range parameters {
		var encoded string
		switch item := value.(type) {
		case string:
			encoded = item
		default:
			raw, err := json.Marshal(item)
			if err != nil {
				return TelegramResponse{}, 0, errors.New("Telegram upload fields are invalid")
			}
			encoded = string(raw)
		}
		if writer.WriteField(key, encoded) != nil {
			return TelegramResponse{}, 0, errors.New("Telegram upload fields are invalid")
		}
	}
	part, err := writer.CreateFormFile(field, file.Name)
	if err != nil {
		return TelegramResponse{}, 0, errors.New("Telegram upload filename is invalid")
	}
	if _, err = part.Write(file.Data); err != nil {
		return TelegramResponse{}, 0, errors.New("Telegram upload data is invalid")
	}
	if writer.Close() != nil {
		return TelegramResponse{}, 0, errors.New("Telegram upload is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+token+"/"+method, body)
	if err != nil {
		return TelegramResponse{}, 0, errors.New("Telegram upload request is invalid")
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return a.telegramRequest(request)
}
