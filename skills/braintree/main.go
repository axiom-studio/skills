package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
)

const (
	// Braintree API endpoints
	BraintreeAPIBaseURL        = "https://api.braintreegateway.com"
	BraintreeAPIBaseURLSandbox = "https://api.sandbox.braintreegateway.com"
)

// ============================================================================
// Braintree Client
// ============================================================================

// BraintreeClient represents a Braintree API client
type BraintreeClient struct {
	httpClient *http.Client
	merchantID string
	publicKey  string
	privateKey string
	baseURL    string
}

// NewBraintreeClient creates a new Braintree client
func NewBraintreeClient(merchantID, publicKey, privateKey, environment string) *BraintreeClient {
	isSandbox := strings.ToLower(environment) == "sandbox" || strings.ToLower(environment) == "test" || environment == ""

	baseURL := BraintreeAPIBaseURL
	if isSandbox {
		baseURL = BraintreeAPIBaseURLSandbox
	}

	return &BraintreeClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		merchantID: merchantID,
		publicKey:  publicKey,
		privateKey: privateKey,
		baseURL:    baseURL,
	}
}

// doRequest performs an authenticated HTTP request to the Braintree API
func (c *BraintreeClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		xmlData, err := xml.MarshalIndent(body, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(xmlData)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Braintree uses Basic Auth with public_key:private_key
	auth := c.publicKey + ":" + c.privateKey
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Accept", "application/xml")

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
		var errResp BraintreeErrorResponse
		if err := xml.Unmarshal(respBody, &errResp); err == nil && len(errResp.Errors.ErrorList) > 0 {
			return nil, fmt.Errorf("Braintree API error (%d): %s - %s",
				resp.StatusCode, errResp.Errors.ErrorList[0].Error.Code,
				errResp.Errors.ErrorList[0].Error.Message)
		}
		return nil, fmt.Errorf("Braintree API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// BraintreeErrorResponse represents a Braintree error response
type BraintreeErrorResponse struct {
	XMLName xml.Name `xml:"api-error-response"`
	Errors  struct {
		ErrorList []struct {
			Error struct {
				Code    string `xml:"code"`
				Message string `xml:"message"`
			} `xml:"error"`
		} `xml:"errors"`
	} `xml:"errors"`
}

// ============================================================================
// Braintree Skill Server
// ============================================================================

type BraintreeSkill struct {
	grpc.SkillServer
}

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50117"
	}

	server := grpc.NewSkillServer("skill-braintree", "1.0.0")

	// Register executors for all node types from skill.yaml
	server.RegisterExecutor("braintree-transaction-sale", &TransactionSaleExecutor{})
	server.RegisterExecutor("braintree-transaction-void", &TransactionVoidExecutor{})
	server.RegisterExecutor("braintree-transaction-refund", &TransactionRefundExecutor{})
	server.RegisterExecutor("braintree-transaction-get", &TransactionGetExecutor{})
	server.RegisterExecutor("braintree-customer-create", &CustomerCreateExecutor{})
	server.RegisterExecutor("braintree-customer-list", &CustomerListExecutor{})
	server.RegisterExecutor("braintree-subscription-create", &SubscriptionCreateExecutor{})
	server.RegisterExecutor("braintree-subscription-cancel", &SubscriptionCancelExecutor{})

	fmt.Printf("Starting skill-braintree gRPC server on port %s\n", port)
	if err := server.Serve(":" + port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// Config Structures
// ============================================================================

// TransactionSaleConfig defines the configuration for a sale transaction
type TransactionSaleConfig struct {
	Amount              string `json:"amount"`
	Currency            string `json:"currency"`
	PaymentMethodNonce  string `json:"payment_method_nonce"`
	PaymentMethodToken  string `json:"payment_method_token"`
	CustomerID          string `json:"customer_id"`
	OrderID             string `json:"order_id"`
	DeviceData          string `json:"device_data,omitempty"`
	StoreInVault        bool   `json:"store_in_vault,omitempty"`
	SubmitForSettlement bool   `json:"submit_for_settlement,omitempty"`
}

// TransactionVoidConfig defines the configuration for voiding a transaction
type TransactionVoidConfig struct {
	TransactionID string `json:"transaction_id"`
}

// TransactionRefundConfig defines the configuration for refunding a transaction
type TransactionRefundConfig struct {
	TransactionID string `json:"transaction_id"`
	Amount        string `json:"amount,omitempty"`
}

// TransactionGetConfig defines the configuration for getting a transaction
type TransactionGetConfig struct {
	TransactionID string `json:"transaction_id"`
}

// CustomerCreateConfig defines the configuration for creating a customer
type CustomerCreateConfig struct {
	FirstName          string `json:"first_name"`
	LastName           string `json:"last_name"`
	Company            string `json:"company"`
	Email              string `json:"email"`
	Phone              string `json:"phone"`
	PaymentMethodNonce string `json:"payment_method_nonce,omitempty"`
}

// CustomerListConfig defines the configuration for listing customers
type CustomerListConfig struct {
	Page        int    `json:"page,omitempty"`
	PageSize    int    `json:"page_size,omitempty"`
	SearchQuery string `json:"search_query,omitempty"`
}

// SubscriptionCreateConfig defines the configuration for creating a subscription
type SubscriptionCreateConfig struct {
	PlanID                string `json:"plan_id"`
	PaymentMethodToken    string `json:"payment_method_token"`
	PaymentMethodNonce    string `json:"payment_method_nonce"`
	CustomerID            string `json:"customer_id"`
	ID                    string `json:"id,omitempty"`
	Price                 string `json:"price,omitempty"`
	NumberOfBillingCycles int    `json:"number_of_billing_cycles,omitempty"`
}

// SubscriptionCancelConfig defines the configuration for canceling a subscription
type SubscriptionCancelConfig struct {
	SubscriptionID string `json:"subscription_id"`
}

// ============================================================================
// XML Request/Response Structures
// ============================================================================

// TransactionSaleRequest represents the XML request for a transaction sale
type TransactionSaleRequest struct {
	XMLName            xml.Name `xml:"transaction"`
	Amount             string   `xml:"amount"`
	CurrencyISOCode    string   `xml:"currency-iso-code,omitempty"`
	PaymentMethodNonce string   `xml:"payment-method-nonce,omitempty"`
	PaymentMethodToken string   `xml:"payment-method-token,omitempty"`
	CustomerID         string   `xml:"customer-id,omitempty"`
	OrderID            string   `xml:"order-id,omitempty"`
	DeviceData         string   `xml:"device-data,omitempty"`
	Options            *TransactionOptions `xml:"options,omitempty"`
}

// TransactionOptions represents transaction options
type TransactionOptions struct {
	StoreInVault        string `xml:"store-in-vault,omitempty"`
	SubmitForSettlement string `xml:"submit-for-settlement,omitempty"`
}

// TransactionResponse represents the XML response for a transaction
type TransactionResponse struct {
	XMLName              xml.Name `xml:"transaction"`
	ID                   string   `xml:"id"`
	Status               string   `xml:"status"`
	Amount               string   `xml:"amount"`
	CurrencyISOCode      string   `xml:"currency-iso-code"`
	Type                 string   `xml:"type"`
	CreatedAt            string   `xml:"created-at"`
	UpdatedAt            string   `xml:"updated-at"`
	ProcessorResponseCode string  `xml:"processor-response-code"`
	ProcessorResponseText string  `xml:"processor-response-text"`
	GatewayRejectionReason string `xml:"gateway-rejection-reason"`
	OrderID              string   `xml:"order-id"`
	Customer             *CustomerData `xml:"customer"`
	CreditCard           *CreditCardData `xml:"credit-card"`
}

// CustomerData represents customer information in the response
type CustomerData struct {
	ID        string `xml:"id"`
	FirstName string `xml:"first-name"`
	LastName  string `xml:"last-name"`
	Email     string `xml:"email"`
}

// CreditCardData represents credit card information in the response
type CreditCardData struct {
	Token           string `xml:"token"`
	CardType        string `xml:"card-type"`
	Last4           string `xml:"last-4"`
	ExpirationMonth string `xml:"expiration-month"`
	ExpirationYear  string `xml:"expiration-year"`
	CardholderName  string `xml:"cardholder-name"`
}

// CustomerCreateRequest represents the XML request for creating a customer
type CustomerCreateRequest struct {
	XMLName            xml.Name `xml:"customer"`
	FirstName          string   `xml:"first-name,omitempty"`
	LastName           string   `xml:"last-name,omitempty"`
	Company            string   `xml:"company,omitempty"`
	Email              string   `xml:"email,omitempty"`
	Phone              string   `xml:"phone,omitempty"`
	PaymentMethodNonce string   `xml:"payment-method-nonce,omitempty"`
}

// CustomerResponse represents the XML response for a customer
type CustomerResponse struct {
	XMLName   xml.Name `xml:"customer"`
	ID        string   `xml:"id"`
	FirstName string   `xml:"first-name"`
	LastName  string   `xml:"last-name"`
	Company   string   `xml:"company"`
	Email     string   `xml:"email"`
	Phone     string   `xml:"phone"`
	CreatedAt string   `xml:"created-at"`
	UpdatedAt string   `xml:"updated-at"`
	CreditCards struct {
		CreditCard []CreditCardData `xml:"credit-card"`
	} `xml:"credit-cards"`
	PaymentMethods struct {
		PaymentMethod []PaymentMethodData `xml:"payment-method"`
	} `xml:"payment-methods"`
}

// PaymentMethodData represents a payment method
type PaymentMethodData struct {
	XMLName xml.Name
	Token   string `xml:"token"`
	Type    string `xml:"type"`
}

// CustomersResponse represents the XML response for customer search
type CustomersResponse struct {
	XMLName     xml.Name `xml:"customers"`
	CurrentPage int      `xml:"current-page"`
	PageSize    int      `xml:"page-size"`
	TotalItems  int      `xml:"total-items"`
	Customer    []CustomerResponse `xml:"customer"`
}

// SubscriptionCreateRequest represents the XML request for creating a subscription
type SubscriptionCreateRequest struct {
	XMLName               xml.Name `xml:"subscription"`
	PlanID                string   `xml:"plan-id"`
	PaymentMethodToken    string   `xml:"payment-method-token,omitempty"`
	PaymentMethodNonce    string   `xml:"payment-method-nonce,omitempty"`
	CustomerID            string   `xml:"customer-id,omitempty"`
	ID                    string   `xml:"id,omitempty"`
	Price                 string   `xml:"price,omitempty"`
	NumberOfBillingCycles string   `xml:"number-of-billing-cycles,omitempty"`
}

// SubscriptionResponse represents the XML response for a subscription
type SubscriptionResponse struct {
	XMLName                xml.Name `xml:"subscription"`
	ID                     string   `xml:"id"`
	Status                 string   `xml:"status"`
	PlanID                 string   `xml:"plan-id"`
	Price                  string   `xml:"price"`
	CurrentBillingCycle    string   `xml:"current-billing-cycle"`
	CurrentBillingPeriodStart string `xml:"current-billing-period-start"`
	CurrentBillingPeriodEnd   string `xml:"current-billing-period-end"`
	NextBillingPeriodStart    string `xml:"next-billing-period-start"`
	NextBillingPeriodEnd      string `xml:"next-billing-period-end"`
	NumberOfBillingCycles  string   `xml:"number-of-billing-cycles"`
	TrialDuration          string   `xml:"trial-duration"`
	TrialDurationUnit      string   `xml:"trial-duration-unit"`
	FirstBillingDate       string   `xml:"first-billing-date"`
	StartDate              string   `xml:"start-date"`
	CreatedAt              string   `xml:"created-at"`
	UpdatedAt              string   `xml:"updated-at"`
	CanceledAt             string   `xml:"canceled-at"`
	PaymentMethodToken     string   `xml:"payment-method-token"`
	Customer               *CustomerData `xml:"customer"`
}

// ============================================================================
// Helper Functions
// ============================================================================

// getBraintreeClient creates a new Braintree client from bindings
func getBraintreeClient(bindings map[string]interface{}) (*BraintreeClient, error) {
	environmentStr, ok := bindings["braintree_environment"].(string)
	if !ok {
		environmentStr = "sandbox"
	}

	merchantID, ok := bindings["braintree_merchant_id"].(string)
	if !ok {
		return nil, fmt.Errorf("braintree_merchant_id is required in bindings")
	}

	publicKey, ok := bindings["braintree_public_key"].(string)
	if !ok {
		return nil, fmt.Errorf("braintree_public_key is required in bindings")
	}

	privateKey, ok := bindings["braintree_private_key"].(string)
	if !ok {
		return nil, fmt.Errorf("braintree_private_key is required in bindings")
	}

	return NewBraintreeClient(merchantID, publicKey, privateKey, environmentStr), nil
}

// getString safely gets a string from a map
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getInt safely gets an int from a map
func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(int); ok {
		return v
	}
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

// getBool safely gets a bool from a map
func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// getBindings extracts bindings from the resolver via type assertion
func getBindings(resolver executor.TemplateResolver) map[string]interface{} {
	if br, ok := resolver.(interface{ GetBindings() map[string]interface{} }); ok {
		return br.GetBindings()
	}
	return make(map[string]interface{})
}

// ============================================================================
// Transaction Sale Executor
// ============================================================================

// TransactionSaleExecutor handles braintree-transaction-sale
type TransactionSaleExecutor struct{}

func (e *TransactionSaleExecutor) Type() string {
	return "braintree-transaction-sale"
}

func (e *TransactionSaleExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	bindings := getBindings(resolver)

	client, err := getBraintreeClient(bindings)
	if err != nil {
		return nil, fmt.Errorf("failed to create Braintree client: %w", err)
	}

	saleConfig := TransactionSaleConfig{
		Amount:              resolver.ResolveString(getString(config, "amount")),
		Currency:            resolver.ResolveString(getString(config, "currency")),
		PaymentMethodNonce:  resolver.ResolveString(getString(config, "payment_method_nonce")),
		PaymentMethodToken:  resolver.ResolveString(getString(config, "payment_method_token")),
		CustomerID:          resolver.ResolveString(getString(config, "customer_id")),
		OrderID:             resolver.ResolveString(getString(config, "order_id")),
		DeviceData:          resolver.ResolveString(getString(config, "device_data")),
		StoreInVault:        getBool(config, "store_in_vault"),
		SubmitForSettlement: getBool(config, "submit_for_settlement"),
	}

	if saleConfig.Amount == "" {
		return nil, fmt.Errorf("amount is required")
	}

	if saleConfig.PaymentMethodNonce == "" && saleConfig.PaymentMethodToken == "" {
		return nil, fmt.Errorf("payment_method_nonce or payment_method_token is required")
	}

	txRequest := &TransactionSaleRequest{
		Amount:          saleConfig.Amount,
		CurrencyISOCode: saleConfig.Currency,
		OrderID:         saleConfig.OrderID,
		DeviceData:      saleConfig.DeviceData,
	}

	if saleConfig.PaymentMethodNonce != "" {
		txRequest.PaymentMethodNonce = saleConfig.PaymentMethodNonce
	} else if saleConfig.PaymentMethodToken != "" {
		txRequest.PaymentMethodToken = saleConfig.PaymentMethodToken
	}

	if saleConfig.CustomerID != "" {
		txRequest.CustomerID = saleConfig.CustomerID
	}

	txRequest.Options = &TransactionOptions{}
	if saleConfig.StoreInVault {
		txRequest.Options.StoreInVault = "true"
	}
	if saleConfig.SubmitForSettlement {
		txRequest.Options.SubmitForSettlement = "true"
	}

	respBody, err := client.doRequest(ctx, "POST", "/merchants/"+client.merchantID+"/transactions", txRequest)
	if err != nil {
		return nil, err
	}

	var tx TransactionResponse
	if err := xml.Unmarshal(respBody, &tx); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := make(map[string]interface{})
	output["transaction_id"] = tx.ID
	output["status"] = tx.Status
	output["amount"] = tx.Amount
	output["currency"] = tx.CurrencyISOCode
	output["type"] = tx.Type
	output["created_at"] = tx.CreatedAt
	output["processor_response_code"] = tx.ProcessorResponseCode
	output["processor_response_text"] = tx.ProcessorResponseText

	if tx.Customer != nil {
		output["customer_id"] = tx.Customer.ID
		output["customer_first_name"] = tx.Customer.FirstName
		output["customer_last_name"] = tx.Customer.LastName
		output["customer_email"] = tx.Customer.Email
	}

	if tx.CreditCard != nil {
		output["payment_method_token"] = tx.CreditCard.Token
		output["card_type"] = tx.CreditCard.CardType
		output["last_four"] = tx.CreditCard.Last4
		output["expiration_month"] = tx.CreditCard.ExpirationMonth
		output["expiration_year"] = tx.CreditCard.ExpirationYear
		output["cardholder_name"] = tx.CreditCard.CardholderName
	}

	if tx.GatewayRejectionReason != "" {
		return nil, fmt.Errorf("transaction rejected: %s", tx.GatewayRejectionReason)
	}

	output["raw_response"] = string(respBody)

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Transaction Void Executor
// ============================================================================

// TransactionVoidExecutor handles braintree-transaction-void
type TransactionVoidExecutor struct{}

func (e *TransactionVoidExecutor) Type() string {
	return "braintree-transaction-void"
}

func (e *TransactionVoidExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	bindings := getBindings(resolver)

	client, err := getBraintreeClient(bindings)
	if err != nil {
		return nil, fmt.Errorf("failed to create Braintree client: %w", err)
	}

	voidConfig := TransactionVoidConfig{
		TransactionID: resolver.ResolveString(getString(config, "transaction_id")),
	}

	if voidConfig.TransactionID == "" {
		return nil, fmt.Errorf("transaction_id is required")
	}

	path := fmt.Sprintf("/merchants/%s/transactions/%s/void", client.merchantID, voidConfig.TransactionID)
	respBody, err := client.doRequest(ctx, "PUT", path, nil)
	if err != nil {
		return nil, err
	}

	var tx TransactionResponse
	if err := xml.Unmarshal(respBody, &tx); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := make(map[string]interface{})
	output["transaction_id"] = tx.ID
	output["status"] = tx.Status
	output["amount"] = tx.Amount
	output["currency"] = tx.CurrencyISOCode
	output["type"] = tx.Type
	output["created_at"] = tx.CreatedAt
	output["raw_response"] = string(respBody)

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Transaction Refund Executor
// ============================================================================

// TransactionRefundExecutor handles braintree-transaction-refund
type TransactionRefundExecutor struct{}

func (e *TransactionRefundExecutor) Type() string {
	return "braintree-transaction-refund"
}

func (e *TransactionRefundExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	bindings := getBindings(resolver)

	client, err := getBraintreeClient(bindings)
	if err != nil {
		return nil, fmt.Errorf("failed to create Braintree client: %w", err)
	}

	refundConfig := TransactionRefundConfig{
		TransactionID: resolver.ResolveString(getString(config, "transaction_id")),
		Amount:        resolver.ResolveString(getString(config, "amount")),
	}

	if refundConfig.TransactionID == "" {
		return nil, fmt.Errorf("transaction_id is required")
	}

	type RefundRequest struct {
		XMLName xml.Name `xml:"transaction"`
		Amount  string   `xml:"amount,omitempty"`
	}

	var refundReq *RefundRequest
	if refundConfig.Amount != "" {
		refundReq = &RefundRequest{Amount: refundConfig.Amount}
	}

	path := fmt.Sprintf("/merchants/%s/transactions/%s/refunds", client.merchantID, refundConfig.TransactionID)
	respBody, err := client.doRequest(ctx, "POST", path, refundReq)
	if err != nil {
		return nil, err
	}

	var tx TransactionResponse
	if err := xml.Unmarshal(respBody, &tx); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := make(map[string]interface{})
	output["transaction_id"] = tx.ID
	output["status"] = tx.Status
	output["amount"] = tx.Amount
	output["currency"] = tx.CurrencyISOCode
	output["type"] = tx.Type
	output["created_at"] = tx.CreatedAt
	output["raw_response"] = string(respBody)

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Transaction Get Executor
// ============================================================================

// TransactionGetExecutor handles braintree-transaction-get
type TransactionGetExecutor struct{}

func (e *TransactionGetExecutor) Type() string {
	return "braintree-transaction-get"
}

func (e *TransactionGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	bindings := getBindings(resolver)

	client, err := getBraintreeClient(bindings)
	if err != nil {
		return nil, fmt.Errorf("failed to create Braintree client: %w", err)
	}

	getConfig := TransactionGetConfig{
		TransactionID: resolver.ResolveString(getString(config, "transaction_id")),
	}

	if getConfig.TransactionID == "" {
		return nil, fmt.Errorf("transaction_id is required")
	}

	path := fmt.Sprintf("/merchants/%s/transactions/%s", client.merchantID, getConfig.TransactionID)
	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var tx TransactionResponse
	if err := xml.Unmarshal(respBody, &tx); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := make(map[string]interface{})
	output["transaction_id"] = tx.ID
	output["status"] = tx.Status
	output["amount"] = tx.Amount
	output["currency"] = tx.CurrencyISOCode
	output["type"] = tx.Type
	output["created_at"] = tx.CreatedAt
	output["updated_at"] = tx.UpdatedAt
	output["processor_response_code"] = tx.ProcessorResponseCode
	output["processor_response_text"] = tx.ProcessorResponseText
	output["order_id"] = tx.OrderID

	if tx.Customer != nil {
		output["customer_id"] = tx.Customer.ID
		output["customer_first_name"] = tx.Customer.FirstName
		output["customer_last_name"] = tx.Customer.LastName
		output["customer_email"] = tx.Customer.Email
	}

	if tx.CreditCard != nil {
		output["payment_method_token"] = tx.CreditCard.Token
		output["card_type"] = tx.CreditCard.CardType
		output["last_four"] = tx.CreditCard.Last4
		output["expiration_month"] = tx.CreditCard.ExpirationMonth
		output["expiration_year"] = tx.CreditCard.ExpirationYear
		output["cardholder_name"] = tx.CreditCard.CardholderName
	}

	output["raw_response"] = string(respBody)

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Customer Create Executor
// ============================================================================

// CustomerCreateExecutor handles braintree-customer-create
type CustomerCreateExecutor struct{}

func (e *CustomerCreateExecutor) Type() string {
	return "braintree-customer-create"
}

func (e *CustomerCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	bindings := getBindings(resolver)

	client, err := getBraintreeClient(bindings)
	if err != nil {
		return nil, fmt.Errorf("failed to create Braintree client: %w", err)
	}

	customerConfig := CustomerCreateConfig{
		FirstName:          resolver.ResolveString(getString(config, "first_name")),
		LastName:           resolver.ResolveString(getString(config, "last_name")),
		Company:            resolver.ResolveString(getString(config, "company")),
		Email:              resolver.ResolveString(getString(config, "email")),
		Phone:              resolver.ResolveString(getString(config, "phone")),
		PaymentMethodNonce: resolver.ResolveString(getString(config, "payment_method_nonce")),
	}

	customerRequest := &CustomerCreateRequest{
		FirstName:          customerConfig.FirstName,
		LastName:           customerConfig.LastName,
		Company:            customerConfig.Company,
		Email:              customerConfig.Email,
		Phone:              customerConfig.Phone,
		PaymentMethodNonce: customerConfig.PaymentMethodNonce,
	}

	respBody, err := client.doRequest(ctx, "POST", "/merchants/"+client.merchantID+"/customers", customerRequest)
	if err != nil {
		return nil, err
	}

	var customer CustomerResponse
	if err := xml.Unmarshal(respBody, &customer); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := make(map[string]interface{})
	output["customer_id"] = customer.ID
	output["first_name"] = customer.FirstName
	output["last_name"] = customer.LastName
	output["company"] = customer.Company
	output["email"] = customer.Email
	output["phone"] = customer.Phone
	output["created_at"] = customer.CreatedAt
	output["updated_at"] = customer.UpdatedAt

	if len(customer.CreditCards.CreditCard) > 0 {
		cc := customer.CreditCards.CreditCard[0]
		output["payment_method_token"] = cc.Token
		output["card_type"] = cc.CardType
		output["last_four"] = cc.Last4
		output["expiration_month"] = cc.ExpirationMonth
		output["expiration_year"] = cc.ExpirationYear
		output["cardholder_name"] = cc.CardholderName
	}

	if len(customer.PaymentMethods.PaymentMethod) > 0 {
		pm := customer.PaymentMethods.PaymentMethod[0]
		output["default_payment_method_token"] = pm.Token
		output["default_payment_method_type"] = pm.Type
	}

	output["raw_response"] = string(respBody)

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Customer List Executor
// ============================================================================

// CustomerListExecutor handles braintree-customer-list
type CustomerListExecutor struct{}

func (e *CustomerListExecutor) Type() string {
	return "braintree-customer-list"
}

func (e *CustomerListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	bindings := getBindings(resolver)

	client, err := getBraintreeClient(bindings)
	if err != nil {
		return nil, fmt.Errorf("failed to create Braintree client: %w", err)
	}

	listConfig := CustomerListConfig{
		SearchQuery: resolver.ResolveString(getString(config, "search_query")),
	}

	// Build search query
	searchQuery := "<customers><search>"
	if listConfig.SearchQuery != "" {
		searchQuery += fmt.Sprintf("<email is>%s</email>", listConfig.SearchQuery)
	}
	searchQuery += "</search></customers>"

	path := fmt.Sprintf("/merchants/%s/customers/advanced_search", client.merchantID)
	respBody, err := client.doRequest(ctx, "POST", path, searchQuery)
	if err != nil {
		return nil, err
	}

	var customers CustomersResponse
	if err := xml.Unmarshal(respBody, &customers); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := make(map[string]interface{})
	output["total_items"] = customers.TotalItems
	output["page_size"] = customers.PageSize
	output["current_page"] = customers.CurrentPage

	customerList := make([]map[string]interface{}, len(customers.Customer))
	for i, c := range customers.Customer {
		customerList[i] = map[string]interface{}{
			"id":         c.ID,
			"first_name": c.FirstName,
			"last_name":  c.LastName,
			"company":    c.Company,
			"email":      c.Email,
			"phone":      c.Phone,
			"created_at": c.CreatedAt,
		}
	}
	output["customers"] = customerList
	output["raw_response"] = string(respBody)

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Subscription Create Executor
// ============================================================================

// SubscriptionCreateExecutor handles braintree-subscription-create
type SubscriptionCreateExecutor struct{}

func (e *SubscriptionCreateExecutor) Type() string {
	return "braintree-subscription-create"
}

func (e *SubscriptionCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	bindings := getBindings(resolver)

	client, err := getBraintreeClient(bindings)
	if err != nil {
		return nil, fmt.Errorf("failed to create Braintree client: %w", err)
	}

	subConfig := SubscriptionCreateConfig{
		PlanID:                resolver.ResolveString(getString(config, "plan_id")),
		PaymentMethodToken:    resolver.ResolveString(getString(config, "payment_method_token")),
		PaymentMethodNonce:    resolver.ResolveString(getString(config, "payment_method_nonce")),
		CustomerID:            resolver.ResolveString(getString(config, "customer_id")),
		ID:                    resolver.ResolveString(getString(config, "id")),
		Price:                 resolver.ResolveString(getString(config, "price")),
		NumberOfBillingCycles: getInt(config, "number_of_billing_cycles"),
	}

	if subConfig.PlanID == "" {
		return nil, fmt.Errorf("plan_id is required")
	}

	subRequest := &SubscriptionCreateRequest{
		PlanID:     subConfig.PlanID,
		ID:         subConfig.ID,
		Price:      subConfig.Price,
		CustomerID: subConfig.CustomerID,
	}

	if subConfig.NumberOfBillingCycles > 0 {
		subRequest.NumberOfBillingCycles = fmt.Sprintf("%d", subConfig.NumberOfBillingCycles)
	}

	if subConfig.PaymentMethodToken != "" {
		subRequest.PaymentMethodToken = subConfig.PaymentMethodToken
	} else if subConfig.PaymentMethodNonce != "" {
		subRequest.PaymentMethodNonce = subConfig.PaymentMethodNonce
	}

	respBody, err := client.doRequest(ctx, "POST", "/merchants/"+client.merchantID+"/subscriptions", subRequest)
	if err != nil {
		return nil, err
	}

	var subscription SubscriptionResponse
	if err := xml.Unmarshal(respBody, &subscription); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := make(map[string]interface{})
	output["subscription_id"] = subscription.ID
	output["status"] = subscription.Status
	output["plan_id"] = subscription.PlanID
	output["price"] = subscription.Price
	output["current_billing_cycle"] = subscription.CurrentBillingCycle
	output["current_billing_period_start"] = subscription.CurrentBillingPeriodStart
	output["current_billing_period_end"] = subscription.CurrentBillingPeriodEnd
	output["next_billing_period_start"] = subscription.NextBillingPeriodStart
	output["next_billing_period_end"] = subscription.NextBillingPeriodEnd
	output["number_of_billing_cycles"] = subscription.NumberOfBillingCycles
	output["trial_duration"] = subscription.TrialDuration
	output["trial_duration_unit"] = subscription.TrialDurationUnit
	output["first_billing_date"] = subscription.FirstBillingDate
	output["start_date"] = subscription.StartDate
	output["created_at"] = subscription.CreatedAt
	output["updated_at"] = subscription.UpdatedAt
	output["payment_method_token"] = subscription.PaymentMethodToken

	if subscription.Customer != nil {
		output["customer_id"] = subscription.Customer.ID
		output["customer_first_name"] = subscription.Customer.FirstName
		output["customer_last_name"] = subscription.Customer.LastName
		output["customer_email"] = subscription.Customer.Email
	}

	output["raw_response"] = string(respBody)

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Subscription Cancel Executor
// ============================================================================

// SubscriptionCancelExecutor handles braintree-subscription-cancel
type SubscriptionCancelExecutor struct{}

func (e *SubscriptionCancelExecutor) Type() string {
	return "braintree-subscription-cancel"
}

func (e *SubscriptionCancelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	bindings := getBindings(resolver)

	client, err := getBraintreeClient(bindings)
	if err != nil {
		return nil, fmt.Errorf("failed to create Braintree client: %w", err)
	}

	cancelConfig := SubscriptionCancelConfig{
		SubscriptionID: resolver.ResolveString(getString(config, "subscription_id")),
	}

	if cancelConfig.SubscriptionID == "" {
		return nil, fmt.Errorf("subscription_id is required")
	}

	path := fmt.Sprintf("/merchants/%s/subscriptions/%s/cancel", client.merchantID, cancelConfig.SubscriptionID)
	respBody, err := client.doRequest(ctx, "PUT", path, nil)
	if err != nil {
		return nil, err
	}

	var subscription SubscriptionResponse
	if err := xml.Unmarshal(respBody, &subscription); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := make(map[string]interface{})
	output["subscription_id"] = subscription.ID
	output["status"] = subscription.Status
	output["plan_id"] = subscription.PlanID
	output["price"] = subscription.Price
	output["canceled_at"] = subscription.CanceledAt
	output["created_at"] = subscription.CreatedAt
	output["updated_at"] = subscription.UpdatedAt
	output["raw_response"] = string(respBody)

	return &executor.StepResult{
		Output: output,
	}, nil
}
