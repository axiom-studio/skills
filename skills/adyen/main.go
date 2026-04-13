package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconAdyen = "credit-card"

	// Adyen API endpoints
	adyenAPIVersion   = "v70"
	defaultLiveURL    = "https://pal-live.adyenpayments.com/pal/servlet"
	defaultTestURL    = "https://pal-test.adyenpayments.com/pal/servlet"
	defaultHPPTestURL = "https://test.adyen.com/hpp"
	defaultHPPLiveURL = "https://live.adyen.com/hpp"
)

// AdyenConfig holds Adyen API configuration
type AdyenConfig struct {
	APIKey          string `json:"apiKey" description:"Adyen API key"`
	MerchantAccount string `json:"merchantAccount" description:"Adyen merchant account"`
	Environment     string `json:"environment" default:"test" options:"Test:test,Live:live" description:"Adyen environment"`
}

// HTTP client cache
var (
	httpClients = make(map[string]*http.Client)
	clientMux   sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50118"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-adyen", "1.0.0")

	// Register Payment executors with schemas
	server.RegisterExecutorWithSchema("adyen-payment", &PaymentExecutor{}, PaymentSchema)
	server.RegisterExecutorWithSchema("adyen-capture", &CaptureExecutor{}, CaptureSchema)
	server.RegisterExecutorWithSchema("adyen-refund", &RefundExecutor{}, RefundSchema)
	server.RegisterExecutorWithSchema("adyen-cancel", &CancelExecutor{}, CancelSchema)
	server.RegisterExecutorWithSchema("adyen-payment-list", &PaymentListExecutor{}, PaymentListSchema)
	server.RegisterExecutorWithSchema("adyen-payment-get", &PaymentGetExecutor{}, PaymentGetSchema)
	server.RegisterExecutorWithSchema("adyen-payout", &PayoutExecutor{}, PayoutSchema)
	server.RegisterExecutorWithSchema("adyen-3ds-authenticate", &ThreeDSAuthenticateExecutor{}, ThreeDSAuthenticateSchema)

	fmt.Printf("Starting skill-adyen gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// ADYEN API CLIENT
// ============================================================================

// getBaseURL returns the appropriate base URL for the environment
func getBaseURL(env string) string {
	if strings.ToLower(env) == "live" {
		return defaultLiveURL
	}
	return defaultTestURL
}

// getHTTPClient returns an HTTP client (cached)
func getHTTPClient() *http.Client {
	clientMux.RLock()
	client, ok := httpClients["default"]
	clientMux.RUnlock()

	if ok {
		return client
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	if client, ok := httpClients["default"]; ok {
		return client
	}

	client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	httpClients["default"] = client
	return client
}

// adyenRequest performs an Adyen API request
func adyenRequest(ctx context.Context, cfg AdyenConfig, endpoint string, requestBody interface{}) (map[string]interface{}, error) {
	baseURL := getBaseURL(cfg.Environment)
	url := fmt.Sprintf("%s/%s/%s", baseURL, adyenAPIVersion, endpoint)

	var reqBody []byte
	if requestBody != nil {
		var err error
		reqBody, err = json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)
	req.Header.Set("User-Agent", "skill-adyen/1.0.0")

	// Make request
	client := getHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errorResp map[string]interface{}
		if err := json.Unmarshal(respBody, &errorResp); err == nil {
			return nil, fmt.Errorf("Adyen API error (%d): %v", resp.StatusCode, errorResp)
		}
		return nil, fmt.Errorf("Adyen API error (%d): %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// adyenGetRequest performs an Adyen API GET request
func adyenGetRequest(ctx context.Context, cfg AdyenConfig, endpoint string, queryParams map[string]string) (map[string]interface{}, error) {
	baseURL := getBaseURL(cfg.Environment)
	url := fmt.Sprintf("%s/%s/%s", baseURL, adyenAPIVersion, endpoint)

	// Add query parameters
	if len(queryParams) > 0 {
		params := make([]string, 0, len(queryParams))
		for k, v := range queryParams {
			params = append(params, fmt.Sprintf("%s=%s", k, v))
		}
		url = url + "?" + strings.Join(params, "&")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)
	req.Header.Set("User-Agent", "skill-adyen/1.0.0")

	// Make request
	client := getHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errorResp map[string]interface{}
		if err := json.Unmarshal(respBody, &errorResp); err == nil {
			return nil, fmt.Errorf("Adyen API error (%d): %v", resp.StatusCode, errorResp)
		}
		return nil, fmt.Errorf("Adyen API error (%d): %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
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

// Helper to get map from config
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
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

// parseAdyenConfig extracts Adyen configuration from config map
func parseAdyenConfig(config map[string]interface{}) AdyenConfig {
	env := getString(config, "environment")
	if env == "" {
		env = "test"
	}
	return AdyenConfig{
		APIKey:          getString(config, "apiKey"),
		MerchantAccount: getString(config, "merchantAccount"),
		Environment:     env,
	}
}

// ============================================================================
// SCHEMAS
// ============================================================================

// PaymentSchema is the UI schema for adyen-payment
var PaymentSchema = resolver.NewSchemaBuilder("adyen-payment").
	WithName("Process Payment").
	WithCategory("payment").
	WithIcon(iconAdyen).
	WithDescription("Process a payment through Adyen").
	AddSection("Authentication").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithSensitive(),
		resolver.WithPlaceholder("YOUR_API_KEY"),
		resolver.WithHint("Adyen API key from Customer Area"),
	).
	AddExpressionField("merchantAccount", "Merchant Account",
		resolver.WithRequired(),
		resolver.WithPlaceholder("YourMerchantAccount"),
		resolver.WithHint("Your Adyen merchant account name"),
	).
	AddSelectField("environment", "Environment",
		[]resolver.SelectOption{
			{Label: "Test", Value: "test"},
			{Label: "Live", Value: "live"},
		},
		resolver.WithDefault("test"),
		resolver.WithHint("Select test or live environment"),
	).
	EndSection().
	AddSection("Payment Details").
	AddExpressionField("reference", "Reference",
		resolver.WithRequired(),
		resolver.WithPlaceholder("order-123"),
		resolver.WithHint("Your unique reference for this payment"),
	).
	AddJSONField("amount", "Amount",
		resolver.WithRequired(),
		resolver.WithHeight(100),
		resolver.WithHint(`{"currency": "USD", "value": 9999} - value in minor units`),
	).
	AddJSONField("paymentMethod", "Payment Method",
		resolver.WithRequired(),
		resolver.WithHeight(150),
		resolver.WithHint(`Payment method details (card, bank transfer, etc.)`),
	).
	EndSection().
	AddSection("Shopper Details").
	AddExpressionField("shopperEmail", "Shopper Email",
		resolver.WithPlaceholder("shopper@example.com"),
		resolver.WithHint("Shopper's email address"),
	).
	AddExpressionField("shopperReference", "Shopper Reference",
		resolver.WithPlaceholder("shopper-123"),
		resolver.WithHint("Your unique identifier for the shopper"),
	).
	AddExpressionField("shopperName", "Shopper Name",
		resolver.WithPlaceholder(`{"firstName": "John", "lastName": "Doe"}`),
		resolver.WithHint("Shopper's name (JSON format)"),
	).
	AddExpressionField("telephoneNumber", "Phone Number",
		resolver.WithPlaceholder("+1-555-123-4567"),
		resolver.WithHint("Shopper's phone number"),
	).
	AddExpressionField("dateOfBirth", "Date of Birth",
		resolver.WithPlaceholder("1990-01-01"),
		resolver.WithHint("Shopper's date of birth (YYYY-MM-DD)"),
	).
	EndSection().
	AddSection("Billing Address").
	AddJSONField("billingAddress", "Billing Address",
		resolver.WithHeight(150),
		resolver.WithHint(`{"street": "123 Main St", "city": "New York", "postalCode": "10001", "country": "US"}`),
	).
	EndSection().
	AddSection("Shipping Address").
	AddJSONField("deliveryAddress", "Delivery Address",
		resolver.WithHeight(150),
		resolver.WithHint(`{"street": "123 Main St", "city": "New York", "postalCode": "10001", "country": "US"}`),
	).
	EndSection().
	AddSection("Options").
	AddToggleField("captureDelayHours", "Capture Delay",
		resolver.WithDefault(false),
		resolver.WithHint("Enable delayed capture"),
	).
	AddNumberField("captureDelayHoursValue", "Capture Delay Hours",
		resolver.WithDefault(0),
		resolver.WithHint("Hours before automatic capture (0 = manual capture)"),
	).
	AddJSONField("additionalData", "Additional Data",
		resolver.WithHeight(100),
		resolver.WithHint("Additional data for the payment"),
	).
	AddExpressionField("recurringProcessingModel", "Recurring Model",
		resolver.WithPlaceholder("CardOnFile, Subscription, or UnscheduledCardOnFile"),
		resolver.WithHint("Recurring processing model for stored payment methods"),
	).
	AddExpressionField("storePaymentMethod", "Store Payment Method",
		resolver.WithPlaceholder("true"),
		resolver.WithHint("Store payment method for future payments"),
	).
	EndSection().
	Build()

// CaptureSchema is the UI schema for adyen-capture
var CaptureSchema = resolver.NewSchemaBuilder("adyen-capture").
	WithName("Capture Payment").
	WithCategory("payment").
	WithIcon(iconAdyen).
	WithDescription("Capture a previously authorized payment").
	AddSection("Authentication").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	AddExpressionField("merchantAccount", "Merchant Account",
		resolver.WithRequired(),
	).
	AddSelectField("environment", "Environment",
		[]resolver.SelectOption{
			{Label: "Test", Value: "test"},
			{Label: "Live", Value: "live"},
		},
		resolver.WithDefault("test"),
	).
	EndSection().
	AddSection("Capture Details").
	AddExpressionField("pspReference", "PSP Reference",
		resolver.WithRequired(),
		resolver.WithPlaceholder("8816178952380553"),
		resolver.WithHint("Original payment PSP reference"),
	).
	AddExpressionField("reference", "Reference",
		resolver.WithPlaceholder("capture-123"),
		resolver.WithHint("Your unique reference for this capture"),
	).
	AddJSONField("amount", "Amount",
		resolver.WithHeight(100),
		resolver.WithHint(`{"currency": "USD", "value": 9999} - optional, defaults to full amount`),
	).
	EndSection().
	Build()

// RefundSchema is the UI schema for adyen-refund
var RefundSchema = resolver.NewSchemaBuilder("adyen-refund").
	WithName("Refund Payment").
	WithCategory("payment").
	WithIcon(iconAdyen).
	WithDescription("Refund a previously captured payment").
	AddSection("Authentication").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	AddExpressionField("merchantAccount", "Merchant Account",
		resolver.WithRequired(),
	).
	AddSelectField("environment", "Environment",
		[]resolver.SelectOption{
			{Label: "Test", Value: "test"},
			{Label: "Live", Value: "live"},
		},
		resolver.WithDefault("test"),
	).
	EndSection().
	AddSection("Refund Details").
	AddExpressionField("pspReference", "PSP Reference",
		resolver.WithRequired(),
		resolver.WithPlaceholder("8816178952380553"),
		resolver.WithHint("Original payment PSP reference"),
	).
	AddExpressionField("reference", "Reference",
		resolver.WithPlaceholder("refund-123"),
		resolver.WithHint("Your unique reference for this refund"),
	).
	AddJSONField("amount", "Amount",
		resolver.WithHeight(100),
		resolver.WithHint(`{"currency": "USD", "value": 9999} - optional, defaults to full amount`),
	).
	EndSection().
	AddSection("Options").
	AddToggleField("reverseAuthorization", "Reverse Authorization",
		resolver.WithDefault(false),
		resolver.WithHint("Reverse the authorization instead of refunding"),
	).
	AddExpressionField("description", "Description",
		resolver.WithPlaceholder("Refund for order 123"),
		resolver.WithHint("Description for the refund"),
	).
	EndSection().
	Build()

// CancelSchema is the UI schema for adyen-cancel
var CancelSchema = resolver.NewSchemaBuilder("adyen-cancel").
	WithName("Cancel Payment").
	WithCategory("payment").
	WithIcon(iconAdyen).
	WithDescription("Cancel a previously authorized payment").
	AddSection("Authentication").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	AddExpressionField("merchantAccount", "Merchant Account",
		resolver.WithRequired(),
	).
	AddSelectField("environment", "Environment",
		[]resolver.SelectOption{
			{Label: "Test", Value: "test"},
			{Label: "Live", Value: "live"},
		},
		resolver.WithDefault("test"),
	).
	EndSection().
	AddSection("Cancel Details").
	AddExpressionField("pspReference", "PSP Reference",
		resolver.WithRequired(),
		resolver.WithPlaceholder("8816178952380553"),
		resolver.WithHint("Original payment PSP reference"),
	).
	AddExpressionField("reference", "Reference",
		resolver.WithPlaceholder("cancel-123"),
		resolver.WithHint("Your unique reference for this cancel"),
	).
	EndSection().
	Build()

// PaymentListSchema is the UI schema for adyen-payment-list
var PaymentListSchema = resolver.NewSchemaBuilder("adyen-payment-list").
	WithName("List Payments").
	WithCategory("payment").
	WithIcon(iconAdyen).
	WithDescription("List payments with optional filters").
	AddSection("Authentication").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	AddExpressionField("merchantAccount", "Merchant Account",
		resolver.WithRequired(),
	).
	AddSelectField("environment", "Environment",
		[]resolver.SelectOption{
			{Label: "Test", Value: "test"},
			{Label: "Live", Value: "live"},
		},
		resolver.WithDefault("test"),
	).
	EndSection().
	AddSection("Filters").
	AddExpressionField("shopperReference", "Shopper Reference",
		resolver.WithPlaceholder("shopper-123"),
		resolver.WithHint("Filter by shopper reference"),
	).
	AddExpressionField("shopperEmail", "Shopper Email",
		resolver.WithPlaceholder("shopper@example.com"),
		resolver.WithHint("Filter by shopper email"),
	).
	AddExpressionField("paymentReference", "Payment Reference",
		resolver.WithPlaceholder("order-123"),
		resolver.WithHint("Filter by payment reference"),
	).
	EndSection().
	AddSection("Pagination").
	AddNumberField("pageSize", "Page Size",
		resolver.WithDefault(10),
		resolver.WithMinMax(1, 100),
		resolver.WithHint("Number of results per page"),
	).
	AddExpressionField("page", "Page",
		resolver.WithDefault("1"),
		resolver.WithHint("Page number"),
	).
	EndSection().
	Build()

// PaymentGetSchema is the UI schema for adyen-payment-get
var PaymentGetSchema = resolver.NewSchemaBuilder("adyen-payment-get").
	WithName("Get Payment Details").
	WithCategory("payment").
	WithIcon(iconAdyen).
	WithDescription("Get details of a specific payment").
	AddSection("Authentication").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	AddExpressionField("merchantAccount", "Merchant Account",
		resolver.WithRequired(),
	).
	AddSelectField("environment", "Environment",
		[]resolver.SelectOption{
			{Label: "Test", Value: "test"},
			{Label: "Live", Value: "live"},
		},
		resolver.WithDefault("test"),
	).
	EndSection().
	AddSection("Payment Identification").
	AddExpressionField("pspReference", "PSP Reference",
		resolver.WithRequired(),
		resolver.WithPlaceholder("8816178952380553"),
		resolver.WithHint("PSP reference of the payment"),
	).
	EndSection().
	Build()

// PayoutSchema is the UI schema for adyen-payout
var PayoutSchema = resolver.NewSchemaBuilder("adyen-payout").
	WithName("Process Payout").
	WithCategory("payment").
	WithIcon(iconAdyen).
	WithDescription("Process a payout to a shopper").
	AddSection("Authentication").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	AddExpressionField("merchantAccount", "Merchant Account",
		resolver.WithRequired(),
	).
	AddSelectField("environment", "Environment",
		[]resolver.SelectOption{
			{Label: "Test", Value: "test"},
			{Label: "Live", Value: "live"},
		},
		resolver.WithDefault("test"),
	).
	EndSection().
	AddSection("Payout Details").
	AddExpressionField("reference", "Reference",
		resolver.WithRequired(),
		resolver.WithPlaceholder("payout-123"),
		resolver.WithHint("Your unique reference for this payout"),
	).
	AddJSONField("amount", "Amount",
		resolver.WithRequired(),
		resolver.WithHeight(100),
		resolver.WithHint(`{"currency": "USD", "value": 9999}`),
	).
	AddExpressionField("shopperReference", "Shopper Reference",
		resolver.WithRequired(),
		resolver.WithPlaceholder("shopper-123"),
		resolver.WithHint("Unique identifier for the shopper"),
	).
	AddExpressionField("shopperEmail", "Shopper Email",
		resolver.WithPlaceholder("shopper@example.com"),
		resolver.WithHint("Shopper's email address"),
	).
	EndSection().
	AddSection("Payout Method").
	AddJSONField("payoutMethod", "Payout Method",
		resolver.WithHeight(150),
		resolver.WithHint("Payout method details (stored or new)"),
	).
	AddExpressionField("recurringDetailReference", "Recurring Detail Reference",
		resolver.WithPlaceholder("8416178952380553"),
		resolver.WithHint("Reference to stored payout method"),
	).
	EndSection().
	AddSection("Shopper Details").
	AddExpressionField("shopperName", "Shopper Name",
		resolver.WithPlaceholder(`{"firstName": "John", "lastName": "Doe"}`),
	).
	AddExpressionField("dateOfBirth", "Date of Birth",
		resolver.WithPlaceholder("1990-01-01"),
	).
	AddExpressionField("telephoneNumber", "Phone Number",
		resolver.WithPlaceholder("+1-555-123-4567"),
	).
	AddJSONField("billingAddress", "Billing Address",
		resolver.WithHeight(100),
	).
	EndSection().
	Build()

// ThreeDSAuthenticateSchema is the UI schema for adyen-3ds-authenticate
var ThreeDSAuthenticateSchema = resolver.NewSchemaBuilder("adyen-3ds-authenticate").
	WithName("3D Secure Authentication").
	WithCategory("payment").
	WithIcon(iconAdyen).
	WithDescription("Authenticate a payment with 3D Secure").
	AddSection("Authentication").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	AddExpressionField("merchantAccount", "Merchant Account",
		resolver.WithRequired(),
	).
	AddSelectField("environment", "Environment",
		[]resolver.SelectOption{
			{Label: "Test", Value: "test"},
			{Label: "Live", Value: "live"},
		},
		resolver.WithDefault("test"),
	).
	EndSection().
	AddSection("Payment Details").
	AddExpressionField("reference", "Reference",
		resolver.WithRequired(),
		resolver.WithPlaceholder("order-123"),
	).
	AddJSONField("amount", "Amount",
		resolver.WithRequired(),
		resolver.WithHeight(100),
		resolver.WithHint(`{"currency": "USD", "value": 9999}`),
	).
	AddJSONField("paymentMethod", "Payment Method",
		resolver.WithRequired(),
		resolver.WithHeight(150),
		resolver.WithHint("Payment method details"),
	).
	EndSection().
	AddSection("3DS Options").
	AddJSONField("browserInfo", "Browser Info",
		resolver.WithHeight(100),
		resolver.WithHint(`{"userAgent": "...", "acceptHeader": "...", "screenWidth": 1920, "screenHeight": 1080, "colorDepth": 24, "timeZoneOffset": -60, "language": "en-US"}`),
	).
	AddExpressionField("threeDSAuthenticationOnly", "Authentication Only",
		resolver.WithPlaceholder("true"),
		resolver.WithHint("Only perform 3DS authentication without payment"),
	).
	AddExpressionField("executeThreeD", "Execute 3D Secure",
		resolver.WithPlaceholder("true"),
		resolver.WithHint("Enable 3D Secure authentication"),
	).
	EndSection().
	AddSection("Shopper Details").
	AddExpressionField("shopperEmail", "Shopper Email",
		resolver.WithPlaceholder("shopper@example.com"),
	).
	AddExpressionField("shopperReference", "Shopper Reference",
		resolver.WithPlaceholder("shopper-123"),
	).
	AddJSONField("billingAddress", "Billing Address",
		resolver.WithHeight(100),
	).
	EndSection().
	AddSection("Return URLs").
	AddExpressionField("returnUrl", "Return URL",
		resolver.WithPlaceholder("https://your-site.com/return"),
		resolver.WithHint("URL to redirect shopper after 3DS authentication"),
	).
	AddExpressionField("channel", "Channel",
		resolver.WithDefault("Web"),
		resolver.WithPlaceholder("Web, iOS, Android"),
		resolver.WithHint("Payment channel"),
	).
	EndSection().
	Build()

// ============================================================================
// PAYMENT EXECUTOR
// ============================================================================

// PaymentExecutor handles adyen-payment node type
type PaymentExecutor struct{}

func (e *PaymentExecutor) Type() string { return "adyen-payment" }

func (e *PaymentExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Parse Adyen config
	adyenCfg := AdyenConfig{
		APIKey:          resolver.ResolveString(getString(config, "apiKey")),
		MerchantAccount: resolver.ResolveString(getString(config, "merchantAccount")),
		Environment:     resolver.ResolveString(getString(config, "environment")),
	}
	if adyenCfg.Environment == "" {
		adyenCfg.Environment = "test"
	}

	if adyenCfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if adyenCfg.MerchantAccount == "" {
		return nil, fmt.Errorf("merchantAccount is required")
	}

	// Build payment request
	reference := resolver.ResolveString(getString(config, "reference"))
	if reference == "" {
		return nil, fmt.Errorf("reference is required")
	}

	// Parse amount
	amountRaw := getMap(config, "amount")
	if amountRaw == nil {
		return nil, fmt.Errorf("amount is required")
	}
	amount := map[string]interface{}{
		"currency": resolver.ResolveString(getString(amountRaw, "currency")),
		"value":    getInt(amountRaw, "value", 0),
	}

	// Parse payment method
	paymentMethodRaw := getMap(config, "paymentMethod")
	if paymentMethodRaw == nil {
		return nil, fmt.Errorf("paymentMethod is required")
	}

	// Build request body
	requestBody := map[string]interface{}{
		"merchantAccount": adyenCfg.MerchantAccount,
		"reference":       reference,
		"amount":          amount,
		"paymentMethod":   paymentMethodRaw,
	}

	// Add optional fields
	if shopperEmail := resolver.ResolveString(getString(config, "shopperEmail")); shopperEmail != "" {
		requestBody["shopperEmail"] = shopperEmail
	}
	if shopperReference := resolver.ResolveString(getString(config, "shopperReference")); shopperReference != "" {
		requestBody["shopperReference"] = shopperReference
	}
	if shopperName := getString(config, "shopperName"); shopperName != "" {
		var shopperNameMap map[string]interface{}
		if err := json.Unmarshal([]byte(shopperName), &shopperNameMap); err == nil {
			requestBody["shopperName"] = shopperNameMap
		}
	}
	if telephoneNumber := resolver.ResolveString(getString(config, "telephoneNumber")); telephoneNumber != "" {
		requestBody["telephoneNumber"] = telephoneNumber
	}
	if dateOfBirth := resolver.ResolveString(getString(config, "dateOfBirth")); dateOfBirth != "" {
		requestBody["dateOfBirth"] = dateOfBirth
	}

	// Billing address
	if billingAddress := getMap(config, "billingAddress"); billingAddress != nil {
		requestBody["billingAddress"] = billingAddress
	}

	// Delivery address
	if deliveryAddress := getMap(config, "deliveryAddress"); deliveryAddress != nil {
		requestBody["deliveryAddress"] = deliveryAddress
	}

	// Capture delay
	if getBool(config, "captureDelayHours", false) {
		requestBody["captureDelayHours"] = getInt(config, "captureDelayHoursValue", 0)
	}

	// Additional data
	if additionalData := getMap(config, "additionalData"); additionalData != nil {
		requestBody["additionalData"] = additionalData
	}

	// Recurring processing model
	if recurringModel := resolver.ResolveString(getString(config, "recurringProcessingModel")); recurringModel != "" {
		requestBody["recurringProcessingModel"] = recurringModel
	}

	// Store payment method
	if storePaymentMethod := resolver.ResolveString(getString(config, "storePaymentMethod")); storePaymentMethod != "" {
		if storePaymentMethod == "true" {
			requestBody["storePaymentMethod"] = true
		}
	}

	// Make API call
	result, err := adyenRequest(ctx, adyenCfg, "payments", requestBody)
	if err != nil {
		return nil, fmt.Errorf("payment failed: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":           true,
		"pspReference":      result["pspReference"],
		"resultCode":        result["resultCode"],
		"merchantReference": reference,
		"response":          result,
	}

	// Add action if present (for 3DS redirect)
	if action, ok := result["action"]; ok {
		output["action"] = action
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// CAPTURE EXECUTOR
// ============================================================================

// CaptureExecutor handles adyen-capture node type
type CaptureExecutor struct{}

func (e *CaptureExecutor) Type() string { return "adyen-capture" }

func (e *CaptureExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Parse Adyen config
	adyenCfg := AdyenConfig{
		APIKey:          resolver.ResolveString(getString(config, "apiKey")),
		MerchantAccount: resolver.ResolveString(getString(config, "merchantAccount")),
		Environment:     resolver.ResolveString(getString(config, "environment")),
	}
	if adyenCfg.Environment == "" {
		adyenCfg.Environment = "test"
	}

	if adyenCfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if adyenCfg.MerchantAccount == "" {
		return nil, fmt.Errorf("merchantAccount is required")
	}

	// Build capture request
	pspReference := resolver.ResolveString(getString(config, "pspReference"))
	if pspReference == "" {
		return nil, fmt.Errorf("pspReference is required")
	}

	reference := resolver.ResolveString(getString(config, "reference"))
	if reference == "" {
		reference = fmt.Sprintf("capture-%s", pspReference)
	}

	// Build request body
	requestBody := map[string]interface{}{
		"merchantAccount": adyenCfg.MerchantAccount,
		"reference":       reference,
	}

	// Add amount if provided (optional - defaults to full amount)
	if amountRaw := getMap(config, "amount"); amountRaw != nil {
		requestBody["amount"] = map[string]interface{}{
			"currency": resolver.ResolveString(getString(amountRaw, "currency")),
			"value":    getInt(amountRaw, "value", 0),
		}
	}

	// Make API call
	result, err := adyenRequest(ctx, adyenCfg, fmt.Sprintf("payments/%s/captures", pspReference), requestBody)
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":      true,
		"pspReference": result["pspReference"],
		"response":     result,
		"status":       result["status"],
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// REFUND EXECUTOR
// ============================================================================

// RefundExecutor handles adyen-refund node type
type RefundExecutor struct{}

func (e *RefundExecutor) Type() string { return "adyen-refund" }

func (e *RefundExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Parse Adyen config
	adyenCfg := AdyenConfig{
		APIKey:          resolver.ResolveString(getString(config, "apiKey")),
		MerchantAccount: resolver.ResolveString(getString(config, "merchantAccount")),
		Environment:     resolver.ResolveString(getString(config, "environment")),
	}
	if adyenCfg.Environment == "" {
		adyenCfg.Environment = "test"
	}

	if adyenCfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if adyenCfg.MerchantAccount == "" {
		return nil, fmt.Errorf("merchantAccount is required")
	}

	// Build refund request
	pspReference := resolver.ResolveString(getString(config, "pspReference"))
	if pspReference == "" {
		return nil, fmt.Errorf("pspReference is required")
	}

	reference := resolver.ResolveString(getString(config, "reference"))
	if reference == "" {
		reference = fmt.Sprintf("refund-%s", pspReference)
	}

	// Build request body
	requestBody := map[string]interface{}{
		"merchantAccount": adyenCfg.MerchantAccount,
		"reference":       reference,
	}

	// Add amount if provided (optional - defaults to full amount)
	if amountRaw := getMap(config, "amount"); amountRaw != nil {
		requestBody["amount"] = map[string]interface{}{
			"currency": resolver.ResolveString(getString(amountRaw, "currency")),
			"value":    getInt(amountRaw, "value", 0),
		}
	}

	// Add description if provided
	if description := resolver.ResolveString(getString(config, "description")); description != "" {
		requestBody["description"] = description
	}

	// Reverse authorization instead of refund
	if getBool(config, "reverseAuthorization", false) {
		requestBody["reverseAuthorization"] = true
	}

	// Make API call
	result, err := adyenRequest(ctx, adyenCfg, fmt.Sprintf("payments/%s/refunds", pspReference), requestBody)
	if err != nil {
		return nil, fmt.Errorf("refund failed: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":      true,
		"pspReference": result["pspReference"],
		"response":     result,
		"status":       result["status"],
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// CANCEL EXECUTOR
// ============================================================================

// CancelExecutor handles adyen-cancel node type
type CancelExecutor struct{}

func (e *CancelExecutor) Type() string { return "adyen-cancel" }

func (e *CancelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Parse Adyen config
	adyenCfg := AdyenConfig{
		APIKey:          resolver.ResolveString(getString(config, "apiKey")),
		MerchantAccount: resolver.ResolveString(getString(config, "merchantAccount")),
		Environment:     resolver.ResolveString(getString(config, "environment")),
	}
	if adyenCfg.Environment == "" {
		adyenCfg.Environment = "test"
	}

	if adyenCfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if adyenCfg.MerchantAccount == "" {
		return nil, fmt.Errorf("merchantAccount is required")
	}

	// Build cancel request
	pspReference := resolver.ResolveString(getString(config, "pspReference"))
	if pspReference == "" {
		return nil, fmt.Errorf("pspReference is required")
	}

	reference := resolver.ResolveString(getString(config, "reference"))
	if reference == "" {
		reference = fmt.Sprintf("cancel-%s", pspReference)
	}

	// Build request body
	requestBody := map[string]interface{}{
		"merchantAccount": adyenCfg.MerchantAccount,
		"reference":       reference,
	}

	// Make API call
	result, err := adyenRequest(ctx, adyenCfg, fmt.Sprintf("payments/%s/cancels", pspReference), requestBody)
	if err != nil {
		return nil, fmt.Errorf("cancel failed: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":      true,
		"pspReference": result["pspReference"],
		"response":     result,
		"status":       result["status"],
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// PAYMENT LIST EXECUTOR
// ============================================================================

// PaymentListExecutor handles adyen-payment-list node type
type PaymentListExecutor struct{}

func (e *PaymentListExecutor) Type() string { return "adyen-payment-list" }

func (e *PaymentListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Parse Adyen config
	adyenCfg := AdyenConfig{
		APIKey:          resolver.ResolveString(getString(config, "apiKey")),
		MerchantAccount: resolver.ResolveString(getString(config, "merchantAccount")),
		Environment:     resolver.ResolveString(getString(config, "environment")),
	}
	if adyenCfg.Environment == "" {
		adyenCfg.Environment = "test"
	}

	if adyenCfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if adyenCfg.MerchantAccount == "" {
		return nil, fmt.Errorf("merchantAccount is required")
	}

	// Build query parameters
	queryParams := map[string]string{
		"merchantAccount": adyenCfg.MerchantAccount,
	}

	if shopperReference := resolver.ResolveString(getString(config, "shopperReference")); shopperReference != "" {
		queryParams["shopperReference"] = shopperReference
	}
	if shopperEmail := resolver.ResolveString(getString(config, "shopperEmail")); shopperEmail != "" {
		queryParams["shopperEmail"] = shopperEmail
	}
	if paymentReference := resolver.ResolveString(getString(config, "paymentReference")); paymentReference != "" {
		queryParams["paymentReference"] = paymentReference
	}

	// Pagination
	pageSize := getInt(config, "pageSize", 10)
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	queryParams["pageSize"] = fmt.Sprintf("%d", pageSize)

	page := resolver.ResolveString(getString(config, "page"))
	if page == "" {
		page = "1"
	}
	queryParams["page"] = page

	// Make API call
	result, err := adyenGetRequest(ctx, adyenCfg, "payments", queryParams)
	if err != nil {
		return nil, fmt.Errorf("payment list failed: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":  true,
		"payments": result["payments"],
		"total":    len(result["payments"].([]interface{})),
		"response": result,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// PAYMENT GET EXECUTOR
// ============================================================================

// PaymentGetExecutor handles adyen-payment-get node type
type PaymentGetExecutor struct{}

func (e *PaymentGetExecutor) Type() string { return "adyen-payment-get" }

func (e *PaymentGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Parse Adyen config
	adyenCfg := AdyenConfig{
		APIKey:          resolver.ResolveString(getString(config, "apiKey")),
		MerchantAccount: resolver.ResolveString(getString(config, "merchantAccount")),
		Environment:     resolver.ResolveString(getString(config, "environment")),
	}
	if adyenCfg.Environment == "" {
		adyenCfg.Environment = "test"
	}

	if adyenCfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if adyenCfg.MerchantAccount == "" {
		return nil, fmt.Errorf("merchantAccount is required")
	}

	// Build request
	pspReference := resolver.ResolveString(getString(config, "pspReference"))
	if pspReference == "" {
		return nil, fmt.Errorf("pspReference is required")
	}

	// Make API call
	result, err := adyenGetRequest(ctx, adyenCfg, fmt.Sprintf("payments/%s/details", pspReference), nil)
	if err != nil {
		return nil, fmt.Errorf("payment get failed: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":      true,
		"pspReference": pspReference,
		"payment":      result,
		"response":     result,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// PAYOUT EXECUTOR
// ============================================================================

// PayoutExecutor handles adyen-payout node type
type PayoutExecutor struct{}

func (e *PayoutExecutor) Type() string { return "adyen-payout" }

func (e *PayoutExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Parse Adyen config
	adyenCfg := AdyenConfig{
		APIKey:          resolver.ResolveString(getString(config, "apiKey")),
		MerchantAccount: resolver.ResolveString(getString(config, "merchantAccount")),
		Environment:     resolver.ResolveString(getString(config, "environment")),
	}
	if adyenCfg.Environment == "" {
		adyenCfg.Environment = "test"
	}

	if adyenCfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if adyenCfg.MerchantAccount == "" {
		return nil, fmt.Errorf("merchantAccount is required")
	}

	// Build payout request
	reference := resolver.ResolveString(getString(config, "reference"))
	if reference == "" {
		return nil, fmt.Errorf("reference is required")
	}

	shopperReference := resolver.ResolveString(getString(config, "shopperReference"))
	if shopperReference == "" {
		return nil, fmt.Errorf("shopperReference is required")
	}

	// Parse amount
	amountRaw := getMap(config, "amount")
	if amountRaw == nil {
		return nil, fmt.Errorf("amount is required")
	}
	amount := map[string]interface{}{
		"currency": resolver.ResolveString(getString(amountRaw, "currency")),
		"value":    getInt(amountRaw, "value", 0),
	}

	// Build request body
	requestBody := map[string]interface{}{
		"merchantAccount":  adyenCfg.MerchantAccount,
		"reference":        reference,
		"shopperReference": shopperReference,
		"amount":           amount,
	}

	// Add optional fields
	if shopperEmail := resolver.ResolveString(getString(config, "shopperEmail")); shopperEmail != "" {
		requestBody["shopperEmail"] = shopperEmail
	}

	// Payout method or recurring detail reference
	if payoutMethod := getMap(config, "payoutMethod"); payoutMethod != nil {
		requestBody["payoutMethod"] = payoutMethod
	}
	if recurringDetailRef := resolver.ResolveString(getString(config, "recurringDetailReference")); recurringDetailRef != "" {
		requestBody["recurringDetailReference"] = recurringDetailRef
	}

	// Shopper details
	if shopperName := getString(config, "shopperName"); shopperName != "" {
		var shopperNameMap map[string]interface{}
		if err := json.Unmarshal([]byte(shopperName), &shopperNameMap); err == nil {
			requestBody["shopperName"] = shopperNameMap
		}
	}
	if dateOfBirth := resolver.ResolveString(getString(config, "dateOfBirth")); dateOfBirth != "" {
		requestBody["dateOfBirth"] = dateOfBirth
	}
	if telephoneNumber := resolver.ResolveString(getString(config, "telephoneNumber")); telephoneNumber != "" {
		requestBody["telephoneNumber"] = telephoneNumber
	}
	if billingAddress := getMap(config, "billingAddress"); billingAddress != nil {
		requestBody["billingAddress"] = billingAddress
	}

	// Make API call - use payouts endpoint
	result, err := adyenRequest(ctx, adyenCfg, "payouts/submit", requestBody)
	if err != nil {
		return nil, fmt.Errorf("payout failed: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":           true,
		"pspReference":      result["pspReference"],
		"resultCode":        result["resultCode"],
		"merchantReference": reference,
		"response":          result,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// 3DS AUTHENTICATE EXECUTOR
// ============================================================================

// ThreeDSAuthenticateExecutor handles adyen-3ds-authenticate node type
type ThreeDSAuthenticateExecutor struct{}

func (e *ThreeDSAuthenticateExecutor) Type() string { return "adyen-3ds-authenticate" }

func (e *ThreeDSAuthenticateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Parse Adyen config
	adyenCfg := AdyenConfig{
		APIKey:          resolver.ResolveString(getString(config, "apiKey")),
		MerchantAccount: resolver.ResolveString(getString(config, "merchantAccount")),
		Environment:     resolver.ResolveString(getString(config, "environment")),
	}
	if adyenCfg.Environment == "" {
		adyenCfg.Environment = "test"
	}

	if adyenCfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if adyenCfg.MerchantAccount == "" {
		return nil, fmt.Errorf("merchantAccount is required")
	}

	// Build 3DS authentication request
	reference := resolver.ResolveString(getString(config, "reference"))
	if reference == "" {
		return nil, fmt.Errorf("reference is required")
	}

	// Parse amount
	amountRaw := getMap(config, "amount")
	if amountRaw == nil {
		return nil, fmt.Errorf("amount is required")
	}
	amount := map[string]interface{}{
		"currency": resolver.ResolveString(getString(amountRaw, "currency")),
		"value":    getInt(amountRaw, "value", 0),
	}

	// Parse payment method
	paymentMethodRaw := getMap(config, "paymentMethod")
	if paymentMethodRaw == nil {
		return nil, fmt.Errorf("paymentMethod is required")
	}

	// Build request body
	requestBody := map[string]interface{}{
		"merchantAccount": adyenCfg.MerchantAccount,
		"reference":       reference,
		"amount":          amount,
		"paymentMethod":   paymentMethodRaw,
	}

	// Browser info (required for 3DS)
	if browserInfo := getMap(config, "browserInfo"); browserInfo != nil {
		requestBody["browserInfo"] = browserInfo
	}

	// 3DS options
	if threeDSAuthOnly := resolver.ResolveString(getString(config, "threeDSAuthenticationOnly")); threeDSAuthOnly != "" {
		if threeDSAuthOnly == "true" {
			requestBody["threeDSAuthenticationOnly"] = true
		}
	}
	if executeThreeD := resolver.ResolveString(getString(config, "executeThreeD")); executeThreeD != "" {
		if executeThreeD == "true" {
			requestBody["executeThreeD"] = true
		}
	}

	// Shopper details
	if shopperEmail := resolver.ResolveString(getString(config, "shopperEmail")); shopperEmail != "" {
		requestBody["shopperEmail"] = shopperEmail
	}
	if shopperReference := resolver.ResolveString(getString(config, "shopperReference")); shopperReference != "" {
		requestBody["shopperReference"] = shopperReference
	}
	if billingAddress := getMap(config, "billingAddress"); billingAddress != nil {
		requestBody["billingAddress"] = billingAddress
	}

	// Return URL and channel (required for 3DS redirect)
	returnUrl := resolver.ResolveString(getString(config, "returnUrl"))
	if returnUrl == "" {
		returnUrl = "https://your-site.com/return"
	}
	requestBody["returnUrl"] = returnUrl

	channel := resolver.ResolveString(getString(config, "channel"))
	if channel == "" {
		channel = "Web"
	}
	requestBody["channel"] = channel

	// Make API call
	result, err := adyenRequest(ctx, adyenCfg, "payments", requestBody)
	if err != nil {
		return nil, fmt.Errorf("3DS authentication failed: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":           true,
		"pspReference":      result["pspReference"],
		"resultCode":        result["resultCode"],
		"merchantReference": reference,
		"response":          result,
	}

	// Add action if present (for 3DS redirect/challenge)
	if action, ok := result["action"]; ok {
		output["action"] = action
		output["requiresAction"] = true
	}

	// Add authentication result if present
	if authentication, ok := result["authentication"]; ok {
		output["authentication"] = authentication
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// BASE64 ENCODE HELPER (for API key authentication alternative)
// ============================================================================

// base64Encode encodes a string to base64
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
