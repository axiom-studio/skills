package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/resolver"
)

var slackFileID = regexp.MustCompile(`^F[A-Z0-9]+$`)

// The host shows these types to the model as images; GIF is not accepted.
var slackModelImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// errSkipDownload stops downloadFile after files.info when the file is not an
// image the model can see, so its bytes are never fetched.
var errSkipDownload = errors.New("Slack file download skipped")

// SlackReadFileExecutor handles slack-read-file: it lets the model see an image
// shared in one authorized channel.
type SlackReadFileExecutor struct{}

func (e *SlackReadFileExecutor) Type() string { return "slack-read-file" }

func (e *SlackReadFileExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := slackConnectionToken(config, resolver)
	channel := getString(config, "channel")
	fileID := strings.TrimSpace(getString(config, "fileId"))
	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if !slackFileID.MatchString(fileID) {
		return nil, fmt.Errorf("fileId must be a Slack file ID such as F0123ABCD")
	}
	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	baseURL := slackBaseURL
	if strings.TrimSpace(slackBaseURLOverride) != "" {
		baseURL = slackBaseURLOverride
	}
	adapter := newSlackAdapter("", baseURL, slackHTTPClient)
	skipStatus := ""
	allow := func(file slackFile) error {
		if !slackFileSharedIn(file, channelID) {
			return fmt.Errorf("file %s is not shared in channel %s", fileID, channelID)
		}
		mediaType := strings.ToLower(file.MediaType)
		if mediaType == "image/gif" {
			skipStatus = "unsupported_image"
		} else if !slackModelImageTypes[mediaType] {
			skipStatus = "not_an_image"
		}
		if skipStatus != "" {
			return errSkipDownload
		}
		return nil
	}
	file, data, status, err := adapter.downloadFile(ctx, token, fileID, allow)
	if errors.Is(err, errSkipDownload) {
		status, err = skipStatus, nil
	}
	if err != nil {
		return nil, err
	}
	if status == "missing_scope" {
		return nil, fmt.Errorf("the Slack app needs the files:read bot scope to read files; add it to the app's bot scopes and reinstall the app once")
	}

	output := map[string]interface{}{"success": true, "channel": channelID, "status": status}
	if described := normalizedSlackFiles([]slackFile{file}); len(described) == 1 {
		output["file"] = described[0]
	} else {
		output["file"] = conversationFile{ID: fileID, Name: fileID, MediaType: "application/octet-stream"}
	}
	if status == "supplied" {
		// A data URL keeps the image's own type. The host keeps it as a file
		// in the chat (tool-output/<call>/) and shows that file to the model,
		// the same way it reads every other chat image.
		output["image"] = "data:" + strings.ToLower(file.MediaType) + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return &executor.StepResult{Output: output}, nil
}

// Slack lists the conversations a file is shared in under shares, keyed by
// channel ID per visibility, and in the older channels/groups/ims arrays.
func slackFileSharedIn(file slackFile, channelID string) bool {
	for _, visibility := range file.Shares {
		if _, ok := visibility[channelID]; ok {
			return true
		}
	}
	for _, list := range [][]string{file.Channels, file.Groups, file.IMs} {
		for _, id := range list {
			if id == channelID {
				return true
			}
		}
	}
	return false
}

var SlackReadFileSchema = resolver.NewSchemaBuilder("slack-read-file").
	WithName("Read File").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Look at a PNG, JPEG or WebP image shared in an authorized Slack channel").
	AddSection("File").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123..., D123..., or #general"), resolver.WithHint("The channel the file was shared in")).
	AddTextField("fileId", "File ID", resolver.WithRequired(), resolver.WithPlaceholder("F0123ABCD"), resolver.WithHint("A file ID listed on a message by Read Messages")).
	EndSection().
	Build()
