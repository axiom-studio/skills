package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50052"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-shopify", "1.0.0")

	// Register Shopify executors with schemas
	server.RegisterExecutorWithSchema("shopify-product", &ShopifyProductExecutor{}, ShopifyProductSchema)
	server.RegisterExecutorWithSchema("shopify-order", &ShopifyOrderExecutor{}, ShopifyOrderSchema)
	server.RegisterExecutorWithSchema("shopify-customer", &ShopifyCustomerExecutor{}, ShopifyCustomerSchema)
	server.RegisterExecutorWithSchema("shopify-inventory", &ShopifyInventoryExecutor{}, ShopifyInventorySchema)
	server.RegisterExecutorWithSchema("shopify-fulfillment", &ShopifyFulfillmentExecutor{}, ShopifyFulfillmentSchema)

	fmt.Printf("Starting skill-shopify gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// Shopify API client helper
type ShopifyClient struct {
	storeURL   string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

func NewShopifyClient(storeURL, apiKey, apiSecret string) *ShopifyClient {
	return &ShopifyClient{
		storeURL:   strings.TrimSuffix(storeURL, "/"),
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{},
	}
}

func (c *ShopifyClient) doRequest(ctx context.Context, method, endpoint string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	url := fmt.Sprintf("%s%s", c.storeURL, endpoint)
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Access-Token", c.apiSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("shopify API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ============== Shopify Product Executor ==============

type ShopifyProductExecutor struct{}

type ShopifyProductConfig struct {
	StoreURL   string `json:"storeUrl" description:"Shopify store URL (e.g., https://mystore.myshopify.com)"`
	APIKey     string `json:"apiKey" description:"Shopify API key"`
	APISecret  string `json:"apiSecret" description:"Shopify API secret/token"`
	Action     string `json:"action" options:"list,get,create,update,delete" description:"Product action to perform"`
	ProductID  string `json:"productId" description:"Product ID (for get/update/delete)"`
	Title      string `json:"title" description:"Product title (for create/update)"`
	BodyHTML   string `json:"bodyHtml" description:"Product description HTML (for create/update)"`
	Vendor     string `json:"vendor" description:"Product vendor (for create/update)"`
	ProductType string `json:"productType" description:"Product type (for create/update)"`
	Tags       string `json:"tags" description:"Comma-separated tags (for create/update)"`
	Variants   []map[string]interface{} `json:"variants" description:"Product variants (for create/update)"`
	Images     []map[string]interface{} `json:"images" description:"Product images (for create/update)"`
	Status     string `json:"status" options:"active,draft,archived" description:"Product status"`
	Limit      int    `json:"limit" default:"50" description:"Number of products to return (for list)"`
}

var ShopifyProductSchema = resolver.NewSchemaBuilder("shopify-product").
	WithName("Shopify Product").
	WithCategory("ecommerce").
	WithIcon("shopping-bag").
	WithDescription("Manage Shopify products - list, get, create, update, delete").
	AddSection("Connection").
		AddTextField("storeUrl", "Store URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://mystore.myshopify.com"),
		).
		AddTextField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithHint("Shopify API key from admin panel"),
		).
		AddTextField("apiSecret", "API Secret",
			resolver.WithRequired(),
			resolver.WithHint("Shopify API access token"),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Products", Value: "list", Icon: "list"},
			{Label: "Get Product", Value: "get", Icon: "eye"},
			{Label: "Create Product", Value: "create", Icon: "plus"},
			{Label: "Update Product", Value: "update", Icon: "edit"},
			{Label: "Delete Product", Value: "delete", Icon: "trash"},
		}, resolver.WithDefault("list")).
		AddTextField("productId", "Product ID",
			resolver.WithHint("Required for get/update/delete actions"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithHint("Max products to return for list action"),
		).
		EndSection().
	AddSection("Product Details").
		AddTextField("title", "Title",
			resolver.WithHint("Required for create/update"),
		).
		AddTextareaField("bodyHtml", "Description",
			resolver.WithRows(4),
			resolver.WithHint("HTML description for create/update"),
		).
		AddTextField("vendor", "Vendor",
			resolver.WithHint("Product vendor/brand"),
		).
		AddTextField("productType", "Product Type",
			resolver.WithHint("Product category/type"),
		).
		AddTextField("tags", "Tags",
			resolver.WithHint("Comma-separated tags"),
		).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "Active", Value: "active"},
			{Label: "Draft", Value: "draft"},
			{Label: "Archived", Value: "archived"},
		}).
		AddJSONField("variants", "Variants",
			resolver.WithHeight(100),
			resolver.WithHint("Array of variant objects"),
		).
		AddJSONField("images", "Images",
			resolver.WithHeight(100),
			resolver.WithHint("Array of image objects with src URLs"),
		).
		EndSection().
	Build()

func (e *ShopifyProductExecutor) Type() string { return "shopify-product" }

func (e *ShopifyProductExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ShopifyProductConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.StoreURL == "" {
		return nil, fmt.Errorf("storeUrl is required")
	}
	if cfg.APISecret == "" {
		return nil, fmt.Errorf("apiSecret is required")
	}

	client := NewShopifyClient(cfg.StoreURL, cfg.APIKey, cfg.APISecret)

	switch cfg.Action {
	case "list":
		return e.listProducts(ctx, client, cfg.Limit)
	case "get":
		return e.getProduct(ctx, client, cfg.ProductID)
	case "create":
		return e.createProduct(ctx, client, cfg)
	case "update":
		return e.updateProduct(ctx, client, cfg)
	case "delete":
		return e.deleteProduct(ctx, client, cfg.ProductID)
	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}
}

func (e *ShopifyProductExecutor) listProducts(ctx context.Context, client *ShopifyClient, limit int) (*executor.StepResult, error) {
	endpoint := fmt.Sprintf("/admin/api/2024-01/products.json?limit=%d", limit)
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Products []map[string]interface{} `json:"products"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"products": result.Products,
			"count":    len(result.Products),
		},
	}, nil
}

func (e *ShopifyProductExecutor) getProduct(ctx context.Context, client *ShopifyClient, productID string) (*executor.StepResult, error) {
	if productID == "" {
		return nil, fmt.Errorf("productId is required for get action")
	}

	endpoint := fmt.Sprintf("/admin/api/2024-01/products/%s.json", productID)
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Product map[string]interface{} `json:"product"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"product": result.Product,
		},
	}, nil
}

func (e *ShopifyProductExecutor) createProduct(ctx context.Context, client *ShopifyClient, cfg ShopifyProductConfig) (*executor.StepResult, error) {
	if cfg.Title == "" {
		return nil, fmt.Errorf("title is required for create action")
	}

	product := map[string]interface{}{
		"title":       cfg.Title,
		"body_html":   cfg.BodyHTML,
		"vendor":      cfg.Vendor,
		"product_type": cfg.ProductType,
		"status":      cfg.Status,
	}

	if cfg.Tags != "" {
		product["tags"] = cfg.Tags
	}
	if len(cfg.Variants) > 0 {
		product["variants"] = cfg.Variants
	}
	if len(cfg.Images) > 0 {
		product["images"] = cfg.Images
	}

	body := map[string]interface{}{"product": product}
	respBody, err := client.doRequest(ctx, "POST", "/admin/api/2024-01/products.json", body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Product map[string]interface{} `json:"product"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"product": result.Product,
			"success": true,
		},
	}, nil
}

func (e *ShopifyProductExecutor) updateProduct(ctx context.Context, client *ShopifyClient, cfg ShopifyProductConfig) (*executor.StepResult, error) {
	if cfg.ProductID == "" {
		return nil, fmt.Errorf("productId is required for update action")
	}

	product := map[string]interface{}{"id": cfg.ProductID}
	if cfg.Title != "" {
		product["title"] = cfg.Title
	}
	if cfg.BodyHTML != "" {
		product["body_html"] = cfg.BodyHTML
	}
	if cfg.Vendor != "" {
		product["vendor"] = cfg.Vendor
	}
	if cfg.ProductType != "" {
		product["product_type"] = cfg.ProductType
	}
	if cfg.Status != "" {
		product["status"] = cfg.Status
	}
	if cfg.Tags != "" {
		product["tags"] = cfg.Tags
	}
	if len(cfg.Variants) > 0 {
		product["variants"] = cfg.Variants
	}
	if len(cfg.Images) > 0 {
		product["images"] = cfg.Images
	}

	body := map[string]interface{}{"product": product}
	endpoint := fmt.Sprintf("/admin/api/2024-01/products/%s.json", cfg.ProductID)
	respBody, err := client.doRequest(ctx, "PUT", endpoint, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Product map[string]interface{} `json:"product"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"product": result.Product,
			"success": true,
		},
	}, nil
}

func (e *ShopifyProductExecutor) deleteProduct(ctx context.Context, client *ShopifyClient, productID string) (*executor.StepResult, error) {
	if productID == "" {
		return nil, fmt.Errorf("productId is required for delete action")
	}

	endpoint := fmt.Sprintf("/admin/api/2024-01/products/%s.json", productID)
	_, err := client.doRequest(ctx, "DELETE", endpoint, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"message": "Product deleted successfully",
		},
	}, nil
}

// ============== Shopify Order Executor ==============

type ShopifyOrderExecutor struct{}

type ShopifyOrderConfig struct {
	StoreURL  string `json:"storeUrl" description:"Shopify store URL"`
	APIKey    string `json:"apiKey" description:"Shopify API key"`
	APISecret string `json:"apiSecret" description:"Shopify API secret/token"`
	Action    string `json:"action" options:"list,get,update,fulfill" description:"Order action to perform"`
	OrderID   string `json:"orderId" description:"Order ID (for get/update)"`
	OrderName string `json:"orderName" description:"Order name/number (for get)"`
	Status    string `json:"status" options:"open,closed,any,archived" description:"Filter by status (for list)"`
	Limit     int    `json:"limit" default:"50" description:"Number of orders to return"`
	Note      string `json:"note" description:"Order note (for update)"`
	Tags      string `json:"tags" description:"Order tags (for update)"`
}

var ShopifyOrderSchema = resolver.NewSchemaBuilder("shopify-order").
	WithName("Shopify Order").
	WithCategory("ecommerce").
	WithIcon("shopping-cart").
	WithDescription("Manage Shopify orders - list, get, update, fulfill").
	AddSection("Connection").
		AddTextField("storeUrl", "Store URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://mystore.myshopify.com"),
		).
		AddTextField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		AddTextField("apiSecret", "API Secret",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Orders", Value: "list", Icon: "list"},
			{Label: "Get Order", Value: "get", Icon: "eye"},
			{Label: "Update Order", Value: "update", Icon: "edit"},
			{Label: "Fulfill Order", Value: "fulfill", Icon: "truck"},
		}, resolver.WithDefault("list")).
		AddTextField("orderId", "Order ID",
			resolver.WithHint("Required for get/update/fulfill actions"),
		).
		AddTextField("orderName", "Order Name",
			resolver.WithHint("Order name/number for lookup"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
		).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "Open", Value: "open"},
			{Label: "Closed", Value: "closed"},
			{Label: "Any", Value: "any"},
			{Label: "Archived", Value: "archived"},
		}, resolver.WithDefault("open")).
		EndSection().
	AddSection("Update Details").
		AddTextareaField("note", "Note",
			resolver.WithRows(3),
			resolver.WithHint("Add a note to the order"),
		).
		AddTextField("tags", "Tags",
			resolver.WithHint("Comma-separated tags"),
		).
		EndSection().
	Build()

func (e *ShopifyOrderExecutor) Type() string { return "shopify-order" }

func (e *ShopifyOrderExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ShopifyOrderConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.StoreURL == "" {
		return nil, fmt.Errorf("storeUrl is required")
	}
	if cfg.APISecret == "" {
		return nil, fmt.Errorf("apiSecret is required")
	}

	client := NewShopifyClient(cfg.StoreURL, cfg.APIKey, cfg.APISecret)

	switch cfg.Action {
	case "list":
		return e.listOrders(ctx, client, cfg.Status, cfg.Limit)
	case "get":
		return e.getOrder(ctx, client, cfg.OrderID, cfg.OrderName)
	case "update":
		return e.updateOrder(ctx, client, cfg)
	case "fulfill":
		return e.fulfillOrder(ctx, client, cfg.OrderID)
	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}
}

func (e *ShopifyOrderExecutor) listOrders(ctx context.Context, client *ShopifyClient, status string, limit int) (*executor.StepResult, error) {
	if status == "" {
		status = "open"
	}
	endpoint := fmt.Sprintf("/admin/api/2024-01/orders.json?status=%s&limit=%d", status, limit)
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Orders []map[string]interface{} `json:"orders"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"orders": result.Orders,
			"count":  len(result.Orders),
		},
	}, nil
}

func (e *ShopifyOrderExecutor) getOrder(ctx context.Context, client *ShopifyClient, orderID, orderName string) (*executor.StepResult, error) {
	var endpoint string
	if orderID != "" {
		endpoint = fmt.Sprintf("/admin/api/2024-01/orders/%s.json", orderID)
	} else if orderName != "" {
		endpoint = fmt.Sprintf("/admin/api/2024-01/orders/name:%s.json", orderName)
	} else {
		return nil, fmt.Errorf("orderId or orderName is required for get action")
	}

	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Order map[string]interface{} `json:"order"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"order": result.Order,
		},
	}, nil
}

func (e *ShopifyOrderExecutor) updateOrder(ctx context.Context, client *ShopifyClient, cfg ShopifyOrderConfig) (*executor.StepResult, error) {
	if cfg.OrderID == "" {
		return nil, fmt.Errorf("orderId is required for update action")
	}

	order := map[string]interface{}{"id": cfg.OrderID}
	if cfg.Note != "" {
		order["note"] = cfg.Note
	}
	if cfg.Tags != "" {
		order["tags"] = cfg.Tags
	}

	body := map[string]interface{}{"order": order}
	endpoint := fmt.Sprintf("/admin/api/2024-01/orders/%s.json", cfg.OrderID)
	respBody, err := client.doRequest(ctx, "PUT", endpoint, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Order map[string]interface{} `json:"order"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"order":   result.Order,
			"success": true,
		},
	}, nil
}

func (e *ShopifyOrderExecutor) fulfillOrder(ctx context.Context, client *ShopifyClient, orderID string) (*executor.StepResult, error) {
	if orderID == "" {
		return nil, fmt.Errorf("orderId is required for fulfill action")
	}

	// Get order first to get line items
	endpoint := fmt.Sprintf("/admin/api/2024-01/orders/%s.json", orderID)
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var orderResult struct {
		Order map[string]interface{} `json:"order"`
	}
	if err := json.Unmarshal(respBody, &orderResult); err != nil {
		return nil, fmt.Errorf("failed to parse order: %w", err)
	}

	// Create fulfillment for all line items
	lineItems, _ := orderResult.Order["line_items"].([]interface{})
	var lineItemsToFulfill []map[string]interface{}
	for _, item := range lineItems {
		if itemMap, ok := item.(map[string]interface{}); ok {
			if id, exists := itemMap["id"]; exists {
				lineItemsToFulfill = append(lineItemsToFulfill, map[string]interface{}{
					"id":       id,
					"quantity": 1,
				})
			}
		}
	}

	fulfillment := map[string]interface{}{
		"line_items": lineItemsToFulfill,
		"notify_customer": true,
	}
	body := map[string]interface{}{"fulfillment": fulfillment}
	endpoint = fmt.Sprintf("/admin/api/2024-01/orders/%s/fulfillments.json", orderID)
	respBody, err = client.doRequest(ctx, "POST", endpoint, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Fulfillment map[string]interface{} `json:"fulfillment"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"fulfillment": result.Fulfillment,
			"success":     true,
		},
	}, nil
}

// ============== Shopify Customer Executor ==============

type ShopifyCustomerExecutor struct{}

type ShopifyCustomerConfig struct {
	StoreURL  string `json:"storeUrl" description:"Shopify store URL"`
	APIKey    string `json:"apiKey" description:"Shopify API key"`
	APISecret string `json:"apiSecret" description:"Shopify API secret/token"`
	Action    string `json:"action" options:"list,get,create,update,delete" description:"Customer action to perform"`
	CustomerID string `json:"customerId" description:"Customer ID (for get/update/delete)"`
	Email     string `json:"email" description:"Customer email (for get/create/update)"`
	FirstName string `json:"firstName" description:"Customer first name (for create/update)"`
	LastName  string `json:"lastName" description:"Customer last name (for create/update)"`
	Phone     string `json:"phone" description:"Customer phone (for create/update)"`
	Tags      string `json:"tags" description:"Customer tags (for create/update)"`
	Note      string `json:"note" description:"Customer note (for create/update)"`
	Limit     int    `json:"limit" default:"50" description:"Number of customers to return"`
}

var ShopifyCustomerSchema = resolver.NewSchemaBuilder("shopify-customer").
	WithName("Shopify Customer").
	WithCategory("ecommerce").
	WithIcon("users").
	WithDescription("Manage Shopify customers - list, get, create, update, delete").
	AddSection("Connection").
		AddTextField("storeUrl", "Store URL",
			resolver.WithRequired(),
		).
		AddTextField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		AddTextField("apiSecret", "API Secret",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Customers", Value: "list", Icon: "list"},
			{Label: "Get Customer", Value: "get", Icon: "eye"},
			{Label: "Create Customer", Value: "create", Icon: "plus"},
			{Label: "Update Customer", Value: "update", Icon: "edit"},
			{Label: "Delete Customer", Value: "delete", Icon: "trash"},
		}, resolver.WithDefault("list")).
		AddTextField("customerId", "Customer ID",
			resolver.WithHint("Required for get/update/delete actions"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
		).
		EndSection().
	AddSection("Customer Details").
		AddTextField("email", "Email",
			resolver.WithHint("Required for create, used for lookup by email"),
		).
		AddTextField("firstName", "First Name",
			resolver.WithHint("Required for create"),
		).
		AddTextField("lastName", "Last Name",
			resolver.WithHint("Customer last name"),
		).
		AddTextField("phone", "Phone",
			resolver.WithHint("Customer phone number"),
		).
		AddTextField("tags", "Tags",
			resolver.WithHint("Comma-separated tags"),
		).
		AddTextareaField("note", "Note",
			resolver.WithRows(3),
			resolver.WithHint("Internal note about customer"),
		).
		EndSection().
	Build()

func (e *ShopifyCustomerExecutor) Type() string { return "shopify-customer" }

func (e *ShopifyCustomerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ShopifyCustomerConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.StoreURL == "" {
		return nil, fmt.Errorf("storeUrl is required")
	}
	if cfg.APISecret == "" {
		return nil, fmt.Errorf("apiSecret is required")
	}

	client := NewShopifyClient(cfg.StoreURL, cfg.APIKey, cfg.APISecret)

	switch cfg.Action {
	case "list":
		return e.listCustomers(ctx, client, cfg.Limit)
	case "get":
		return e.getCustomer(ctx, client, cfg.CustomerID, cfg.Email)
	case "create":
		return e.createCustomer(ctx, client, cfg)
	case "update":
		return e.updateCustomer(ctx, client, cfg)
	case "delete":
		return e.deleteCustomer(ctx, client, cfg.CustomerID)
	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}
}

func (e *ShopifyCustomerExecutor) listCustomers(ctx context.Context, client *ShopifyClient, limit int) (*executor.StepResult, error) {
	endpoint := fmt.Sprintf("/admin/api/2024-01/customers.json?limit=%d", limit)
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Customers []map[string]interface{} `json:"customers"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"customers": result.Customers,
			"count":     len(result.Customers),
		},
	}, nil
}

func (e *ShopifyCustomerExecutor) getCustomer(ctx context.Context, client *ShopifyClient, customerID, email string) (*executor.StepResult, error) {
	var endpoint string
	if customerID != "" {
		endpoint = fmt.Sprintf("/admin/api/2024-01/customers/%s.json", customerID)
	} else if email != "" {
		endpoint = fmt.Sprintf("/admin/api/2024-01/customers/search.json?query=email:%s", email)
	} else {
		return nil, fmt.Errorf("customerId or email is required for get action")
	}

	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	// Handle search response vs direct get
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var customer map[string]interface{}
	if customers, ok := result["customers"].([]interface{}); ok && len(customers) > 0 {
		if c, ok := customers[0].(map[string]interface{}); ok {
			customer = c
		}
	} else if c, ok := result["customer"].(map[string]interface{}); ok {
		customer = c
	}

	if customer == nil {
		return nil, fmt.Errorf("customer not found")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"customer": customer,
		},
	}, nil
}

func (e *ShopifyCustomerExecutor) createCustomer(ctx context.Context, client *ShopifyClient, cfg ShopifyCustomerConfig) (*executor.StepResult, error) {
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required for create action")
	}

	customer := map[string]interface{}{
		"email": cfg.Email,
	}
	if cfg.FirstName != "" {
		customer["first_name"] = cfg.FirstName
	}
	if cfg.LastName != "" {
		customer["last_name"] = cfg.LastName
	}
	if cfg.Phone != "" {
		customer["phone"] = cfg.Phone
	}
	if cfg.Tags != "" {
		customer["tags"] = cfg.Tags
	}
	if cfg.Note != "" {
		customer["note"] = cfg.Note
	}

	body := map[string]interface{}{"customer": customer}
	respBody, err := client.doRequest(ctx, "POST", "/admin/api/2024-01/customers.json", body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Customer map[string]interface{} `json:"customer"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"customer": result.Customer,
			"success":  true,
		},
	}, nil
}

func (e *ShopifyCustomerExecutor) updateCustomer(ctx context.Context, client *ShopifyClient, cfg ShopifyCustomerConfig) (*executor.StepResult, error) {
	if cfg.CustomerID == "" {
		return nil, fmt.Errorf("customerId is required for update action")
	}

	customer := map[string]interface{}{"id": cfg.CustomerID}
	if cfg.Email != "" {
		customer["email"] = cfg.Email
	}
	if cfg.FirstName != "" {
		customer["first_name"] = cfg.FirstName
	}
	if cfg.LastName != "" {
		customer["last_name"] = cfg.LastName
	}
	if cfg.Phone != "" {
		customer["phone"] = cfg.Phone
	}
	if cfg.Tags != "" {
		customer["tags"] = cfg.Tags
	}
	if cfg.Note != "" {
		customer["note"] = cfg.Note
	}

	body := map[string]interface{}{"customer": customer}
	endpoint := fmt.Sprintf("/admin/api/2024-01/customers/%s.json", cfg.CustomerID)
	respBody, err := client.doRequest(ctx, "PUT", endpoint, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Customer map[string]interface{} `json:"customer"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"customer": result.Customer,
			"success":  true,
		},
	}, nil
}

func (e *ShopifyCustomerExecutor) deleteCustomer(ctx context.Context, client *ShopifyClient, customerID string) (*executor.StepResult, error) {
	if customerID == "" {
		return nil, fmt.Errorf("customerId is required for delete action")
	}

	endpoint := fmt.Sprintf("/admin/api/2024-01/customers/%s.json", customerID)
	_, err := client.doRequest(ctx, "DELETE", endpoint, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"message": "Customer deleted successfully",
		},
	}, nil
}

// ============== Shopify Inventory Executor ==============

type ShopifyInventoryExecutor struct{}

type ShopifyInventoryConfig struct {
	StoreURL       string `json:"storeUrl" description:"Shopify store URL"`
	APIKey         string `json:"apiKey" description:"Shopify API key"`
	APISecret      string `json:"apiSecret" description:"Shopify API secret/token"`
	Action         string `json:"action" options:"list,get,set,adjust" description:"Inventory action to perform"`
	InventoryItemID string `json:"inventoryItemId" description:"Inventory item ID"`
	LocationID     string `json:"locationId" description:"Location ID"`
	Quantity       int    `json:"quantity" description:"Quantity to set or adjust"`
	Available      int    `json:"available" description:"Available quantity to set"`
}

var ShopifyInventorySchema = resolver.NewSchemaBuilder("shopify-inventory").
	WithName("Shopify Inventory").
	WithCategory("ecommerce").
	WithIcon("package").
	WithDescription("Manage Shopify inventory levels").
	AddSection("Connection").
		AddTextField("storeUrl", "Store URL",
			resolver.WithRequired(),
		).
		AddTextField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		AddTextField("apiSecret", "API Secret",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Inventory", Value: "list", Icon: "list"},
			{Label: "Get Inventory", Value: "get", Icon: "eye"},
			{Label: "Set Quantity", Value: "set", Icon: "edit"},
			{Label: "Adjust Quantity", Value: "adjust", Icon: "trending-up"},
		}, resolver.WithDefault("list")).
		AddTextField("inventoryItemId", "Inventory Item ID",
			resolver.WithHint("Required for get/set/adjust actions"),
		).
		AddTextField("locationId", "Location ID",
			resolver.WithHint("Required for set/adjust actions"),
		).
		AddNumberField("quantity", "Quantity",
			resolver.WithHint("Quantity for set/adjust actions"),
		).
		EndSection().
	Build()

func (e *ShopifyInventoryExecutor) Type() string { return "shopify-inventory" }

func (e *ShopifyInventoryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ShopifyInventoryConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.StoreURL == "" {
		return nil, fmt.Errorf("storeUrl is required")
	}
	if cfg.APISecret == "" {
		return nil, fmt.Errorf("apiSecret is required")
	}

	client := NewShopifyClient(cfg.StoreURL, cfg.APIKey, cfg.APISecret)

	switch cfg.Action {
	case "list":
		return e.listInventory(ctx, client)
	case "get":
		return e.getInventory(ctx, client, cfg.InventoryItemID)
	case "set":
		return e.setInventory(ctx, client, cfg)
	case "adjust":
		return e.adjustInventory(ctx, client, cfg)
	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}
}

func (e *ShopifyInventoryExecutor) listInventory(ctx context.Context, client *ShopifyClient) (*executor.StepResult, error) {
	endpoint := "/admin/api/2024-01/inventory_levels.json?limit=250"
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		InventoryLevels []map[string]interface{} `json:"inventory_levels"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"inventory_levels": result.InventoryLevels,
			"count":            len(result.InventoryLevels),
		},
	}, nil
}

func (e *ShopifyInventoryExecutor) getInventory(ctx context.Context, client *ShopifyClient, inventoryItemID string) (*executor.StepResult, error) {
	if inventoryItemID == "" {
		return nil, fmt.Errorf("inventoryItemId is required for get action")
	}

	endpoint := fmt.Sprintf("/admin/api/2024-01/inventory_levels.json?inventory_item_ids=%s", inventoryItemID)
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		InventoryLevels []map[string]interface{} `json:"inventory_levels"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"inventory_levels": result.InventoryLevels,
			"count":            len(result.InventoryLevels),
		},
	}, nil
}

func (e *ShopifyInventoryExecutor) setInventory(ctx context.Context, client *ShopifyClient, cfg ShopifyInventoryConfig) (*executor.StepResult, error) {
	if cfg.InventoryItemID == "" || cfg.LocationID == "" {
		return nil, fmt.Errorf("inventoryItemId and locationId are required for set action")
	}

	body := map[string]interface{}{
		"inventory_item_id": cfg.InventoryItemID,
		"location_id":       cfg.LocationID,
		"available":         cfg.Quantity,
	}

	respBody, err := client.doRequest(ctx, "PUT", "/admin/api/2024-01/inventory_levels/set.json", body)
	if err != nil {
		return nil, err
	}

	var result struct {
		InventoryLevel map[string]interface{} `json:"inventory_level"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"inventory_level": result.InventoryLevel,
			"success":         true,
		},
	}, nil
}

func (e *ShopifyInventoryExecutor) adjustInventory(ctx context.Context, client *ShopifyClient, cfg ShopifyInventoryConfig) (*executor.StepResult, error) {
	if cfg.InventoryItemID == "" || cfg.LocationID == "" {
		return nil, fmt.Errorf("inventoryItemId and locationId are required for adjust action")
	}

	body := map[string]interface{}{
		"inventory_item_id": cfg.InventoryItemID,
		"location_id":       cfg.LocationID,
		"available_adjustment": cfg.Quantity,
	}

	respBody, err := client.doRequest(ctx, "POST", "/admin/api/2024-01/inventory_levels/adjust.json", body)
	if err != nil {
		return nil, err
	}

	var result struct {
		InventoryLevel map[string]interface{} `json:"inventory_level"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"inventory_level": result.InventoryLevel,
			"success":         true,
		},
	}, nil
}

// ============== Shopify Fulfillment Executor ==============

type ShopifyFulfillmentExecutor struct{}

type ShopifyFulfillmentConfig struct {
	StoreURL    string `json:"storeUrl" description:"Shopify store URL"`
	APIKey      string `json:"apiKey" description:"Shopify API key"`
	APISecret   string `json:"apiSecret" description:"Shopify API secret/token"`
	Action      string `json:"action" options:"list,get,create,update,track" description:"Fulfillment action to perform"`
	OrderID     string `json:"orderId" description:"Order ID"`
	FulfillmentID string `json:"fulfillmentId" description:"Fulfillment ID (for get/update)"`
	TrackingCompany string `json:"trackingCompany" description:"Tracking company name"`
	TrackingNumber  string `json:"trackingNumber" description:"Tracking number"`
	TrackingURL     string `json:"trackingUrl" description:"Tracking URL"`
	NotifyCustomer  bool   `json:"notifyCustomer" default:"true" description:"Notify customer of fulfillment"`
	LineItems       []map[string]interface{} `json:"lineItems" description:"Line items to fulfill"`
}

var ShopifyFulfillmentSchema = resolver.NewSchemaBuilder("shopify-fulfillment").
	WithName("Shopify Fulfillment").
	WithCategory("ecommerce").
	WithIcon("truck").
	WithDescription("Manage Shopify order fulfillments").
	AddSection("Connection").
		AddTextField("storeUrl", "Store URL",
			resolver.WithRequired(),
		).
		AddTextField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		AddTextField("apiSecret", "API Secret",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Fulfillments", Value: "list", Icon: "list"},
			{Label: "Get Fulfillment", Value: "get", Icon: "eye"},
			{Label: "Create Fulfillment", Value: "create", Icon: "plus"},
			{Label: "Update Fulfillment", Value: "update", Icon: "edit"},
			{Label: "Add Tracking", Value: "track", Icon: "map-pin"},
		}, resolver.WithDefault("list")).
		AddTextField("orderId", "Order ID",
			resolver.WithRequired(),
			resolver.WithHint("Order ID for all fulfillment actions"),
		).
		AddTextField("fulfillmentId", "Fulfillment ID",
			resolver.WithHint("Required for get/update actions"),
		).
		AddToggleField("notifyCustomer", "Notify Customer",
			resolver.WithDefault(true),
		).
		EndSection().
	AddSection("Tracking Info").
		AddTextField("trackingCompany", "Tracking Company",
			resolver.WithHint("e.g., UPS, FedEx, USPS"),
		).
		AddTextField("trackingNumber", "Tracking Number",
			resolver.WithHint("Package tracking number"),
		).
		AddTextField("trackingUrl", "Tracking URL",
			resolver.WithHint("URL to track package"),
		).
		AddJSONField("lineItems", "Line Items",
			resolver.WithHeight(100),
			resolver.WithHint("Array of line items to fulfill"),
		).
		EndSection().
	Build()

func (e *ShopifyFulfillmentExecutor) Type() string { return "shopify-fulfillment" }

func (e *ShopifyFulfillmentExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ShopifyFulfillmentConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.StoreURL == "" {
		return nil, fmt.Errorf("storeUrl is required")
	}
	if cfg.APISecret == "" {
		return nil, fmt.Errorf("apiSecret is required")
	}

	client := NewShopifyClient(cfg.StoreURL, cfg.APIKey, cfg.APISecret)

	switch cfg.Action {
	case "list":
		return e.listFulfillments(ctx, client, cfg.OrderID)
	case "get":
		return e.getFulfillment(ctx, client, cfg.OrderID, cfg.FulfillmentID)
	case "create":
		return e.createFulfillment(ctx, client, cfg)
	case "update":
		return e.updateFulfillment(ctx, client, cfg)
	case "track":
		return e.addTracking(ctx, client, cfg)
	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}
}

func (e *ShopifyFulfillmentExecutor) listFulfillments(ctx context.Context, client *ShopifyClient, orderID string) (*executor.StepResult, error) {
	if orderID == "" {
		return nil, fmt.Errorf("orderId is required for list action")
	}

	endpoint := fmt.Sprintf("/admin/api/2024-01/orders/%s/fulfillments.json", orderID)
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Fulfillments []map[string]interface{} `json:"fulfillments"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"fulfillments": result.Fulfillments,
			"count":        len(result.Fulfillments),
		},
	}, nil
}

func (e *ShopifyFulfillmentExecutor) getFulfillment(ctx context.Context, client *ShopifyClient, orderID, fulfillmentID string) (*executor.StepResult, error) {
	if orderID == "" || fulfillmentID == "" {
		return nil, fmt.Errorf("orderId and fulfillmentId are required for get action")
	}

	endpoint := fmt.Sprintf("/admin/api/2024-01/orders/%s/fulfillments/%s.json", orderID, fulfillmentID)
	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Fulfillment map[string]interface{} `json:"fulfillment"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"fulfillment": result.Fulfillment,
		},
	}, nil
}

func (e *ShopifyFulfillmentExecutor) createFulfillment(ctx context.Context, client *ShopifyClient, cfg ShopifyFulfillmentConfig) (*executor.StepResult, error) {
	if cfg.OrderID == "" {
		return nil, fmt.Errorf("orderId is required for create action")
	}

	fulfillment := map[string]interface{}{
		"notify_customer": cfg.NotifyCustomer,
	}

	if len(cfg.LineItems) > 0 {
		fulfillment["line_items"] = cfg.LineItems
	}

	if cfg.TrackingNumber != "" {
		fulfillment["tracking_info"] = map[string]interface{}{
			"number": cfg.TrackingNumber,
		}
		if cfg.TrackingCompany != "" {
			fulfillment["tracking_info"].(map[string]interface{})["company"] = cfg.TrackingCompany
		}
		if cfg.TrackingURL != "" {
			fulfillment["tracking_info"].(map[string]interface{})["url"] = cfg.TrackingURL
		}
	}

	body := map[string]interface{}{"fulfillment": fulfillment}
	endpoint := fmt.Sprintf("/admin/api/2024-01/orders/%s/fulfillments.json", cfg.OrderID)
	respBody, err := client.doRequest(ctx, "POST", endpoint, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Fulfillment map[string]interface{} `json:"fulfillment"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"fulfillment": result.Fulfillment,
			"success":     true,
		},
	}, nil
}

func (e *ShopifyFulfillmentExecutor) updateFulfillment(ctx context.Context, client *ShopifyClient, cfg ShopifyFulfillmentConfig) (*executor.StepResult, error) {
	if cfg.OrderID == "" || cfg.FulfillmentID == "" {
		return nil, fmt.Errorf("orderId and fulfillmentId are required for update action")
	}

	fulfillment := map[string]interface{}{}
	if cfg.TrackingNumber != "" {
		fulfillment["tracking_info"] = map[string]interface{}{
			"number": cfg.TrackingNumber,
		}
		if cfg.TrackingCompany != "" {
			fulfillment["tracking_info"].(map[string]interface{})["company"] = cfg.TrackingCompany
		}
		if cfg.TrackingURL != "" {
			fulfillment["tracking_info"].(map[string]interface{})["url"] = cfg.TrackingURL
		}
	}

	body := map[string]interface{}{"fulfillment": fulfillment}
	endpoint := fmt.Sprintf("/admin/api/2024-01/orders/%s/fulfillments/%s.json", cfg.OrderID, cfg.FulfillmentID)
	respBody, err := client.doRequest(ctx, "PUT", endpoint, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Fulfillment map[string]interface{} `json:"fulfillment"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"fulfillment": result.Fulfillment,
			"success":     true,
		},
	}, nil
}

func (e *ShopifyFulfillmentExecutor) addTracking(ctx context.Context, client *ShopifyClient, cfg ShopifyFulfillmentConfig) (*executor.StepResult, error) {
	if cfg.OrderID == "" || cfg.FulfillmentID == "" {
		return nil, fmt.Errorf("orderId and fulfillmentId are required for track action")
	}

	trackingInfo := map[string]interface{}{}
	if cfg.TrackingNumber != "" {
		trackingInfo["number"] = cfg.TrackingNumber
	}
	if cfg.TrackingCompany != "" {
		trackingInfo["company"] = cfg.TrackingCompany
	}
	if cfg.TrackingURL != "" {
		trackingInfo["url"] = cfg.TrackingURL
	}

	body := map[string]interface{}{
		"tracking_info": trackingInfo,
		"notify_customer": cfg.NotifyCustomer,
	}

	endpoint := fmt.Sprintf("/admin/api/2024-01/orders/%s/fulfillments/%s/track.json", cfg.OrderID, cfg.FulfillmentID)
	respBody, err := client.doRequest(ctx, "POST", endpoint, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Fulfillment map[string]interface{} `json:"fulfillment"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"fulfillment": result.Fulfillment,
			"success":     true,
		},
	}, nil
}
