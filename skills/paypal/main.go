package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"golang.org/x/oauth2"
)

const (
	// PayPal API endpoints
	PayPalAPIBaseURL     = "https://api.paypal.com"
	PayPalAPIBaseURLSandbox = "https://api.sandbox.paypal.com"
	PayPalAuthTokenURL   = "https://api.paypal.com/v1/oauth2/token"
	PayPalAuthTokenURLSandbox = "https://api.sandbox.paypal.com/v1/oauth2/token"
)

// PayPalClient represents a PayPal API client with OAuth2 support
type PayPalClient struct {
	httpClient  *http.Client
	clientID    string
	clientSecret string
	baseURL     string
	authURL     string
	token       *oauth2.Token
	tokenExpiry time.Time
	mu          sync.RWMutex
}

// NewPayPalClient creates a new PayPal client
func NewPayPalClient(clientID, clientSecret, environment string) *PayPalClient {
	isSandbox := strings.ToLower(environment) == "sandbox" || strings.ToLower(environment) == "test"
	
	baseURL := PayPalAPIBaseURL
	authURL := PayPalAuthTokenURL
	if isSandbox {
		baseURL = PayPalAPIBaseURLSandbox
		authURL = PayPalAuthTokenURLSandbox
	}

	return &PayPalClient{
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		clientID:     clientID,
		clientSecret: clientSecret,
		baseURL:      baseURL,
		authURL:      authURL,
	}
}

// getAccessToken retrieves and caches the OAuth2 access token
func (c *PayPalClient) getAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.token != nil && time.Now().Before(c.tokenExpiry.Add(-2*time.Minute)) {
		token := c.token.AccessToken
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.token != nil && time.Now().Before(c.tokenExpiry.Add(-2*time.Minute)) {
		return c.token.AccessToken, nil
	}

	// Request new token
	form := url.Values{}
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, "POST", c.authURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create auth request: %w", err)
	}

	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request auth token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("auth request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode auth response: %w", err)
	}

	c.token = &oauth2.Token{
		AccessToken: tokenResp.AccessToken,
		TokenType:   tokenResp.TokenType,
	}
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return tokenResp.AccessToken, nil
}

// doRequest performs an authenticated HTTP request to the PayPal API
func (c *PayPalClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp PayPalErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			return nil, fmt.Errorf("PayPal API error (%d): %s - %s", 
				resp.StatusCode, errResp.Name, errResp.Message)
		}
		return nil, fmt.Errorf("PayPal API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// PayPalErrorResponse represents a PayPal error response
type PayPalErrorResponse struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	DebugID string `json:"debug_id"`
	Details []struct {
		Field       string `json:"field"`
		Issue       string `json:"issue"`
		Description string `json:"description"`
	} `json:"details,omitempty"`
}

// ============================================================================
// PAYPAL ORDER CREATE
// ============================================================================

// OrderCreateConfig defines the configuration for creating a PayPal order
type OrderCreateConfig struct {
	Intent         string                 `json:"intent"`
	PurchaseUnits  []PurchaseUnitRequest  `json:"purchase_units"`
	PaymentSource  map[string]interface{} `json:"payment_source,omitempty"`
	ApplicationCtx map[string]interface{} `json:"application_context,omitempty"`
}

// PurchaseUnitRequest represents a purchase unit for order creation
type PurchaseUnitRequest struct {
	ReferenceID    string              `json:"reference_id,omitempty"`
	Amount         Amount              `json:"amount"`
	Description    string              `json:"description,omitempty"`
	CustomID       string              `json:"custom_id,omitempty"`
	InvoiceID      string              `json:"invoice_id,omitempty"`
	SoftDescriptor string              `json:"soft_descriptor,omitempty"`
	Items          []Item              `json:"items,omitempty"`
	Shipping       *ShippingDetails    `json:"shipping,omitempty"`
}

// Amount represents a monetary amount
type Amount struct {
	CurrencyCode string  `json:"currency_code"`
	Value        string  `json:"value"`
	Breakdown    *Breakdown `json:"breakdown,omitempty"`
}

// Breakdown represents amount breakdown
type Breakdown struct {
	ItemTotal      *Amount `json:"item_total,omitempty"`
	Shipping       *Amount `json:"shipping,omitempty"`
	Handling       *Amount `json:"handling,omitempty"`
	TaxTotal       *Amount `json:"tax_total,omitempty"`
	Insurance      *Amount `json:"insurance,omitempty"`
	ShippingDiscount *Amount `json:"shipping_discount,omitempty"`
	Discount       *Amount `json:"discount,omitempty"`
}

// Item represents an item in a purchase unit
type Item struct {
	Name        string `json:"name"`
	UnitAmount  Amount `json:"unit_amount"`
	Tax         *Amount `json:"tax,omitempty"`
	Quantity    string `json:"quantity"`
	Description string `json:"description,omitempty"`
	SKU         string `json:"sku,omitempty"`
	Category    string `json:"category,omitempty"`
}

// ShippingDetails represents shipping information
type ShippingDetails struct {
	Name    *ShippingName `json:"name,omitempty"`
	Address *Address      `json:"address,omitempty"`
}

// ShippingName represents the shipping name
type ShippingName struct {
	FullName string `json:"full_name"`
}

// Address represents a physical address
type Address struct {
	AddressLine1 string `json:"address_line_1,omitempty"`
	AddressLine2 string `json:"address_line_2,omitempty"`
	AdminArea2   string `json:"admin_area_2,omitempty"`
	AdminArea1   string `json:"admin_area_1,omitempty"`
	PostalCode   string `json:"postal_code,omitempty"`
	CountryCode  string `json:"country_code"`
}

// OrderCreateExecutor handles paypal-order-create
type OrderCreateExecutor struct{}

func (e *OrderCreateExecutor) Type() string {
	return "paypal-order-create"
}

func (e *OrderCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get credentials
	clientID := resolver.ResolveString(getString(config, "clientID"))
	clientSecret := resolver.ResolveString(getString(config, "clientSecret"))
	environment := resolver.ResolveString(getString(config, "environment"))

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("PayPal client ID and secret are required")
	}

	client := NewPayPalClient(clientID, clientSecret, environment)

	// Build purchase units
	purchaseUnitsRaw := getInterfaceSlice(config, "purchaseUnits")
	if len(purchaseUnitsRaw) == 0 {
		return nil, fmt.Errorf("at least one purchase unit is required")
	}

	purchaseUnits, err := parsePurchaseUnits(purchaseUnitsRaw, resolver)
	if err != nil {
		return nil, fmt.Errorf("failed to parse purchase units: %w", err)
	}

	// Build order request
	intent := resolver.ResolveString(getString(config, "intent"))
	if intent == "" {
		intent = "CAPTURE"
	}

	orderReq := map[string]interface{}{
		"intent":         intent,
		"purchase_units": purchaseUnits,
	}

	// Add payment source if provided
	if paymentSource, ok := config["paymentSource"]; ok {
		orderReq["payment_source"] = paymentSource
	}

	// Add application context if provided
	if appCtx, ok := config["applicationContext"]; ok {
		orderReq["application_context"] = appCtx
	}

	// Make API call
	respBody, err := client.doRequest(ctx, "POST", "/v2/checkout/orders", orderReq)
	if err != nil {
		return nil, err
	}

	// Parse response
	var order OrderResponse
	if err := json.Unmarshal(respBody, &order); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"id":              order.ID,
		"status":          order.Status,
		"intent":          order.Intent,
		"createTime":      order.CreateTime,
		"updateTime":      order.UpdateTime,
		"links":           order.Links,
		"purchaseUnits":   order.PurchaseUnits,
		"approvalURL":     getApprovalURL(order.Links),
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// OrderResponse represents a PayPal order response
type OrderResponse struct {
	ID            string           `json:"id"`
	Status        string           `json:"status"`
	Intent        string           `json:"intent"`
	PurchaseUnits []PurchaseUnit   `json:"purchase_units"`
	CreateTime    string           `json:"create_time"`
	UpdateTime    string           `json:"update_time"`
	Links         []Link           `json:"links"`
	PaymentSource map[string]interface{} `json:"payment_source,omitempty"`
}

// PurchaseUnit represents a purchase unit in the response
type PurchaseUnit struct {
	ReferenceID string  `json:"reference_id"`
	Amount      Amount  `json:"amount"`
	Payments    *Payments `json:"payments,omitempty"`
	Shipping    *ShippingDetails `json:"shipping,omitempty"`
}

// Payments represents payment details
type Payments struct {
	Authorizations []Authorization `json:"authorizations,omitempty"`
	Captures       []Capture       `json:"captures,omitempty"`
	Refunds        []Refund        `json:"refunds,omitempty"`
}

// Authorization represents an authorization
type Authorization struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount Amount `json:"amount"`
}

// Capture represents a capture
type Capture struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount Amount `json:"amount"`
}

// Refund represents a refund
type Refund struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount Amount `json:"amount"`
}

// Link represents a HATEOAS link
type Link struct {
	Href   string `json:"href"`
	Rel    string `json:"rel"`
	Method string `json:"method"`
}

func getApprovalURL(links []Link) string {
	for _, link := range links {
		if link.Rel == "approve" {
			return link.Href
		}
	}
	return ""
}

// ============================================================================
// PAYPAL ORDER CAPTURE
// ============================================================================

// OrderCaptureExecutor handles paypal-order-capture
type OrderCaptureExecutor struct{}

func (e *OrderCaptureExecutor) Type() string {
	return "paypal-order-capture"
}

func (e *OrderCaptureExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get credentials
	clientID := resolver.ResolveString(getString(config, "clientID"))
	clientSecret := resolver.ResolveString(getString(config, "clientSecret"))
	environment := resolver.ResolveString(getString(config, "environment"))
	orderID := resolver.ResolveString(getString(config, "orderID"))

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("PayPal client ID and secret are required")
	}
	if orderID == "" {
		return nil, fmt.Errorf("order ID is required")
	}

	client := NewPayPalClient(clientID, clientSecret, environment)

	// Build capture request
	captureReq := map[string]interface{}{}

	if transactionFee, ok := config["transactionFee"]; ok {
		captureReq["transaction_fee"] = transactionFee
	}
	if invoiceID := resolver.ResolveString(getString(config, "invoiceID")); invoiceID != "" {
		captureReq["invoice_id"] = invoiceID
	}
	if note := resolver.ResolveString(getString(config, "note")); note != "" {
		captureReq["note_to_payer"] = note
	}

	// Make API call
	path := fmt.Sprintf("/v2/checkout/orders/%s/capture", orderID)
	respBody, err := client.doRequest(ctx, "POST", path, captureReq)
	if err != nil {
		return nil, err
	}

	// Parse response
	var capture CaptureResponse
	if err := json.Unmarshal(respBody, &capture); err != nil {
		return nil, fmt.Errorf("failed to parse capture response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"id":              capture.ID,
		"status":          capture.Status,
		"amount":          capture.Amount,
		"finalCapture":    capture.FinalCapture,
		"createTime":      capture.CreateTime,
		"updateTime":      capture.UpdateTime,
		"links":           capture.Links,
		"sellerProtection": capture.SellerProtection,
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// CaptureResponse represents a capture response
type CaptureResponse struct {
	ID               string           `json:"id"`
	Status           string           `json:"status"`
	Amount           Amount           `json:"amount"`
	FinalCapture     bool             `json:"final_capture"`
	CreateTime       string           `json:"create_time"`
	UpdateTime       string           `json:"update_time"`
	Links            []Link           `json:"links"`
	SellerProtection *SellerProtection `json:"seller_protection,omitempty"`
	DisbursementMode string           `json:"disbursement_mode,omitempty"`
}

// SellerProtection represents seller protection status
type SellerProtection struct {
	Status             string   `json:"status"`
	DisputeCategories  []string `json:"dispute_categories,omitempty"`
}

// ============================================================================
// PAYPAL ORDER GET
// ============================================================================

// OrderGetExecutor handles paypal-order-get
type OrderGetExecutor struct{}

func (e *OrderGetExecutor) Type() string {
	return "paypal-order-get"
}

func (e *OrderGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get credentials
	clientID := resolver.ResolveString(getString(config, "clientID"))
	clientSecret := resolver.ResolveString(getString(config, "clientSecret"))
	environment := resolver.ResolveString(getString(config, "environment"))
	orderID := resolver.ResolveString(getString(config, "orderID"))

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("PayPal client ID and secret are required")
	}
	if orderID == "" {
		return nil, fmt.Errorf("order ID is required")
	}

	client := NewPayPalClient(clientID, clientSecret, environment)

	// Make API call
	path := fmt.Sprintf("/v2/checkout/orders/%s", orderID)
	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var order OrderResponse
	if err := json.Unmarshal(respBody, &order); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"id":              order.ID,
		"status":          order.Status,
		"intent":          order.Intent,
		"createTime":      order.CreateTime,
		"updateTime":      order.UpdateTime,
		"purchaseUnits":   order.PurchaseUnits,
		"paymentSource":   order.PaymentSource,
		"links":           order.Links,
		"approvalURL":     getApprovalURL(order.Links),
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ============================================================================
// PAYPAL PAYMENT LIST
// ============================================================================

// PaymentListExecutor handles paypal-payment-list
type PaymentListExecutor struct{}

func (e *PaymentListExecutor) Type() string {
	return "paypal-payment-list"
}

func (e *PaymentListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get credentials
	clientID := resolver.ResolveString(getString(config, "clientID"))
	clientSecret := resolver.ResolveString(getString(config, "clientSecret"))
	environment := resolver.ResolveString(getString(config, "environment"))

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("PayPal client ID and secret are required")
	}

	client := NewPayPalClient(clientID, clientSecret, environment)

	// Build query parameters
	queryParams := url.Values{}
	
	if startTime := resolver.ResolveString(getString(config, "startTime")); startTime != "" {
		queryParams.Set("start_time", startTime)
	}
	if endTime := resolver.ResolveString(getString(config, "endTime")); endTime != "" {
		queryParams.Set("end_time", endTime)
	}
	if transactionType := resolver.ResolveString(getString(config, "transactionType")); transactionType != "" {
		queryParams.Set("transaction_type", transactionType)
	}
	if transactionStatus := resolver.ResolveString(getString(config, "transactionStatus")); transactionStatus != "" {
		queryParams.Set("transaction_status", transactionStatus)
	}
	if page := resolver.ResolveString(getString(config, "page")); page != "" {
		queryParams.Set("page", page)
	}
	if pageSize := resolver.ResolveString(getString(config, "pageSize")); pageSize != "" {
		queryParams.Set("page_size", pageSize)
	}

	// Make API call
	path := "/v1/reporting/transactions"
	if len(queryParams) > 0 {
		path += "?" + queryParams.Encode()
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var payments PaymentsListResponse
	if err := json.Unmarshal(respBody, &payments); err != nil {
		return nil, fmt.Errorf("failed to parse payments response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"count":        payments.Count,
		"transactions": payments.TransactionDetails,
		"links":        payments.Links,
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// PaymentsListResponse represents a list of payments
type PaymentsListResponse struct {
	Count            int                 `json:"count"`
	TransactionDetails []TransactionDetail `json:"transaction_details"`
	Links            []Link              `json:"links,omitempty"`
}

// TransactionDetail represents a transaction detail
type TransactionDetail struct {
	TransactionInfo     *TransactionInfo     `json:"transaction_info"`
	PayerInfo           *PayerInfo           `json:"payer_info,omitempty"`
	ShippingInfo        *ShippingInfo        `json:"shipping_info,omitempty"`
	CartInfo            *CartInfo            `json:"cart_info,omitempty"`
	InvoiceInfo         *InvoiceInfo         `json:"invoice_info,omitempty"`
	CustomInfo          *CustomInfo          `json:"custom_info,omitempty"`
	AuctionInfo         *AuctionInfo         `json:"auction_info,omitempty"`
	IncentiveInfo       []IncentiveInfo      `json:"incentive_info,omitempty"`
	StoreInfo           *StoreInfo           `json:"store_info,omitempty"`
	ProtectionEligibility *ProtectionEligibility `json:"protection_eligibility,omitempty"`
}

// TransactionInfo represents transaction information
type TransactionInfo struct {
	PayPalAccountID         string   `json:"paypal_account_id"`
	TransactionID           string   `json:"transaction_id"`
	TransactionType         string   `json:"transaction_type"`
	TransactionSubject      string   `json:"transaction_subject"`
	TransactionAmount       *Amount  `json:"transaction_amount"`
	FeeAmount               *Amount  `json:"fee_amount,omitempty"`
	InsuranceAmount         *Amount  `json:"insurance_amount,omitempty"`
	CustomField             string   `json:"custom_field,omitempty"`
	InvoiceID               string   `json:"invoice_id,omitempty"`
	CustomInvoiceLabel      string   `json:"custom_invoice_label,omitempty"`
	UpdateTime              string   `json:"update_time"`
	TransactionStatus       string   `json:"transaction_status"`
	TransactionStatusReason string   `json:"transaction_status_reason,omitempty"`
}

// PayerInfo represents payer information
type PayerInfo struct {
	EmailID    string `json:"email_id"`
	AccountID  string `json:"account_id"`
	AddressStatus string `json:"address_status"`
	PayerStatus   string `json:"payer_status"`
	PayerName     *PayerName `json:"payer_name"`
	CountryCode   string `json:"country_code"`
	AddressID     string `json:"address_id"`
	ShippingAddress *Address `json:"shipping_address"`
}

// PayerName represents the payer's name
type PayerName struct {
	GivenName string `json:"given_name"`
	Surname   string `json:"surname"`
}

// ShippingInfo represents shipping information
type ShippingInfo struct {
	Name    *PayerName `json:"name"`
	Address *Address   `json:"address"`
}

// CartInfo represents cart information
type CartInfo struct {
	ItemDetails []ItemDetail `json:"item_details"`
}

// ItemDetail represents an item detail
type ItemDetail struct {
	ItemName    string  `json:"item_name"`
	ItemQuantity string `json:"item_quantity"`
	ItemPrice   *Amount `json:"item_price"`
}

// InvoiceInfo represents invoice information
type InvoiceInfo struct {
	InvoiceNumber string `json:"invoice_number"`
}

// CustomInfo represents custom information
type CustomInfo struct {
	CustomNote string `json:"custom_note"`
}

// AuctionInfo represents auction information
type AuctionInfo struct {
	CustomerAuctionID string `json:"customer_auction_id"`
}

// IncentiveInfo represents incentive information
type IncentiveInfo struct {
	IncentiveID   string `json:"incentive_id"`
	IncentiveType string `json:"incentive_type"`
}

// StoreInfo represents store information
type StoreInfo struct {
	StoreID string `json:"store_id"`
}

// ProtectionEligibility represents protection eligibility
type ProtectionEligibility struct {
	Eligible             bool     `json:"eligible"`
	EligibilityType      []string `json:"eligibility_type"`
}

// ============================================================================
// PAYPAL REFUND
// ============================================================================

// RefundExecutor handles paypal-refund
type RefundExecutor struct{}

func (e *RefundExecutor) Type() string {
	return "paypal-refund"
}

func (e *RefundExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get credentials
	clientID := resolver.ResolveString(getString(config, "clientID"))
	clientSecret := resolver.ResolveString(getString(config, "clientSecret"))
	environment := resolver.ResolveString(getString(config, "environment"))
	captureID := resolver.ResolveString(getString(config, "captureID"))

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("PayPal client ID and secret are required")
	}
	if captureID == "" {
		return nil, fmt.Errorf("capture ID is required")
	}

	client := NewPayPalClient(clientID, clientSecret, environment)

	// Build refund request
	refundReq := map[string]interface{}{}

	// Add amount if provided
	if amountRaw, ok := config["amount"]; ok {
		amountMap, ok := amountRaw.(map[string]interface{})
		if ok {
			amount := Amount{
				CurrencyCode: resolver.ResolveString(getString(amountMap, "currency_code")),
				Value:        resolver.ResolveString(getString(amountMap, "value")),
			}
			if amount.CurrencyCode != "" && amount.Value != "" {
				refundReq["amount"] = amount
			}
		}
	}

	// Add reason if provided
	if reason := resolver.ResolveString(getString(config, "reason")); reason != "" {
		refundReq["reason"] = reason
	}

	// Add invoice ID if provided
	if invoiceID := resolver.ResolveString(getString(config, "invoiceID")); invoiceID != "" {
		refundReq["invoice_id"] = invoiceID
	}

	// Add note to payer if provided
	if noteToPayer := resolver.ResolveString(getString(config, "noteToPayer")); noteToPayer != "" {
		refundReq["note_to_payer"] = noteToPayer
	}

	// Make API call
	path := fmt.Sprintf("/v2/payments/captures/%s/refund", captureID)
	respBody, err := client.doRequest(ctx, "POST", path, refundReq)
	if err != nil {
		return nil, err
	}

	// Parse response
	var refund RefundResponse
	if err := json.Unmarshal(respBody, &refund); err != nil {
		return nil, fmt.Errorf("failed to parse refund response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"id":              refund.ID,
		"status":          refund.Status,
		"amount":          refund.Amount,
		"payerRefundable": refund.PayerRefundable,
		"sellerPayable":   refund.SellerPayable,
		"createTime":      refund.CreateTime,
		"updateTime":      refund.UpdateTime,
		"links":           refund.Links,
		"invoiceID":       refund.InvoiceID,
		"reason":          refund.Reason,
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// RefundResponse represents a refund response
type RefundResponse struct {
	ID              string           `json:"id"`
	Status          string           `json:"status"`
	Amount          Amount           `json:"amount"`
	PayerRefundable string           `json:"payer_refundable,omitempty"`
	SellerPayable   string           `json:"seller_payable,omitempty"`
	CreateTime      string           `json:"create_time"`
	UpdateTime      string           `json:"update_time"`
	Links           []Link           `json:"links"`
	InvoiceID       string           `json:"invoice_id,omitempty"`
	Reason          string           `json:"reason,omitempty"`
}

// ============================================================================
// PAYPAL PAYOUT
// ============================================================================

// PayoutExecutor handles paypal-payout
type PayoutExecutor struct{}

func (e *PayoutExecutor) Type() string {
	return "paypal-payout"
}

func (e *PayoutExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get credentials
	clientID := resolver.ResolveString(getString(config, "clientID"))
	clientSecret := resolver.ResolveString(getString(config, "clientSecret"))
	environment := resolver.ResolveString(getString(config, "environment"))

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("PayPal client ID and secret are required")
	}

	client := NewPayPalClient(clientID, clientSecret, environment)

	// Build payout request
	payoutReq := map[string]interface{}{}

	// Add sender batch ID if provided
	if senderBatchID := resolver.ResolveString(getString(config, "senderBatchID")); senderBatchID != "" {
		payoutReq["sender_batch_id"] = senderBatchID
	} else {
		// Generate a unique sender batch ID
		payoutReq["sender_batch_id"] = fmt.Sprintf("batch_%d", time.Now().UnixNano())
	}

	// Add email subject if provided
	if emailSubject := resolver.ResolveString(getString(config, "emailSubject")); emailSubject != "" {
		payoutReq["email_subject"] = emailSubject
	}

	// Add email message if provided
	if emailMessage := resolver.ResolveString(getString(config, "emailMessage")); emailMessage != "" {
		payoutReq["email_message"] = emailMessage
	}

	// Add recipient type if provided
	if recipientType := resolver.ResolveString(getString(config, "recipientType")); recipientType != "" {
		payoutReq["recipient_type"] = recipientType
	} else {
		payoutReq["recipient_type"] = "EMAIL"
	}

	// Build payout items
	itemsRaw := getInterfaceSlice(config, "items")
	if len(itemsRaw) == 0 {
		return nil, fmt.Errorf("at least one payout item is required")
	}

	items, err := parsePayoutItems(itemsRaw, resolver)
	if err != nil {
		return nil, fmt.Errorf("failed to parse payout items: %w", err)
	}
	payoutReq["items"] = items

	// Make API call - use batch mode by default
	syncMode := getBool(config, "syncMode", false)
	path := "/v1/payments/payouts"
	if syncMode {
		path += "?sync_mode=1"
	}

	respBody, err := client.doRequest(ctx, "POST", path, payoutReq)
	if err != nil {
		return nil, err
	}

	// Parse response
	var payout PayoutResponse
	if err := json.Unmarshal(respBody, &payout); err != nil {
		return nil, fmt.Errorf("failed to parse payout response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"batchHeader":    payout.BatchHeader,
		"items":          payout.Items,
		"links":          payout.Links,
		"payoutBatchID":  payout.BatchHeader.PayoutBatchID,
		"batchStatus":    payout.BatchHeader.BatchStatus,
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// PayoutResponse represents a payout response
type PayoutResponse struct {
	BatchHeader *PayoutBatchHeader `json:"batch_header"`
	Items       []PayoutItemDetail `json:"items"`
	Links       []Link             `json:"links,omitempty"`
}

// PayoutBatchHeader represents the batch header
type PayoutBatchHeader struct {
	PayoutBatchID     string           `json:"payout_batch_id"`
	BatchStatus       string           `json:"batch_status"`
	TimeCreated       string           `json:"time_created,omitempty"`
	TimeCompleted     string           `json:"time_completed,omitempty"`
	SenderBatchID     string           `json:"sender_batch_id"`
	SenderBatchStatus string           `json:"sender_batch_status,omitempty"`
	Amount            *Amount          `json:"amount,omitempty"`
	Fees              *Amount          `json:"fees,omitempty"`
	EmailSubject      string           `json:"email_subject,omitempty"`
	EmailMessage      string           `json:"email_message,omitempty"`
}

// PayoutItemDetail represents a payout item detail
type PayoutItemDetail struct {
	PayoutItemID         string           `json:"payout_item_id"`
	TransactionID        string           `json:"transaction_id"`
	TransactionStatus    string           `json:"transaction_status"`
	PayoutItemFee        *Amount          `json:"payout_item_fee,omitempty"`
	PayoutBatchID        string           `json:"payout_batch_id"`
	SenderBatchID        string           `json:"sender_batch_id"`
	PayoutItem           *PayoutItem      `json:"payout_item"`
	TimeProcessed        string           `json:"time_processed,omitempty"`
	Links                []Link           `json:"links,omitempty"`
	Errors               *PayoutItemError `json:"errors,omitempty"`
}

// PayoutItem represents a payout item request
type PayoutItem struct {
	RecipientType   string  `json:"recipient_type"`
	Receiver        string  `json:"receiver"`
	Amount          Amount  `json:"amount"`
	Note            string  `json:"note,omitempty"`
	SenderItemID    string  `json:"sender_item_id,omitempty"`
	RecipientWallet string  `json:"recipient_wallet,omitempty"`
}

// PayoutItemError represents a payout item error
type PayoutItemError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// ============================================================================
// PAYPAL TRANSACTION LIST
// ============================================================================

// TransactionListExecutor handles paypal-transaction-list
type TransactionListExecutor struct{}

func (e *TransactionListExecutor) Type() string {
	return "paypal-transaction-list"
}

func (e *TransactionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get credentials
	clientID := resolver.ResolveString(getString(config, "clientID"))
	clientSecret := resolver.ResolveString(getString(config, "clientSecret"))
	environment := resolver.ResolveString(getString(config, "environment"))
	accountID := resolver.ResolveString(getString(config, "accountID"))

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("PayPal client ID and secret are required")
	}

	client := NewPayPalClient(clientID, clientSecret, environment)

	// Build query parameters
	queryParams := url.Values{}
	
	if startTime := resolver.ResolveString(getString(config, "startTime")); startTime != "" {
		queryParams.Set("start_date", startTime)
	}
	if endTime := resolver.ResolveString(getString(config, "endTime")); endTime != "" {
		queryParams.Set("end_date", endTime)
	}
	if page := resolver.ResolveString(getString(config, "page")); page != "" {
		queryParams.Set("page", page)
	}
	if pageSize := resolver.ResolveString(getString(config, "pageSize")); pageSize != "" {
		queryParams.Set("page_size", pageSize)
	}

	// Make API call
	path := fmt.Sprintf("/v1/reporting/transactions")
	if accountID != "" {
		queryParams.Set("paypal_account_id", accountID)
	}
	if len(queryParams) > 0 {
		path += "?" + queryParams.Encode()
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var transactions TransactionsResponse
	if err := json.Unmarshal(respBody, &transactions); err != nil {
		return nil, fmt.Errorf("failed to parse transactions response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"count":        transactions.Count,
		"transactions": transactions.TransactionDetails,
		"links":        transactions.Links,
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// TransactionsResponse represents a transactions response
type TransactionsResponse struct {
	Count            int                 `json:"count"`
	TransactionDetails []TransactionDetail `json:"transaction_details"`
	Links            []Link              `json:"links,omitempty"`
}

// ============================================================================
// PAYPAL INVOICE CREATE
// ============================================================================

// InvoiceCreateExecutor handles paypal-invoice-create
type InvoiceCreateExecutor struct{}

func (e *InvoiceCreateExecutor) Type() string {
	return "paypal-invoice-create"
}

func (e *InvoiceCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get credentials
	clientID := resolver.ResolveString(getString(config, "clientID"))
	clientSecret := resolver.ResolveString(getString(config, "clientSecret"))
	environment := resolver.ResolveString(getString(config, "environment"))

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("PayPal client ID and secret are required")
	}

	client := NewPayPalClient(clientID, clientSecret, environment)

	// Build invoice request
	invoiceReq := map[string]interface{}{}

	// Add billing info if provided
	if billingRaw, ok := config["billing"]; ok {
		billing, err := parseBillingInfo(billingRaw, resolver)
		if err != nil {
			return nil, fmt.Errorf("failed to parse billing info: %w", err)
		}
		invoiceReq["billing"] = billing
	}

	// Add invoice date if provided
	if invoiceDate := resolver.ResolveString(getString(config, "invoiceDate")); invoiceDate != "" {
		invoiceReq["invoice_date"] = invoiceDate
	}

	// Add payment term if provided
	if paymentTermRaw, ok := config["paymentTerm"]; ok {
		paymentTerm, err := parsePaymentTerm(paymentTermRaw, resolver)
		if err != nil {
			return nil, fmt.Errorf("failed to parse payment term: %w", err)
		}
		invoiceReq["payment_term"] = paymentTerm
	}

	// Add reference ID if provided
	if referenceID := resolver.ResolveString(getString(config, "referenceID")); referenceID != "" {
		invoiceReq["reference"] = referenceID
	}

	// Add discount if provided
	if discountRaw, ok := config["discount"]; ok {
		discount, err := parseDiscount(discountRaw, resolver)
		if err != nil {
			return nil, fmt.Errorf("failed to parse discount: %w", err)
		}
		invoiceReq["discount"] = discount
	}

	// Add shipping info if provided
	if shippingRaw, ok := config["shippingInfo"]; ok {
		shipping, err := parseShippingInfo(shippingRaw, resolver)
		if err != nil {
			return nil, fmt.Errorf("failed to parse shipping info: %w", err)
		}
		invoiceReq["shipping"] = shipping
	}

	// Add items
	itemsRaw := getInterfaceSlice(config, "items")
	if len(itemsRaw) == 0 {
		return nil, fmt.Errorf("at least one invoice item is required")
	}

	items, err := parseInvoiceItems(itemsRaw, resolver)
	if err != nil {
		return nil, fmt.Errorf("failed to parse invoice items: %w", err)
	}
	invoiceReq["items"] = items

	// Add amount if provided (usually calculated from items)
	if amountRaw, ok := config["amount"]; ok {
		invoiceReq["amount"] = amountRaw
	}

	// Add memo if provided
	if memo := resolver.ResolveString(getString(config, "memo")); memo != "" {
		invoiceReq["memo"] = memo
	}

	// Add note if provided
	if note := resolver.ResolveString(getString(config, "note")); note != "" {
		invoiceReq["note"] = note
	}

	// Add terms and conditions if provided
	if termsAndConditions := resolver.ResolveString(getString(config, "termsAndConditions")); termsAndConditions != "" {
		invoiceReq["terms_and_conditions"] = termsAndConditions
	}

	// Add merchant info if provided
	if merchantRaw, ok := config["merchantInfo"]; ok {
		merchant, err := parseMerchantInfo(merchantRaw, resolver)
		if err != nil {
			return nil, fmt.Errorf("failed to parse merchant info: %w", err)
		}
		invoiceReq["merchant"] = merchant
	}

	// Make API call
	respBody, err := client.doRequest(ctx, "POST", "/v2/invoicing/invoices", invoiceReq)
	if err != nil {
		return nil, err
	}

	// Parse response
	var invoice InvoiceResponse
	if err := json.Unmarshal(respBody, &invoice); err != nil {
		return nil, fmt.Errorf("failed to parse invoice response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"id":              invoice.ID,
		"number":          invoice.Number,
		"status":          invoice.Status,
		"invoiceDate":     invoice.InvoiceDate,
		"dueDate":         invoice.DueDate,
		"amount":          invoice.Amount,
		"billing":         invoice.Billing,
		"items":           invoice.Items,
		"links":           invoice.Links,
		"invoiceURL":      getInvoiceURL(invoice.Links),
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// InvoiceResponse represents an invoice response
type InvoiceResponse struct {
	ID                 string           `json:"id"`
	Number             string           `json:"number"`
	Status             string           `json:"status"`
	InvoiceDate        string           `json:"invoice_date"`
	DueDate            string           `json:"due_date"`
	Amount             *Amount          `json:"amount"`
	Billing            []BillingInfo    `json:"billing"`
	Items              []InvoiceItem    `json:"items"`
	Links              []Link           `json:"links"`
	Memo               string           `json:"memo,omitempty"`
	Note               string           `json:"note,omitempty"`
	TermsAndConditions string           `json:"terms_and_conditions,omitempty"`
	Merchant           *MerchantInfo    `json:"merchant,omitempty"`
	Shipping           *ShippingInfo    `json:"shipping,omitempty"`
	Discount           *Discount        `json:"discount,omitempty"`
	PaymentTerm        *PaymentTerm     `json:"payment_term,omitempty"`
	Reference          string           `json:"reference,omitempty"`
	Metadata           *InvoiceMetadata `json:"metadata,omitempty"`
}

// InvoiceMetadata represents invoice metadata
type InvoiceMetadata struct {
	CreatedDate string `json:"created_date"`
	CreatedBy   string `json:"created_by"`
}

// BillingInfo represents billing information
type BillingInfo struct {
	Email   string      `json:"email,omitempty"`
	Name    *PayerName  `json:"name,omitempty"`
	Address *Address    `json:"address,omitempty"`
	Phone   *PhoneInfo  `json:"phone,omitempty"`
}

// PhoneInfo represents phone information
type PhoneInfo struct {
	CountryCode string `json:"country_code"`
	NationalNumber string `json:"national_number"`
}

// PaymentTerm represents payment terms
type PaymentTerm struct {
	TermType   string `json:"term_type,omitempty"`
	DueDate    string `json:"due_date,omitempty"`
	DueOffset  *DueOffset `json:"due_offset,omitempty"`
}

// DueOffset represents due offset
type DueOffset struct {
	Unit  string `json:"unit"`
	Value int    `json:"value"`
}

// Discount represents a discount
type Discount struct {
	Percent  string  `json:"percent,omitempty"`
	Amount   *Amount `json:"amount,omitempty"`
}

// MerchantInfo represents merchant information
type MerchantInfo struct {
	Email        string      `json:"email"`
	BusinessName string      `json:"business_name,omitempty"`
	Phone        *PhoneInfo  `json:"phone,omitempty"`
	Address      *Address    `json:"address,omitempty"`
	Website      string      `json:"website,omitempty"`
	TaxID        string      `json:"tax_id,omitempty"`
}

// InvoiceItem represents an invoice item
type InvoiceItem struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Quantity    string  `json:"quantity"`
	UnitAmount  Amount  `json:"unit_amount"`
	Tax         *Tax    `json:"tax,omitempty"`
	Discount    *Discount `json:"discount,omitempty"`
	UnitOfMeasure string `json:"unit_of_measure,omitempty"`
}

// Tax represents tax information
type Tax struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Percent string `json:"percent,omitempty"`
	Amount *Amount `json:"amount,omitempty"`
}

// ============================================================================
// HELPER FUNCTIONS
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

// Helper to get interface slice from config
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// parsePurchaseUnits parses purchase units from config
func parsePurchaseUnits(itemsRaw []interface{}, resolver executor.TemplateResolver) ([]map[string]interface{}, error) {
	purchaseUnits := make([]map[string]interface{}, 0, len(itemsRaw))

	for _, itemRaw := range itemsRaw {
		itemMap, ok := itemRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid purchase unit format")
		}

		pu := make(map[string]interface{})

		if refID := resolver.ResolveString(getString(itemMap, "reference_id")); refID != "" {
			pu["reference_id"] = refID
		}
		if desc := resolver.ResolveString(getString(itemMap, "description")); desc != "" {
			pu["description"] = desc
		}
		if customID := resolver.ResolveString(getString(itemMap, "custom_id")); customID != "" {
			pu["custom_id"] = customID
		}
		if invoiceID := resolver.ResolveString(getString(itemMap, "invoice_id")); invoiceID != "" {
			pu["invoice_id"] = invoiceID
		}
		if softDesc := resolver.ResolveString(getString(itemMap, "soft_descriptor")); softDesc != "" {
			pu["soft_descriptor"] = softDesc
		}

		// Parse amount
		if amountRaw, ok := itemMap["amount"]; ok {
			amountMap, ok := amountRaw.(map[string]interface{})
			if ok {
				amount := map[string]interface{}{
					"currency_code": resolver.ResolveString(getString(amountMap, "currency_code")),
					"value":         resolver.ResolveString(getString(amountMap, "value")),
				}
				pu["amount"] = amount
			}
		}

		// Parse items
		if itemsRaw, ok := itemMap["items"]; ok {
			items, err := parseItems(itemsRaw, resolver)
			if err != nil {
				return nil, fmt.Errorf("failed to parse items: %w", err)
			}
			pu["items"] = items
		}

		// Parse shipping
		if shippingRaw, ok := itemMap["shipping"]; ok {
			shipping, err := parseShipping(shippingRaw, resolver)
			if err != nil {
				return nil, fmt.Errorf("failed to parse shipping: %w", err)
			}
			pu["shipping"] = shipping
		}

		purchaseUnits = append(purchaseUnits, pu)
	}

	return purchaseUnits, nil
}

// parseItems parses items from config
func parseItems(itemsRaw interface{}, resolver executor.TemplateResolver) ([]map[string]interface{}, error) {
	arr, ok := itemsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("items must be an array")
	}

	items := make([]map[string]interface{}, 0, len(arr))
	for _, itemRaw := range arr {
		itemMap, ok := itemRaw.(map[string]interface{})
		if !ok {
			continue
		}

		item := make(map[string]interface{})
		if name := resolver.ResolveString(getString(itemMap, "name")); name != "" {
			item["name"] = name
		}
		if desc := resolver.ResolveString(getString(itemMap, "description")); desc != "" {
			item["description"] = desc
		}
		if sku := resolver.ResolveString(getString(itemMap, "sku")); sku != "" {
			item["sku"] = sku
		}
		if category := resolver.ResolveString(getString(itemMap, "category")); category != "" {
			item["category"] = category
		}
		if qty := resolver.ResolveString(getString(itemMap, "quantity")); qty != "" {
			item["quantity"] = qty
		} else {
			item["quantity"] = "1"
		}

		// Parse unit amount
		if unitAmountRaw, ok := itemMap["unit_amount"]; ok {
			amountMap, ok := unitAmountRaw.(map[string]interface{})
			if ok {
				item["unit_amount"] = map[string]interface{}{
					"currency_code": resolver.ResolveString(getString(amountMap, "currency_code")),
					"value":         resolver.ResolveString(getString(amountMap, "value")),
				}
			}
		}

		// Parse tax
		if taxRaw, ok := itemMap["tax"]; ok {
			taxMap, ok := taxRaw.(map[string]interface{})
			if ok {
				tax := map[string]interface{}{
					"currency_code": resolver.ResolveString(getString(taxMap, "currency_code")),
					"value":         resolver.ResolveString(getString(taxMap, "value")),
				}
				item["tax"] = tax
			}
		}

		items = append(items, item)
	}

	return items, nil
}

// parseShipping parses shipping details from config
func parseShipping(shippingRaw interface{}, resolver executor.TemplateResolver) (map[string]interface{}, error) {
	shippingMap, ok := shippingRaw.(map[string]interface{})
	if !ok {
		return nil, nil
	}

	shipping := make(map[string]interface{})

	// Parse name
	if nameRaw, ok := shippingMap["name"]; ok {
		nameMap, ok := nameRaw.(map[string]interface{})
		if ok {
			shipping["name"] = map[string]interface{}{
				"full_name": resolver.ResolveString(getString(nameMap, "full_name")),
			}
		}
	}

	// Parse address
	if addrRaw, ok := shippingMap["address"]; ok {
		addrMap, ok := addrRaw.(map[string]interface{})
		if ok {
			addr := map[string]interface{}{
				"country_code": resolver.ResolveString(getString(addrMap, "country_code")),
			}
			if line1 := resolver.ResolveString(getString(addrMap, "address_line_1")); line1 != "" {
				addr["address_line_1"] = line1
			}
			if line2 := resolver.ResolveString(getString(addrMap, "address_line_2")); line2 != "" {
				addr["address_line_2"] = line2
			}
			if admin1 := resolver.ResolveString(getString(addrMap, "admin_area_1")); admin1 != "" {
				addr["admin_area_1"] = admin1
			}
			if admin2 := resolver.ResolveString(getString(addrMap, "admin_area_2")); admin2 != "" {
				addr["admin_area_2"] = admin2
			}
			if postal := resolver.ResolveString(getString(addrMap, "postal_code")); postal != "" {
				addr["postal_code"] = postal
			}
			shipping["address"] = addr
		}
	}

	return shipping, nil
}

// parsePayoutItems parses payout items from config
func parsePayoutItems(itemsRaw []interface{}, resolver executor.TemplateResolver) ([]map[string]interface{}, error) {
	items := make([]map[string]interface{}, 0, len(itemsRaw))

	for _, itemRaw := range itemsRaw {
		itemMap, ok := itemRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid payout item format")
		}

		item := make(map[string]interface{})

		if recipientType := resolver.ResolveString(getString(itemMap, "recipient_type")); recipientType != "" {
			item["recipient_type"] = recipientType
		} else {
			item["recipient_type"] = "EMAIL"
		}

		if receiver := resolver.ResolveString(getString(itemMap, "receiver")); receiver != "" {
			item["receiver"] = receiver
		} else {
			return nil, fmt.Errorf("payout item receiver is required")
		}

		if senderItemID := resolver.ResolveString(getString(itemMap, "sender_item_id")); senderItemID != "" {
			item["sender_item_id"] = senderItemID
		}

		if note := resolver.ResolveString(getString(itemMap, "note")); note != "" {
			item["note"] = note
		}

		if wallet := resolver.ResolveString(getString(itemMap, "recipient_wallet")); wallet != "" {
			item["recipient_wallet"] = wallet
		}

		// Parse amount
		if amountRaw, ok := itemMap["amount"]; ok {
			amountMap, ok := amountRaw.(map[string]interface{})
			if ok {
				item["amount"] = map[string]interface{}{
					"currency": resolver.ResolveString(getString(amountMap, "currency")),
					"value":    resolver.ResolveString(getString(amountMap, "value")),
				}
			}
		}

		items = append(items, item)
	}

	return items, nil
}

// parseBillingInfo parses billing info from config
func parseBillingInfo(billingRaw interface{}, resolver executor.TemplateResolver) ([]map[string]interface{}, error) {
	arr, ok := billingRaw.([]interface{})
	if !ok {
		// Single billing info
		billingMap, ok := billingRaw.(map[string]interface{})
		if !ok {
			return nil, nil
		}
		arr = []interface{}{billingMap}
	}

	billing := make([]map[string]interface{}, 0, len(arr))
	for _, bRaw := range arr {
		bMap, ok := bRaw.(map[string]interface{})
		if !ok {
			continue
		}

		b := make(map[string]interface{})
		if email := resolver.ResolveString(getString(bMap, "email")); email != "" {
			b["email"] = email
		}

		// Parse name
		if nameRaw, ok := bMap["name"]; ok {
			nameMap, ok := nameRaw.(map[string]interface{})
			if ok {
				b["name"] = map[string]interface{}{
					"given_name": resolver.ResolveString(getString(nameMap, "given_name")),
					"surname":    resolver.ResolveString(getString(nameMap, "surname")),
				}
			}
		}

		// Parse address
		if addrRaw, ok := bMap["address"]; ok {
			addrMap, ok := addrRaw.(map[string]interface{})
			if ok {
				addr := map[string]interface{}{
					"country_code": resolver.ResolveString(getString(addrMap, "country_code")),
				}
				if line1 := resolver.ResolveString(getString(addrMap, "address_line_1")); line1 != "" {
					addr["address_line_1"] = line1
				}
				if line2 := resolver.ResolveString(getString(addrMap, "address_line_2")); line2 != "" {
					addr["address_line_2"] = line2
				}
				if admin1 := resolver.ResolveString(getString(addrMap, "admin_area_1")); admin1 != "" {
					addr["admin_area_1"] = admin1
				}
				if admin2 := resolver.ResolveString(getString(addrMap, "admin_area_2")); admin2 != "" {
					addr["admin_area_2"] = admin2
				}
				if postal := resolver.ResolveString(getString(addrMap, "postal_code")); postal != "" {
					addr["postal_code"] = postal
				}
				b["address"] = addr
			}
		}

		// Parse phone
		if phoneRaw, ok := bMap["phone"]; ok {
			phoneMap, ok := phoneRaw.(map[string]interface{})
			if ok {
				b["phone"] = map[string]interface{}{
					"country_code":   resolver.ResolveString(getString(phoneMap, "country_code")),
					"national_number": resolver.ResolveString(getString(phoneMap, "national_number")),
				}
			}
		}

		billing = append(billing, b)
	}

	return billing, nil
}

// parsePaymentTerm parses payment term from config
func parsePaymentTerm(termRaw interface{}, resolver executor.TemplateResolver) (map[string]interface{}, error) {
	termMap, ok := termRaw.(map[string]interface{})
	if !ok {
		return nil, nil
	}

	term := make(map[string]interface{})
	if termType := resolver.ResolveString(getString(termMap, "term_type")); termType != "" {
		term["term_type"] = termType
	}
	if dueDate := resolver.ResolveString(getString(termMap, "due_date")); dueDate != "" {
		term["due_date"] = dueDate
	}

	return term, nil
}

// parseDiscount parses discount from config
func parseDiscount(discountRaw interface{}, resolver executor.TemplateResolver) (map[string]interface{}, error) {
	discountMap, ok := discountRaw.(map[string]interface{})
	if !ok {
		return nil, nil
	}

	discount := make(map[string]interface{})
	if percent := resolver.ResolveString(getString(discountMap, "percent")); percent != "" {
		discount["percent"] = percent
	}

	// Parse amount
	if amountRaw, ok := discountMap["amount"]; ok {
		amountMap, ok := amountRaw.(map[string]interface{})
		if ok {
			discount["amount"] = map[string]interface{}{
				"currency_code": resolver.ResolveString(getString(amountMap, "currency_code")),
				"value":         resolver.ResolveString(getString(amountMap, "value")),
			}
		}
	}

	return discount, nil
}

// parseShippingInfo parses shipping info from config
func parseShippingInfo(shippingRaw interface{}, resolver executor.TemplateResolver) (map[string]interface{}, error) {
	shippingMap, ok := shippingRaw.(map[string]interface{})
	if !ok {
		return nil, nil
	}

	shipping := make(map[string]interface{})

	// Parse name
	if nameRaw, ok := shippingMap["name"]; ok {
		nameMap, ok := nameRaw.(map[string]interface{})
		if ok {
			shipping["name"] = map[string]interface{}{
				"given_name": resolver.ResolveString(getString(nameMap, "given_name")),
				"surname":    resolver.ResolveString(getString(nameMap, "surname")),
			}
		}
	}

	// Parse address
	if addrRaw, ok := shippingMap["address"]; ok {
		addrMap, ok := addrRaw.(map[string]interface{})
		if ok {
			addr := map[string]interface{}{
				"country_code": resolver.ResolveString(getString(addrMap, "country_code")),
			}
			if line1 := resolver.ResolveString(getString(addrMap, "address_line_1")); line1 != "" {
				addr["address_line_1"] = line1
			}
			if line2 := resolver.ResolveString(getString(addrMap, "address_line_2")); line2 != "" {
				addr["address_line_2"] = line2
			}
			if admin1 := resolver.ResolveString(getString(addrMap, "admin_area_1")); admin1 != "" {
				addr["admin_area_1"] = admin1
			}
			if admin2 := resolver.ResolveString(getString(addrMap, "admin_area_2")); admin2 != "" {
				addr["admin_area_2"] = admin2
			}
			if postal := resolver.ResolveString(getString(addrMap, "postal_code")); postal != "" {
				addr["postal_code"] = postal
			}
			shipping["address"] = addr
		}
	}

	return shipping, nil
}

// parseInvoiceItems parses invoice items from config
func parseInvoiceItems(itemsRaw []interface{}, resolver executor.TemplateResolver) ([]map[string]interface{}, error) {
	items := make([]map[string]interface{}, 0, len(itemsRaw))

	for _, itemRaw := range itemsRaw {
		itemMap, ok := itemRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid invoice item format")
		}

		item := make(map[string]interface{})

		if name := resolver.ResolveString(getString(itemMap, "name")); name != "" {
			item["name"] = name
		}
		if desc := resolver.ResolveString(getString(itemMap, "description")); desc != "" {
			item["description"] = desc
		}
		if qty := resolver.ResolveString(getString(itemMap, "quantity")); qty != "" {
			item["quantity"] = qty
		} else {
			item["quantity"] = "1"
		}
		if uom := resolver.ResolveString(getString(itemMap, "unit_of_measure")); uom != "" {
			item["unit_of_measure"] = uom
		}

		// Parse unit amount
		if unitAmountRaw, ok := itemMap["unit_amount"]; ok {
			amountMap, ok := unitAmountRaw.(map[string]interface{})
			if ok {
				item["unit_amount"] = map[string]interface{}{
					"currency_code": resolver.ResolveString(getString(amountMap, "currency_code")),
					"value":         resolver.ResolveString(getString(amountMap, "value")),
				}
			}
		}

		// Parse tax
		if taxRaw, ok := itemMap["tax"]; ok {
			taxMap, ok := taxRaw.(map[string]interface{})
			if ok {
				tax := map[string]interface{}{}
				if taxID := resolver.ResolveString(getString(taxMap, "id")); taxID != "" {
					tax["id"] = taxID
				}
				if taxName := resolver.ResolveString(getString(taxMap, "name")); taxName != "" {
					tax["name"] = taxName
				}
				if taxPercent := resolver.ResolveString(getString(taxMap, "percent")); taxPercent != "" {
					tax["percent"] = taxPercent
				}
				if len(tax) > 0 {
					item["tax"] = tax
				}
			}
		}

		// Parse discount
		if discountRaw, ok := itemMap["discount"]; ok {
			discountMap, ok := discountRaw.(map[string]interface{})
			if ok {
				discount := map[string]interface{}{}
				if percent := resolver.ResolveString(getString(discountMap, "percent")); percent != "" {
					discount["percent"] = percent
				}
				if len(discount) > 0 {
					item["discount"] = discount
				}
			}
		}

		items = append(items, item)
	}

	return items, nil
}

// parseMerchantInfo parses merchant info from config
func parseMerchantInfo(merchantRaw interface{}, resolver executor.TemplateResolver) (map[string]interface{}, error) {
	merchantMap, ok := merchantRaw.(map[string]interface{})
	if !ok {
		return nil, nil
	}

	merchant := make(map[string]interface{})
	if email := resolver.ResolveString(getString(merchantMap, "email")); email != "" {
		merchant["email"] = email
	}
	if businessName := resolver.ResolveString(getString(merchantMap, "business_name")); businessName != "" {
		merchant["business_name"] = businessName
	}
	if website := resolver.ResolveString(getString(merchantMap, "website")); website != "" {
		merchant["website"] = website
	}
	if taxID := resolver.ResolveString(getString(merchantMap, "tax_id")); taxID != "" {
		merchant["tax_id"] = taxID
	}

	// Parse phone
	if phoneRaw, ok := merchantMap["phone"]; ok {
		phoneMap, ok := phoneRaw.(map[string]interface{})
		if ok {
			merchant["phone"] = map[string]interface{}{
				"country_code":    resolver.ResolveString(getString(phoneMap, "country_code")),
				"national_number": resolver.ResolveString(getString(phoneMap, "national_number")),
			}
		}
	}

	// Parse address
	if addrRaw, ok := merchantMap["address"]; ok {
		addrMap, ok := addrRaw.(map[string]interface{})
		if ok {
			addr := map[string]interface{}{
				"country_code": resolver.ResolveString(getString(addrMap, "country_code")),
			}
			if line1 := resolver.ResolveString(getString(addrMap, "address_line_1")); line1 != "" {
				addr["address_line_1"] = line1
			}
			if line2 := resolver.ResolveString(getString(addrMap, "address_line_2")); line2 != "" {
				addr["address_line_2"] = line2
			}
			if admin1 := resolver.ResolveString(getString(addrMap, "admin_area_1")); admin1 != "" {
				addr["admin_area_1"] = admin1
			}
			if admin2 := resolver.ResolveString(getString(addrMap, "admin_area_2")); admin2 != "" {
				addr["admin_area_2"] = admin2
			}
			if postal := resolver.ResolveString(getString(addrMap, "postal_code")); postal != "" {
				addr["postal_code"] = postal
			}
			merchant["address"] = addr
		}
	}

	return merchant, nil
}

// getInvoiceURL extracts the invoice URL from links
func getInvoiceURL(links []Link) string {
	for _, link := range links {
		if link.Rel == "self" {
			return link.Href
		}
	}
	return ""
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50116"
	}

	server := grpc.NewSkillServer("skill-paypal", "1.0.0")

	// Register executors for all node types from skill.yaml
	server.RegisterExecutor("paypal-order-create", &OrderCreateExecutor{})
	server.RegisterExecutor("paypal-order-capture", &OrderCaptureExecutor{})
	server.RegisterExecutor("paypal-order-get", &OrderGetExecutor{})
	server.RegisterExecutor("paypal-payment-list", &PaymentListExecutor{})
	server.RegisterExecutor("paypal-refund", &RefundExecutor{})
	server.RegisterExecutor("paypal-payout", &PayoutExecutor{})
	server.RegisterExecutor("paypal-transaction-list", &TransactionListExecutor{})
	server.RegisterExecutor("paypal-invoice-create", &InvoiceCreateExecutor{})

	fmt.Printf("Starting skill-paypal gRPC server on port %s\n", port)
	if err := server.Serve(":" + port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
