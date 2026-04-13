package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

// Zoom API configuration
const (
	ZoomAPIBase      = "https://api.zoom.us/v2"
	ZoomTokenTimeout = 50 * time.Minute // Refresh tokens before expiry
)

// ZoomClient represents a Zoom API client with JWT authentication
type ZoomClient struct {
	AccountID    string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

// TokenCache caches JWT tokens to avoid regenerating on every request
type TokenCache struct {
	mu        sync.RWMutex
	tokens    map[string]*CachedToken
}

type CachedToken struct {
	Token     string
	ExpiresAt time.Time
}

var (
	tokenCache = &TokenCache{
		tokens: make(map[string]*CachedToken),
	}
)

// ============================================================================
// ZOOM API RESPONSE TYPES
// ============================================================================

// ZoomMeeting represents a Zoom meeting
type ZoomMeeting struct {
	ID               int64                  `json:"id"`
	UUID             string                 `json:"uuid"`
	HostID           string                 `json:"host_id"`
	HostEmail        string                 `json:"host_email"`
	Status           string                 `json:"status"`
	Topic            string                 `json:"topic"`
	Type             int                    `json:"type"`
	StartTime        string                 `json:"start_time"`
	Duration         int                    `json:"duration"`
	Timezone         string                 `json:"timezone"`
	CreatedAt        string                 `json:"created_at"`
	JoinURL          string                 `json:"join_url"`
	Pmi              string                 `json:"pmi"`
	Password         string                 `json:"password"`
	H323Password     string                 `json:"h323_password"`
	AlternativeHosts string                 `json:"alternative_hosts"`
	Settings         *ZoomMeetingSettings   `json:"settings"`
	OccurrenceID     int64                  `json:"occurrence_id,omitempty"`
	Occurrences      []ZoomOccurrence       `json:"occurrences,omitempty"`
	TrackingFields   []ZoomTrackingField    `json:"tracking_fields,omitempty"`
}

// ZoomMeetingSettings represents meeting settings
type ZoomMeetingSettings struct {
	HostVideo              bool                   `json:"host_video"`
	ParticipantVideo       bool                   `json:"participant_video"`
	CnMeeting              bool                   `json:"cn_meeting"`
	InMeeting              bool                   `json:"in_meeting"`
	JoinBeforeHost         bool                   `json:"join_before_host"`
	MuteUponEntry          bool                   `json:"mute_upon_entry"`
	Watermark              bool                   `json:"watermark"`
	UsePmi                 bool                   `json:"use_pmi"`
	ApprovalType           int                    `json:"approval_type"`
	Audio                  string                 `json:"audio"`
	AutoRecording          string                 `json:"auto_recording"`
	EnforceLogin           bool                   `json:"enforce_login"`
	EnforceLoginDomains    string                 `json:"enforce_login_domains"`
	AlternativeHosts       string                 `json:"alternative_hosts"`
	AlternativeHostsUpdate bool                   `json:"alternative_hosts_update"`
	RegistrationType       int                    `json:"registration_type"`
	CloseRegistration      bool                   `json:"close_registration"`
	ShowShareButton        bool                   `json:"show_share_button"`
	AllowMultipleDevices   bool                   `json:"allow_multiple_devices"`
	WaitingRoom            bool                   `json:"waiting_room"`
	ContactEmail           string                 `json:"contact_email"`
	ContactName            string                 `json:"contact_name"`
	PermittedParticipants  string                 `json:"permitted_participants"`
	GlobalDialInCountries  []string               `json:"global_dial_in_countries"`
	GlobalDialInNumbers    []ZoomDialInNumber     `json:"global_dial_in_numbers"`
	PrivateMeeting         bool                   `json:"private_meeting"`
	MeetingAuthentication  bool                   `json:"meeting_authentication"`
	AuthenticationOption   string                 `json:"authentication_option"`
	AuthenticationDomains  string                 `json:"authentication_domains"`
	AuthenticationName     string                 `json:"authentication_name"`
	ThirdPartyAudio        bool                   `json:"third_party_audio"`
	AudioConferencing      []ZoomAudioConferencing `json:"audio_conferencing"`
	ScheduleFor            string                 `json:"schedule_for"`
	HostSaveVideoOrder     bool                   `json:"host_save_video_order"`
}

// ZoomDialInNumber represents a dial-in number
type ZoomDialInNumber struct {
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	City        string `json:"city"`
	Number      string `json:"number"`
	Type        string `json:"type"`
}

// ZoomAudioConferencing represents audio conferencing info
type ZoomAudioConferencing struct {
	TollNumber      string `json:"toll_number"`
	TollFreeNumber  string `json:"toll_free_number"`
	CountryLabel    string `json:"country_label"`
	DisplayGlobal   bool   `json:"display_global"`
}

// ZoomOccurrence represents a recurring meeting occurrence
type ZoomOccurrence struct {
	OccurrenceID int64  `json:"occurrence_id"`
	Status       string `json:"status"`
	StartTime    string `json:"start_time"`
	Duration     int    `json:"duration"`
}

// ZoomTrackingField represents a tracking field
type ZoomTrackingField struct {
	Field string `json:"field"`
	Value string `json:"value"`
	Visible bool `json:"visible"`
}

// ZoomMeetingListResponse represents the response from list meetings
type ZoomMeetingListResponse struct {
	TotalCount   int            `json:"total_count"`
	Meetings     []ZoomMeeting  `json:"meetings"`
	NextPageToken string        `json:"next_page_token"`
	PageSize     int            `json:"page_size"`
}

// ZoomRecording represents a Zoom recording
type ZoomRecording struct {
	ID            string                  `json:"id"`
	MeetingID     string                  `json:"meeting_id"`
	MeetingUUID   string                  `json:"meeting_uuid"`
	HostID        string                  `json:"host_id"`
	Topic         string                  `json:"topic"`
	RecordingType string                  `json:"recording_type"`
	StartTime     string                  `json:"start_time"`
	Duration      int                     `json:"duration"`
	TotalSize     int64                   `json:"total_size"`
	ShareURL      string                  `json:"share_url"`
	FileCount     int                     `json:"file_count"`
	Files         []ZoomRecordingFile     `json:"recording_files"`
}

// ZoomRecordingFile represents a recording file
type ZoomRecordingFile struct {
	ID         string `json:"id"`
	MeetingID  string `json:"meeting_id"`
	RecordingStart string `json:"recording_start"`
	RecordingEnd   string `json:"recording_end"`
	FileType   string `json:"file_type"`
	FileSize   int64  `json:"file_size"`
	PlayURL    string `json:"play_url"`
	DownloadURL string `json:"download_url"`
	Status     string `json:"status"`
	RecordingType string `json:"recording_type"`
}

// ZoomRecordingListResponse represents the response from list recordings
type ZoomRecordingListResponse struct {
	TotalCount   int               `json:"total_count"`
	FromDate     string            `json:"from"`
	ToDate       string            `json:"to"`
	NextPageToken string           `json:"next_page_token"`
	PageSize     int               `json:"page_size"`
	Recordings   []ZoomRecording   `json:"meetings"`
}

// ZoomParticipant represents a meeting participant
type ZoomParticipant struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	JoinTime       string `json:"join_time"`
	LeaveTime      string `json:"leave_time"`
	Duration       int    `json:"duration"`
	Status         string `json:"status"`
	UserID         string `json:"user_id"`
}

// ZoomParticipantListResponse represents the response from list participants
type ZoomParticipantListResponse struct {
	TotalCount   int               `json:"total_count"`
	Participants []ZoomParticipant `json:"participants"`
	NextPageToken string           `json:"next_page_token"`
	PageSize     int               `json:"page_size"`
}

// ZoomUser represents a Zoom user
type ZoomUser struct {
	ID              string               `json:"id"`
	FirstName       string               `json:"first_name"`
	LastName        string               `json:"last_name"`
	Email           string               `json:"email"`
	Type            int                  `json:"type"`
	RoleName        string               `json:"role_name"`
	Pmi             string               `json:"pmi"`
	UsePmi          bool                 `json:"use_pmi"`
	Dept            string               `json:"dept"`
	Verified        int                  `json:"verified"`
	CreatedAt       string               `json:"created_at"`
	LastLoginTime   string               `json:"last_login_time"`
	LastClientVersion string             `json:"last_client_version"`
	PicURL          string               `json:"pic_url"`
	HostKey         string               `json:"host_key"`
	JoinedAt        string               `json:"joined_at"`
	Language        string               `json:"language"`
	PhoneCountry    string               `json:"phone_country"`
	PhoneNumber     string               `json:"phone_number"`
	Status          string               `json:"status"`
	Stats           *ZoomUserStats       `json:"stats"`
	Groups          []ZoomGroup          `json:"groups"`
	IMGroupIDs      []string             `json:"im_group_ids"`
	AccountID       string               `json:"account_id"`
	LoginTypes      []string             `json:"login_types"`
	Timezone        string               `json:"timezone"`
}

// ZoomUserStats represents user statistics
type ZoomUserStats struct {
	CurrentUsage *ZoomCurrentUsage `json:"current_usage"`
	LastMonthUsage *ZoomLastMonthUsage `json:"last_month_usage"`
}

// ZoomCurrentUsage represents current month usage
type ZoomCurrentUsage struct {
	CurrentMonth int `json:"current_month"`
	TotalMeetingMinutes int `json:"total_meeting_minutes"`
	TotalParticipants int `json:"total_participants"`
}

// ZoomLastMonthUsage represents last month usage
type ZoomLastMonthUsage struct {
	CurrentMonth int `json:"current_month"`
	TotalMeetingMinutes int `json:"total_meeting_minutes"`
	TotalParticipants int `json:"total_participants"`
}

// ZoomGroup represents a user group
type ZoomGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ZoomUserListResponse represents the response from list users
type ZoomUserListResponse struct {
	TotalCount   int          `json:"total_count"`
	PageCount    int          `json:"page_count"`
	PageSize     int          `json:"page_size"`
	PageNumber   int          `json:"page_number"`
	NextPageToken string      `json:"next_page_token"`
	Users        []ZoomUser   `json:"users"`
}

// ZoomWebinar represents a Zoom webinar
type ZoomWebinar struct {
	UUID             string                `json:"uuid"`
	ID               int64                 `json:"id"`
	HostID           string                `json:"host_id"`
	HostEmail        string                `json:"host_email"`
	Topic            string                `json:"topic"`
	Type             int                   `json:"type"`
	Status           string                `json:"status"`
	StartTime        string                `json:"start_time"`
	Duration         int                   `json:"duration"`
	Timezone         string                `json:"timezone"`
	CreatedAt        string                `json:"created_at"`
	JoinURL          string                `json:"join_url"`
	Panelists        []ZoomPanelist        `json:"panelists"`
	Settings         *ZoomWebinarSettings  `json:"settings"`
	TrackingFields   []ZoomTrackingField   `json:"tracking_fields,omitempty"`
}

// ZoomPanelist represents a webinar panelist
type ZoomPanelist struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	JoinURL     string `json:"join_url"`
}

// ZoomWebinarSettings represents webinar settings
type ZoomWebinarSettings struct {
	HostVideo              bool     `json:"host_video"`
	PanelistsVideo         bool     `json:"panelists_video"`
	Audio                  string   `json:"audio"`
	EnforceLogin           bool     `json:"enforce_login"`
	EnforceLoginDomains    string   `json:"enforce_login_domains"`
	AlternativeHosts       string   `json:"alternative_hosts"`
	CloseRegistration      bool     `json:"close_registration"`
	ShowShareButton        bool     `json:"show_share_button"`
	AllowMultipleDevices   bool     `json:"allow_multiple_devices"`
	OnDemand               bool     `json:"ondemand"`
	PracticeSession        bool     `json:"practice_session"`
	OpenRegistration       bool     `json:"open_registration"`
	ApprovalType           int      `json:"approval_type"`
	RegistrationType       int      `json:"registration_type"`
	QuestionAndAnswer      bool     `json:"question_and_answer"`
	QAChat                 bool     `json:"qa_chat"`
	QAAnonymousQuestion    bool     `json:"qa_anonymous_question"`
	Chat                   string   `json:"chat"`
	Poll                   bool     `json:"poll"`
	EnableLiveStreaming    bool     `json:"enable_live_streaming"`
	LiveStreamInfo         *ZoomLiveStreamInfo `json:"live_stream_info"`
	ViewTheVideo           bool     `json:"view_the_video"`
	AttendeeAndPanelistLink string  `json:"attendee_and_panelist_link"`
	LanguageInterpretation bool    `json:"language_interpretation"`
}

// ZoomLiveStreamInfo represents live stream configuration
type ZoomLiveStreamInfo struct {
	StreamURL     string `json:"stream_url"`
	StreamKey     string `json:"stream_key"`
	LiveStreamStatus string `json:"live_stream_status"`
}

// ZoomWebinarListResponse represents the response from list webinars
type ZoomWebinarListResponse struct {
	TotalCount   int            `json:"total_count"`
	Webinars     []ZoomWebinar  `json:"webinars"`
	NextPageToken string        `json:"next_page_token"`
	PageSize     int            `json:"page_size"`
}

// ZoomError represents a Zoom API error response
type ZoomError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ============================================================================
// JWT AUTHENTICATION
// ============================================================================

// generateJWT creates a JWT token for Zoom API authentication
func generateJWT(accountID, clientID, clientSecret string) (string, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s", accountID, clientID, clientSecret)

	// Check cache first
	tokenCache.mu.RLock()
	cached, ok := tokenCache.tokens[cacheKey]
	tokenCache.mu.RUnlock()

	if ok && time.Now().Before(cached.ExpiresAt) {
		return cached.Token, nil
	}

	// Generate new JWT
	tokenCache.mu.Lock()
	defer tokenCache.mu.Unlock()

	// Double check after acquiring write lock
	if cached, ok := tokenCache.tokens[cacheKey]; ok && time.Now().Before(cached.ExpiresAt) {
		return cached.Token, nil
	}

	// Create header
	header := map[string]interface{}{
		"alg": "HS256",
		"typ": "JWT",
	}

	// Create claims
	now := time.Now().UTC()
	expiry := now.Add(ZoomTokenTimeout)

	claims := map[string]interface{}{
		"iss": clientID,
		"exp": expiry.Unix(),
	}

	// For JWT auth, we need to include account ID in the signature
	// Zoom uses HMAC-SHA256 with the client secret

	// Encode header
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %w", err)
	}
	headerEncoded := base64URLEncode(headerJSON)

	// Encode claims
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal claims: %w", err)
	}
	claimsEncoded := base64URLEncode(claimsJSON)

	// Create signature input
	signatureInput := headerEncoded + "." + claimsEncoded

	// Create signature using HMAC-SHA256
	signature, err := signHMACSHA256([]byte(signatureInput), []byte(clientSecret))
	if err != nil {
		return "", fmt.Errorf("failed to create signature: %w", err)
	}

	token := signatureInput + "." + signature

	// Cache the token
	tokenCache.tokens[cacheKey] = &CachedToken{
		Token:     token,
		ExpiresAt: expiry.Add(-time.Minute), // Refresh 1 minute early
	}

	return token, nil
}

// base64URLEncode encodes bytes to base64 URL encoding without padding
func base64URLEncode(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}

// signHMACSHA256 creates an HMAC-SHA256 signature
func signHMACSHA256(data, key []byte) (string, error) {
	h := sha256.New224() // Zoom uses SHA256-224 for JWT
	h.Write(data)
	sum := h.Sum(key)

	h2 := sha256.New()
	h2.Write(sum)
	signature := h2.Sum(nil)

	return base64URLEncode(signature), nil
}

// ============================================================================
// ZOOM CLIENT HELPERS
// ============================================================================

// getZoomClient returns or creates a Zoom client (cached)
func getZoomClient(accountID, clientID, clientSecret string) *ZoomClient {
	return &ZoomClient{
		AccountID:    accountID,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

// doRequest performs an HTTP request to the Zoom API
func (c *ZoomClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	// Generate JWT token
	token, err := generateJWT(c.AccountID, c.ClientID, c.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to generate JWT: %w", err)
	}

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	reqURL := ZoomAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// decodeResponse decodes a Zoom API response
func decodeResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var zoomErr ZoomError
		if err := json.Unmarshal(body, &zoomErr); err == nil && zoomErr.Message != "" {
			return fmt.Errorf("Zoom API error (%d): %s (code: %s)", resp.StatusCode, zoomErr.Message, zoomErr.Code)
		}
		return fmt.Errorf("Zoom API error (%d): %s", resp.StatusCode, string(body))
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

// Helper to get string from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Helper to get int from config
func getInt(config map[string]interface{}, key string, def int) int {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// Helper to get bool from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// Helper to get string slice from config
func getStringSlice(config map[string]interface{}, key string) []string {
	if v, ok := config[key]; ok {
		switch arr := v.(type) {
		case []interface{}:
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		case []string:
			return arr
		case string:
			return strings.Split(arr, ",")
		}
	}
	return nil
}

// Helper to get map from config
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// parseZoomConfig extracts Zoom configuration from config map
func parseZoomConfig(config map[string]interface{}) (accountID, clientID, clientSecret string) {
	return getString(config, "accountId"), getString(config, "clientId"), getString(config, "clientSecret")
}

// ============================================================================
// SCHEMAS
// ============================================================================

// ZoomMeetingCreateSchema is the UI schema for zoom-meeting-create
var ZoomMeetingCreateSchema = resolver.NewSchemaBuilder("zoom-meeting-create").
	WithName("Create Zoom Meeting").
	WithCategory("action").
	WithIcon("video").
	WithDescription("Create a new Zoom meeting").
	AddSection("Zoom Connection").
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Zoom account ID"),
			resolver.WithHint("Zoom account ID for JWT authentication"),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithHint("Zoom OAuth client ID"),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
			resolver.WithHint("Zoom OAuth client secret"),
		).
		EndSection().
	AddSection("Meeting Details").
		AddTextField("topic", "Topic",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Team Meeting"),
			resolver.WithHint("Meeting title"),
		).
		AddTextareaField("agenda", "Agenda",
			resolver.WithRows(3),
			resolver.WithPlaceholder("Meeting agenda and details"),
		).
		AddSelectField("type", "Meeting Type",
			[]resolver.SelectOption{
				{Label: "Instant Meeting", Value: "1"},
				{Label: "Scheduled Meeting", Value: "2"},
				{Label: "Recurring Meeting (No Fixed Time)", Value: "3"},
				{Label: "Recurring Meeting (Fixed Time)", Value: "8"},
			},
			resolver.WithDefault("2"),
			resolver.WithHint("Type of meeting to create"),
		).
		EndSection().
	AddSection("Schedule").
		AddTextField("startTime", "Start Time",
			resolver.WithPlaceholder("YYYY-MM-DDTHH:mm:ss"),
			resolver.WithHint("Meeting start time in ISO 8601 format (for scheduled meetings)"),
		).
		AddTextField("timezone", "Timezone",
			resolver.WithDefault("America/New_York"),
			resolver.WithPlaceholder("America/New_York"),
			resolver.WithHint("Timezone for the meeting"),
		).
		AddNumberField("duration", "Duration (minutes)",
			resolver.WithDefault(30),
			resolver.WithHint("Meeting duration in minutes"),
		).
		EndSection().
	AddSection("Settings").
		AddToggleField("hostVideo", "Host Video On",
			resolver.WithDefault(true),
		).
		AddToggleField("participantVideo", "Participant Video On",
			resolver.WithDefault(true),
		).
		AddSelectField("audio", "Audio Options",
			[]resolver.SelectOption{
				{Label: "Both", Value: "both"},
				{Label: "VoIP Only", Value: "voip"},
				{Label: "Telephony Only", Value: "telephony"},
				{Label: "Third Party", Value: "thirdParty"},
			},
			resolver.WithDefault("both"),
		).
		AddToggleField("waitingRoom", "Enable Waiting Room",
			resolver.WithDefault(false),
		).
		AddToggleField("muteUponEntry", "Mute Participants Upon Entry",
			resolver.WithDefault(false),
		).
		AddToggleField("joinBeforeHost", "Allow Join Before Host",
			resolver.WithDefault(false),
		).
		AddTextField("alternativeHosts", "Alternative Hosts",
			resolver.WithPlaceholder("user1@example.com,user2@example.com"),
			resolver.WithHint("Comma-separated emails of alternative hosts"),
		).
		AddSelectField("autoRecording", "Auto Recording",
			[]resolver.SelectOption{
				{Label: "None", Value: "none"},
				{Label: "Local", Value: "local"},
				{Label: "Cloud", Value: "cloud"},
			},
			resolver.WithDefault("none"),
		).
		EndSection().
	Build()

// ZoomMeetingListSchema is the UI schema for zoom-meeting-list
var ZoomMeetingListSchema = resolver.NewSchemaBuilder("zoom-meeting-list").
	WithName("List Zoom Meetings").
	WithCategory("action").
	WithIcon("list").
	WithDescription("List Zoom meetings for a user").
	AddSection("Zoom Connection").
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("userId", "User ID",
			resolver.WithDefault("me"),
			resolver.WithPlaceholder("me or user ID"),
			resolver.WithHint("User ID or 'me' for current user"),
		).
		AddSelectField("type", "Meeting Type",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Scheduled", Value: "scheduled"},
				{Label: "Live", Value: "live"},
				{Label: "Upcoming", Value: "upcoming"},
				{Label: "Past", Value: "past"},
				{Label: "Past One", Value: "past_one"},
			},
			resolver.WithDefault(""),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("pageSize", "Page Size",
			resolver.WithDefault(30),
			resolver.WithMinMax(1, 300),
		).
		AddTextField("nextPageToken", "Next Page Token",
			resolver.WithHint("Token for pagination"),
		).
		EndSection().
	Build()

// ZoomMeetingGetSchema is the UI schema for zoom-meeting-get
var ZoomMeetingGetSchema = resolver.NewSchemaBuilder("zoom-meeting-get").
	WithName("Get Zoom Meeting").
	WithCategory("action").
	WithIcon("info").
	WithDescription("Get details of a specific Zoom meeting").
	AddSection("Zoom Connection").
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Meeting").
		AddTextField("meetingId", "Meeting ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
			resolver.WithHint("Meeting ID or UUID"),
		).
		AddToggleField("showOccurrenceId", "Show Occurrence ID",
			resolver.WithDefault(false),
			resolver.WithHint("Include occurrence ID for recurring meetings"),
		).
		EndSection().
	Build()

// ZoomMeetingDeleteSchema is the UI schema for zoom-meeting-delete
var ZoomMeetingDeleteSchema = resolver.NewSchemaBuilder("zoom-meeting-delete").
	WithName("Delete Zoom Meeting").
	WithCategory("action").
	WithIcon("trash").
	WithDescription("Delete a Zoom meeting").
	AddSection("Zoom Connection").
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Meeting").
		AddTextField("meetingId", "Meeting ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
			resolver.WithHint("Meeting ID to delete"),
		).
		AddToggleField("scheduleForReminder", "Schedule for Reminder",
			resolver.WithDefault(true),
			resolver.WithHint("Send cancellation email to registrants"),
		).
		EndSection().
	Build()

// ZoomRecordingListSchema is the UI schema for zoom-recording-list
var ZoomRecordingListSchema = resolver.NewSchemaBuilder("zoom-recording-list").
	WithName("List Zoom Recordings").
	WithCategory("action").
	WithIcon("film").
	WithDescription("List Zoom cloud recordings").
	AddSection("Zoom Connection").
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("userId", "User ID",
			resolver.WithDefault("me"),
			resolver.WithPlaceholder("me or user ID"),
		).
		AddTextField("fromDate", "From Date",
			resolver.WithPlaceholder("YYYY-MM-DD"),
			resolver.WithHint("Start date (defaults to 1 month ago)"),
		).
		AddTextField("toDate", "To Date",
			resolver.WithPlaceholder("YYYY-MM-DD"),
			resolver.WithHint("End date (defaults to today)"),
		).
		AddTextField("meetingId", "Meeting ID",
			resolver.WithPlaceholder("Filter by specific meeting ID"),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("pageSize", "Page Size",
			resolver.WithDefault(30),
			resolver.WithMinMax(1, 300),
		).
		AddTextField("nextPageToken", "Next Page Token",
			resolver.WithHint("Token for pagination"),
		).
		EndSection().
	Build()

// ZoomParticipantListSchema is the UI schema for zoom-participant-list
var ZoomParticipantListSchema = resolver.NewSchemaBuilder("zoom-participant-list").
	WithName("List Meeting Participants").
	WithCategory("action").
	WithIcon("users").
	WithDescription("List participants in a Zoom meeting").
	AddSection("Zoom Connection").
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Meeting").
		AddTextField("meetingId", "Meeting ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
			resolver.WithHint("Meeting ID or UUID"),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("pageSize", "Page Size",
			resolver.WithDefault(30),
			resolver.WithMinMax(1, 300),
		).
		AddTextField("nextPageToken", "Next Page Token",
			resolver.WithHint("Token for pagination"),
		).
		EndSection().
	Build()

// ZoomUserListSchema is the UI schema for zoom-user-list
var ZoomUserListSchema = resolver.NewSchemaBuilder("zoom-user-list").
	WithName("List Zoom Users").
	WithCategory("action").
	WithIcon("users").
	WithDescription("List users in your Zoom account").
	AddSection("Zoom Connection").
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddSelectField("status", "Status",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Active", Value: "active"},
				{Label: "Inactive", Value: "inactive"},
				{Label: "Pending", Value: "pending"},
			},
			resolver.WithDefault(""),
		).
		AddSelectField("role", "Role",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Admin", Value: "admin"},
				{Label: "Member", Value: "member"},
			},
			resolver.WithDefault(""),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("pageSize", "Page Size",
			resolver.WithDefault(30),
			resolver.WithMinMax(1, 300),
		).
		AddNumberField("pageNumber", "Page Number",
			resolver.WithDefault(1),
			resolver.WithHint("Page number (1-indexed)"),
		).
		EndSection().
	Build()

// ZoomWebinarCreateSchema is the UI schema for zoom-webinar-create
var ZoomWebinarCreateSchema = resolver.NewSchemaBuilder("zoom-webinar-create").
	WithName("Create Zoom Webinar").
	WithCategory("action").
	WithIcon("video").
	WithDescription("Create a new Zoom webinar").
	AddSection("Zoom Connection").
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Webinar Details").
		AddTextField("topic", "Topic",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Company Webinar"),
			resolver.WithHint("Webinar title"),
		).
		AddTextareaField("agenda", "Agenda",
			resolver.WithRows(3),
			resolver.WithPlaceholder("Webinar description and agenda"),
		).
		AddSelectField("type", "Webinar Type",
			[]resolver.SelectOption{
				{Label: "Webinar", Value: "5"},
				{Label: "Webinar with Practice Session", Value: "6"},
			},
			resolver.WithDefault("5"),
		).
		EndSection().
	AddSection("Schedule").
		AddTextField("startTime", "Start Time",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YYYY-MM-DDTHH:mm:ss"),
			resolver.WithHint("Webinar start time in ISO 8601 format"),
		).
		AddTextField("timezone", "Timezone",
			resolver.WithDefault("America/New_York"),
			resolver.WithPlaceholder("America/New_York"),
		).
		AddNumberField("duration", "Duration (minutes)",
			resolver.WithDefault(60),
			resolver.WithHint("Webinar duration in minutes"),
		).
		EndSection().
	AddSection("Settings").
		AddToggleField("hostVideo", "Host Video On",
			resolver.WithDefault(true),
		).
		AddToggleField("panelistsVideo", "Panelists Video On",
			resolver.WithDefault(true),
		).
		AddSelectField("audio", "Audio Options",
			[]resolver.SelectOption{
				{Label: "Both", Value: "both"},
				{Label: "VoIP Only", Value: "voip"},
				{Label: "Telephony Only", Value: "telephony"},
			},
			resolver.WithDefault("both"),
		).
		AddToggleField("practiceSession", "Enable Practice Session",
			resolver.WithDefault(false),
			resolver.WithHint("Allow panelists to join before attendees"),
		).
		AddToggleField("onDemand", "Enable On-Demand",
			resolver.WithDefault(false),
			resolver.WithHint("Allow viewing recording after webinar ends"),
		).
		AddToggleField("questionAndAnswer", "Enable Q&A",
			resolver.WithDefault(true),
		).
		AddSelectField("chat", "Chat Settings",
			[]resolver.SelectOption{
				{Label: "Disabled", Value: "none"},
				{Label: "Host Only", Value: "host"},
				{Label: "Everyone", Value: "all"},
			},
			resolver.WithDefault("all"),
		).
		EndSection().
	Build()

// ============================================================================
// ZOOM-MEETING-CREATE EXECUTOR
// ============================================================================

// ZoomMeetingCreateConfig defines the configuration for zoom-meeting-create
type ZoomMeetingCreateConfig struct {
	AccountID        string `json:"accountId" description:"Zoom account ID"`
	ClientID         string `json:"clientId" description:"Zoom OAuth client ID"`
	ClientSecret     string `json:"clientSecret" description:"Zoom OAuth client secret"`
	Topic            string `json:"topic" description:"Meeting topic"`
	Agenda           string `json:"agenda" description:"Meeting agenda"`
	Type             int    `json:"type" default:"2" description:"Meeting type (1=instant, 2=scheduled, 3=recurring no time, 8=recurring fixed)"`
	StartTime        string `json:"startTime" description:"Meeting start time (ISO 8601)"`
	Timezone         string `json:"timezone" default:"America/New_York" description:"Meeting timezone"`
	Duration         int    `json:"duration" default:"30" description:"Meeting duration in minutes"`
	HostVideo        bool   `json:"hostVideo" default:"true" description:"Start with host video on"`
	ParticipantVideo bool   `json:"participantVideo" default:"true" description:"Start with participant video on"`
	Audio            string `json:"audio" default:"both" description:"Audio options (both/voip/telephony/thirdParty)"`
	WaitingRoom      bool   `json:"waitingRoom" description:"Enable waiting room"`
	MuteUponEntry    bool   `json:"muteUponEntry" description:"Mute participants upon entry"`
	JoinBeforeHost   bool   `json:"joinBeforeHost" description:"Allow participants to join before host"`
	AlternativeHosts string `json:"alternativeHosts" description:"Comma-separated emails of alternative hosts"`
	AutoRecording    string `json:"autoRecording" default:"none" description:"Auto recording setting (none/local/cloud)"`
	Password         string `json:"password" description:"Meeting password"`
	ScheduleFor      string `json:"scheduleFor" description:"Email to schedule meeting for another user"`
}

type ZoomMeetingCreateExecutor struct{}

func (e *ZoomMeetingCreateExecutor) Type() string { return "zoom-meeting-create" }

func (e *ZoomMeetingCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	accountID := templateResolver.ResolveString(getString(config, "accountId"))
	clientID := templateResolver.ResolveString(getString(config, "clientId"))
	clientSecret := templateResolver.ResolveString(getString(config, "clientSecret"))

	if accountID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("accountId, clientId, and clientSecret are required")
	}

	topic := templateResolver.ResolveString(getString(config, "topic"))
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	client := getZoomClient(accountID, clientID, clientSecret)

	// Build meeting request
	meetingData := map[string]interface{}{
		"topic":      topic,
		"type":       getInt(config, "type", 2),
		"duration":   getInt(config, "duration", 30),
		"timezone":   getString(config, "timezone"),
		"agenda":     getString(config, "agenda"),
		"start_time": getString(config, "startTime"),
		"password":   getString(config, "password"),
		"schedule_for": getString(config, "scheduleFor"),
		"settings": map[string]interface{}{
			"host_video":        getBool(config, "hostVideo", true),
			"participant_video": getBool(config, "participantVideo", true),
			"audio":             getString(config, "audio"),
			"waiting_room":      getBool(config, "waitingRoom", false),
			"mute_upon_entry":   getBool(config, "muteUponEntry", false),
			"join_before_host":  getBool(config, "joinBeforeHost", false),
			"auto_recording":    getString(config, "autoRecording"),
		},
	}

	// Add alternative hosts if provided
	if altHosts := getString(config, "alternativeHosts"); altHosts != "" {
		meetingData["alternative_hosts"] = altHosts
		meetingData["settings"].(map[string]interface{})["alternative_hosts"] = altHosts
	}

	resp, err := client.doRequest(ctx, "POST", "/users/me/meetings", meetingData)
	if err != nil {
		return nil, err
	}

	var meeting ZoomMeeting
	if err := decodeResponse(resp, &meeting); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"id":        meeting.ID,
		"uuid":      meeting.UUID,
		"topic":     meeting.Topic,
		"join_url":  meeting.JoinURL,
		"start_time": meeting.StartTime,
		"duration":  meeting.Duration,
		"password":  meeting.Password,
		"settings":  meeting.Settings,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// ZOOM-MEETING-LIST EXECUTOR
// ============================================================================

// ZoomMeetingListConfig defines the configuration for zoom-meeting-list
type ZoomMeetingListConfig struct {
	AccountID     string `json:"accountId" description:"Zoom account ID"`
	ClientID      string `json:"clientId" description:"Zoom OAuth client ID"`
	ClientSecret  string `json:"clientSecret" description:"Zoom OAuth client secret"`
	UserID        string `json:"userId" default:"me" description:"User ID or 'me'"`
	Type          string `json:"type" description:"Meeting type filter (scheduled/live/upcoming/past/past_one)"`
	PageSize      int    `json:"pageSize" default:"30" description:"Results per page"`
	NextPageToken string `json:"nextPageToken" description:"Pagination token"`
}

type ZoomMeetingListExecutor struct{}

func (e *ZoomMeetingListExecutor) Type() string { return "zoom-meeting-list" }

func (e *ZoomMeetingListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	accountID := templateResolver.ResolveString(getString(config, "accountId"))
	clientID := templateResolver.ResolveString(getString(config, "clientId"))
	clientSecret := templateResolver.ResolveString(getString(config, "clientSecret"))

	if accountID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("accountId, clientId, and clientSecret are required")
	}

	userID := getString(config, "userId")
	if userID == "" {
		userID = "me"
	}
	userID = templateResolver.ResolveString(userID)

	client := getZoomClient(accountID, clientID, clientSecret)

	// Build query parameters
	params := url.Values{}
	params.Set("page_size", strconv.Itoa(getInt(config, "pageSize", 30)))

	if meetingType := getString(config, "type"); meetingType != "" {
		params.Set("type", templateResolver.ResolveString(meetingType))
	}

	if nextPageToken := getString(config, "nextPageToken"); nextPageToken != "" {
		params.Set("next_page_token", templateResolver.ResolveString(nextPageToken))
	}

	path := fmt.Sprintf("/users/%s/meetings?%s", userID, params.Encode())

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp ZoomMeetingListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Convert meetings to output format
	var meetings []map[string]interface{}
	for _, m := range listResp.Meetings {
		meetings = append(meetings, map[string]interface{}{
			"id":         m.ID,
			"uuid":       m.UUID,
			"topic":      m.Topic,
			"type":       m.Type,
			"status":     m.Status,
			"start_time": m.StartTime,
			"duration":   m.Duration,
			"join_url":   m.JoinURL,
			"host_email": m.HostEmail,
			"timezone":   m.Timezone,
		})
	}

	output := map[string]interface{}{
		"meetings":      meetings,
		"total_count":   listResp.TotalCount,
		"page_size":     listResp.PageSize,
		"next_page_token": listResp.NextPageToken,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// ZOOM-MEETING-GET EXECUTOR
// ============================================================================

// ZoomMeetingGetConfig defines the configuration for zoom-meeting-get
type ZoomMeetingGetConfig struct {
	AccountID         string `json:"accountId" description:"Zoom account ID"`
	ClientID          string `json:"clientId" description:"Zoom OAuth client ID"`
	ClientSecret      string `json:"clientSecret" description:"Zoom OAuth client secret"`
	MeetingID         string `json:"meetingId" description:"Meeting ID or UUID"`
	ShowOccurrenceID  bool   `json:"showOccurrenceId" description:"Include occurrence ID"`
}

type ZoomMeetingGetExecutor struct{}

func (e *ZoomMeetingGetExecutor) Type() string { return "zoom-meeting-get" }

func (e *ZoomMeetingGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	accountID := templateResolver.ResolveString(getString(config, "accountId"))
	clientID := templateResolver.ResolveString(getString(config, "clientId"))
	clientSecret := templateResolver.ResolveString(getString(config, "clientSecret"))

	if accountID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("accountId, clientId, and clientSecret are required")
	}

	meetingID := templateResolver.ResolveString(getString(config, "meetingId"))
	if meetingID == "" {
		return nil, fmt.Errorf("meetingId is required")
	}

	client := getZoomClient(accountID, clientID, clientSecret)

	path := fmt.Sprintf("/meetings/%s", meetingID)
	if getBool(config, "showOccurrenceId", false) {
		path += "?show_occurrence_id=true"
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var meeting ZoomMeeting
	if err := decodeResponse(resp, &meeting); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"id":              meeting.ID,
		"uuid":            meeting.UUID,
		"topic":           meeting.Topic,
		"type":            meeting.Type,
		"status":          meeting.Status,
		"start_time":      meeting.StartTime,
		"duration":        meeting.Duration,
		"timezone":        meeting.Timezone,
		"join_url":        meeting.JoinURL,
		"password":        meeting.Password,
		"host_email":      meeting.HostEmail,
		"host_id":         meeting.HostID,
		"created_at":      meeting.CreatedAt,
		"settings":        meeting.Settings,
		"alternative_hosts": meeting.AlternativeHosts,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// ZOOM-MEETING-DELETE EXECUTOR
// ============================================================================

// ZoomMeetingDeleteConfig defines the configuration for zoom-meeting-delete
type ZoomMeetingDeleteConfig struct {
	AccountID           string `json:"accountId" description:"Zoom account ID"`
	ClientID            string `json:"clientId" description:"Zoom OAuth client ID"`
	ClientSecret        string `json:"clientSecret" description:"Zoom OAuth client secret"`
	MeetingID           string `json:"meetingId" description:"Meeting ID to delete"`
	ScheduleForReminder bool   `json:"scheduleForReminder" default:"true" description:"Send cancellation email"`
}

type ZoomMeetingDeleteExecutor struct{}

func (e *ZoomMeetingDeleteExecutor) Type() string { return "zoom-meeting-delete" }

func (e *ZoomMeetingDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	accountID := templateResolver.ResolveString(getString(config, "accountId"))
	clientID := templateResolver.ResolveString(getString(config, "clientId"))
	clientSecret := templateResolver.ResolveString(getString(config, "clientSecret"))

	if accountID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("accountId, clientId, and clientSecret are required")
	}

	meetingID := templateResolver.ResolveString(getString(config, "meetingId"))
	if meetingID == "" {
		return nil, fmt.Errorf("meetingId is required")
	}

	client := getZoomClient(accountID, clientID, clientSecret)

	path := fmt.Sprintf("/meetings/%s", meetingID)
	if getBool(config, "scheduleForReminder", true) {
		path += "?schedule_for_reminder=true"
	} else {
		path += "?schedule_for_reminder=false"
	}

	resp, err := client.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return nil, err
	}

	if err := decodeResponse(resp, nil); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"success":  true,
		"message":  fmt.Sprintf("Meeting %s deleted successfully", meetingID),
		"meeting_id": meetingID,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// ZOOM-RECORDING-LIST EXECUTOR
// ============================================================================

// ZoomRecordingListConfig defines the configuration for zoom-recording-list
type ZoomRecordingListConfig struct {
	AccountID     string `json:"accountId" description:"Zoom account ID"`
	ClientID      string `json:"clientId" description:"Zoom OAuth client ID"`
	ClientSecret  string `json:"clientSecret" description:"Zoom OAuth client secret"`
	UserID        string `json:"userId" default:"me" description:"User ID or 'me'"`
	FromDate      string `json:"fromDate" description:"Start date (YYYY-MM-DD)"`
	ToDate        string `json:"toDate" description:"End date (YYYY-MM-DD)"`
	MeetingID     string `json:"meetingId" description:"Filter by meeting ID"`
	PageSize      int    `json:"pageSize" default:"30" description:"Results per page"`
	NextPageToken string `json:"nextPageToken" description:"Pagination token"`
}

type ZoomRecordingListExecutor struct{}

func (e *ZoomRecordingListExecutor) Type() string { return "zoom-recording-list" }

func (e *ZoomRecordingListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	accountID := templateResolver.ResolveString(getString(config, "accountId"))
	clientID := templateResolver.ResolveString(getString(config, "clientId"))
	clientSecret := templateResolver.ResolveString(getString(config, "clientSecret"))

	if accountID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("accountId, clientId, and clientSecret are required")
	}

	userID := getString(config, "userId")
	if userID == "" {
		userID = "me"
	}
	userID = templateResolver.ResolveString(userID)

	client := getZoomClient(accountID, clientID, clientSecret)

	// Build query parameters
	params := url.Values{}
	params.Set("page_size", strconv.Itoa(getInt(config, "pageSize", 30)))

	// Set date range (defaults to last 30 days if not specified)
	fromDate := getString(config, "fromDate")
	toDate := getString(config, "toDate")

	if fromDate == "" {
		fromDate = time.Now().AddDate(0, -1, 0).Format("2006-01-02")
	} else {
		fromDate = templateResolver.ResolveString(fromDate)
	}

	if toDate == "" {
		toDate = time.Now().Format("2006-01-02")
	} else {
		toDate = templateResolver.ResolveString(toDate)
	}

	params.Set("from", fromDate)
	params.Set("to", toDate)

	if meetingID := getString(config, "meetingId"); meetingID != "" {
		params.Set("meeting_id", templateResolver.ResolveString(meetingID))
	}

	if nextPageToken := getString(config, "nextPageToken"); nextPageToken != "" {
		params.Set("next_page_token", templateResolver.ResolveString(nextPageToken))
	}

	path := fmt.Sprintf("/users/%s/recordings?%s", userID, params.Encode())

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp ZoomRecordingListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Convert recordings to output format
	var recordings []map[string]interface{}
	for _, r := range listResp.Recordings {
		var files []map[string]interface{}
		for _, f := range r.Files {
			files = append(files, map[string]interface{}{
				"id":             f.ID,
				"file_type":      f.FileType,
				"file_size":      f.FileSize,
				"play_url":       f.PlayURL,
				"download_url":   f.DownloadURL,
				"recording_type": f.RecordingType,
				"status":         f.Status,
			})
		}

		recordings = append(recordings, map[string]interface{}{
			"id":             r.ID,
			"meeting_id":     r.MeetingID,
			"meeting_uuid":   r.MeetingUUID,
			"topic":          r.Topic,
			"recording_type": r.RecordingType,
			"start_time":     r.StartTime,
			"duration":       r.Duration,
			"total_size":     r.TotalSize,
			"share_url":      r.ShareURL,
			"file_count":     r.FileCount,
			"files":          files,
		})
	}

	output := map[string]interface{}{
		"recordings":      recordings,
		"total_count":     listResp.TotalCount,
		"from_date":       listResp.FromDate,
		"to_date":         listResp.ToDate,
		"page_size":       listResp.PageSize,
		"next_page_token": listResp.NextPageToken,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// ZOOM-PARTICIPANT-LIST EXECUTOR
// ============================================================================

// ZoomParticipantListConfig defines the configuration for zoom-participant-list
type ZoomParticipantListConfig struct {
	AccountID     string `json:"accountId" description:"Zoom account ID"`
	ClientID      string `json:"clientId" description:"Zoom OAuth client ID"`
	ClientSecret  string `json:"clientSecret" description:"Zoom OAuth client secret"`
	MeetingID     string `json:"meetingId" description:"Meeting ID or UUID"`
	PageSize      int    `json:"pageSize" default:"30" description:"Results per page"`
	NextPageToken string `json:"nextPageToken" description:"Pagination token"`
}

type ZoomParticipantListExecutor struct{}

func (e *ZoomParticipantListExecutor) Type() string { return "zoom-participant-list" }

func (e *ZoomParticipantListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	accountID := templateResolver.ResolveString(getString(config, "accountId"))
	clientID := templateResolver.ResolveString(getString(config, "clientId"))
	clientSecret := templateResolver.ResolveString(getString(config, "clientSecret"))

	if accountID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("accountId, clientId, and clientSecret are required")
	}

	meetingID := templateResolver.ResolveString(getString(config, "meetingId"))
	if meetingID == "" {
		return nil, fmt.Errorf("meetingId is required")
	}

	client := getZoomClient(accountID, clientID, clientSecret)

	// Build query parameters
	params := url.Values{}
	params.Set("page_size", strconv.Itoa(getInt(config, "pageSize", 30)))

	if nextPageToken := getString(config, "nextPageToken"); nextPageToken != "" {
		params.Set("next_page_token", templateResolver.ResolveString(nextPageToken))
	}

	path := fmt.Sprintf("/meetings/%s/participants?%s", meetingID, params.Encode())

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp ZoomParticipantListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Convert participants to output format
	var participants []map[string]interface{}
	for _, p := range listResp.Participants {
		participants = append(participants, map[string]interface{}{
			"id":          p.ID,
			"name":        p.Name,
			"email":       p.Email,
			"join_time":   p.JoinTime,
			"leave_time":  p.LeaveTime,
			"duration":    p.Duration,
			"status":      p.Status,
			"user_id":     p.UserID,
		})
	}

	output := map[string]interface{}{
		"participants":    participants,
		"total_count":     listResp.TotalCount,
		"page_size":       listResp.PageSize,
		"next_page_token": listResp.NextPageToken,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// ZOOM-USER-LIST EXECUTOR
// ============================================================================

// ZoomUserListConfig defines the configuration for zoom-user-list
type ZoomUserListConfig struct {
	AccountID    string `json:"accountId" description:"Zoom account ID"`
	ClientID     string `json:"clientId" description:"Zoom OAuth client ID"`
	ClientSecret string `json:"clientSecret" description:"Zoom OAuth client secret"`
	Status       string `json:"status" description:"User status filter (active/inactive/pending)"`
	Role         string `json:"role" description:"User role filter (admin/member)"`
	PageSize     int    `json:"pageSize" default:"30" description:"Results per page"`
	PageNumber   int    `json:"pageNumber" default:"1" description:"Page number (1-indexed)"`
}

type ZoomUserListExecutor struct{}

func (e *ZoomUserListExecutor) Type() string { return "zoom-user-list" }

func (e *ZoomUserListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	accountID := templateResolver.ResolveString(getString(config, "accountId"))
	clientID := templateResolver.ResolveString(getString(config, "clientId"))
	clientSecret := templateResolver.ResolveString(getString(config, "clientSecret"))

	if accountID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("accountId, clientId, and clientSecret are required")
	}

	client := getZoomClient(accountID, clientID, clientSecret)

	// Build query parameters
	params := url.Values{}
	params.Set("page_size", strconv.Itoa(getInt(config, "pageSize", 30)))
	params.Set("page_number", strconv.Itoa(getInt(config, "pageNumber", 1)))

	if status := getString(config, "status"); status != "" {
		params.Set("status", templateResolver.ResolveString(status))
	}

	if role := getString(config, "role"); role != "" {
		params.Set("role", templateResolver.ResolveString(role))
	}

	path := fmt.Sprintf("/users?%s", params.Encode())

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp ZoomUserListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Convert users to output format
	var users []map[string]interface{}
	for _, u := range listResp.Users {
		userMap := map[string]interface{}{
			"id":            u.ID,
			"first_name":    u.FirstName,
			"last_name":     u.LastName,
			"email":         u.Email,
			"type":          u.Type,
			"role_name":     u.RoleName,
			"status":        u.Status,
			"verified":      u.Verified,
			"dept":          u.Dept,
			"created_at":    u.CreatedAt,
			"last_login_time": u.LastLoginTime,
			"pmi":           u.Pmi,
			"phone_number":  u.PhoneNumber,
			"language":      u.Language,
			"timezone":      u.Timezone,
		}
		if u.PicURL != "" {
			userMap["pic_url"] = u.PicURL
		}
		users = append(users, userMap)
	}

	output := map[string]interface{}{
		"users":           users,
		"total_count":     listResp.TotalCount,
		"page_count":      listResp.PageCount,
		"page_size":       listResp.PageSize,
		"page_number":     listResp.PageNumber,
		"next_page_token": listResp.NextPageToken,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// ZOOM-WEBINAR-CREATE EXECUTOR
// ============================================================================

// ZoomWebinarCreateConfig defines the configuration for zoom-webinar-create
type ZoomWebinarCreateConfig struct {
	AccountID       string `json:"accountId" description:"Zoom account ID"`
	ClientID        string `json:"clientId" description:"Zoom OAuth client ID"`
	ClientSecret    string `json:"clientSecret" description:"Zoom OAuth client secret"`
	Topic           string `json:"topic" description:"Webinar topic"`
	Agenda          string `json:"agenda" description:"Webinar agenda"`
	Type            int    `json:"type" default:"5" description:"Webinar type (5=webinar, 6=webinar with practice)"`
	StartTime       string `json:"startTime" description:"Webinar start time (ISO 8601)"`
	Timezone        string `json:"timezone" default:"America/New_York" description:"Webinar timezone"`
	Duration        int    `json:"duration" default:"60" description:"Webinar duration in minutes"`
	HostVideo       bool   `json:"hostVideo" default:"true" description:"Start with host video on"`
	PanelistsVideo  bool   `json:"panelistsVideo" default:"true" description:"Start with panelists video on"`
	Audio           string `json:"audio" default:"both" description:"Audio options"`
	PracticeSession bool   `json:"practiceSession" description:"Enable practice session"`
	OnDemand        bool   `json:"onDemand" description:"Enable on-demand viewing"`
	QuestionAndAnswer bool `json:"questionAndAnswer" default:"true" description:"Enable Q&A"`
	Chat            string `json:"chat" default:"all" description:"Chat settings (none/host/all)"`
}

type ZoomWebinarCreateExecutor struct{}

func (e *ZoomWebinarCreateExecutor) Type() string { return "zoom-webinar-create" }

func (e *ZoomWebinarCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	accountID := templateResolver.ResolveString(getString(config, "accountId"))
	clientID := templateResolver.ResolveString(getString(config, "clientId"))
	clientSecret := templateResolver.ResolveString(getString(config, "clientSecret"))

	if accountID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("accountId, clientId, and clientSecret are required")
	}

	topic := templateResolver.ResolveString(getString(config, "topic"))
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	startTime := templateResolver.ResolveString(getString(config, "startTime"))
	if startTime == "" {
		return nil, fmt.Errorf("startTime is required")
	}

	client := getZoomClient(accountID, clientID, clientSecret)

	// Build webinar request
	webinarData := map[string]interface{}{
		"topic":      topic,
		"type":       getInt(config, "type", 5),
		"start_time": startTime,
		"duration":   getInt(config, "duration", 60),
		"timezone":   getString(config, "timezone"),
		"agenda":     getString(config, "agenda"),
		"settings": map[string]interface{}{
			"host_video":         getBool(config, "hostVideo", true),
			"panelists_video":    getBool(config, "panelistsVideo", true),
			"audio":              getString(config, "audio"),
			"practice_session":   getBool(config, "practiceSession", false),
			"ondemand":           getBool(config, "onDemand", false),
			"question_and_answer": getBool(config, "questionAndAnswer", true),
			"chat":               getString(config, "chat"),
		},
	}

	resp, err := client.doRequest(ctx, "POST", "/users/me/webinars", webinarData)
	if err != nil {
		return nil, err
	}

	var webinar ZoomWebinar
	if err := decodeResponse(resp, &webinar); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"id":         webinar.ID,
		"uuid":       webinar.UUID,
		"topic":      webinar.Topic,
		"join_url":   webinar.JoinURL,
		"start_time": webinar.StartTime,
		"duration":   webinar.Duration,
		"settings":   webinar.Settings,
		"status":     webinar.Status,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50120"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-zoom", "1.0.0")

	// Register Zoom executors with schemas
	server.RegisterExecutorWithSchema("zoom-meeting-create", &ZoomMeetingCreateExecutor{}, ZoomMeetingCreateSchema)
	server.RegisterExecutorWithSchema("zoom-meeting-list", &ZoomMeetingListExecutor{}, ZoomMeetingListSchema)
	server.RegisterExecutorWithSchema("zoom-meeting-get", &ZoomMeetingGetExecutor{}, ZoomMeetingGetSchema)
	server.RegisterExecutorWithSchema("zoom-meeting-delete", &ZoomMeetingDeleteExecutor{}, ZoomMeetingDeleteSchema)
	server.RegisterExecutorWithSchema("zoom-recording-list", &ZoomRecordingListExecutor{}, ZoomRecordingListSchema)
	server.RegisterExecutorWithSchema("zoom-participant-list", &ZoomParticipantListExecutor{}, ZoomParticipantListSchema)
	server.RegisterExecutorWithSchema("zoom-user-list", &ZoomUserListExecutor{}, ZoomUserListSchema)
	server.RegisterExecutorWithSchema("zoom-webinar-create", &ZoomWebinarCreateExecutor{}, ZoomWebinarCreateSchema)

	fmt.Printf("Starting skill-zoom gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %w\n", err)
		os.Exit(1)
	}
}
