# Shopify Skill

Shopify e-commerce operations skill for Atlas agents. This skill enables automation of Shopify store operations including product management, order processing, customer management, inventory tracking, and fulfillment.

## Features

- **Product Management**: List, get, create, update, and delete products
- **Order Management**: List, get, update, and fulfill orders
- **Customer Management**: List, get, create, update, and delete customers
- **Inventory Management**: List, get, set, and adjust inventory levels
- **Fulfillment Management**: List, get, create, update fulfillments and add tracking

## Installation

### Build from Source

```bash
go build -o skill-shopify .
```

### Build with Docker

```bash
docker build -t skill-shopify .
```

## Configuration

### Required Credentials

To use this skill, you need the following Shopify credentials:

| Parameter | Description |
|-----------|-------------|
| `storeUrl` | Your Shopify store URL (e.g., `https://mystore.myshopify.com`) |
| `apiKey` | Shopify API key from admin panel |
| `apiSecret` | Shopify API access token |

### Getting Shopify API Credentials

1. Log in to your Shopify admin panel
2. Go to **Apps** > **Develop apps**
3. Click **Create an app**
4. Configure the required API scopes:
   - `read_products`, `write_products`
   - `read_orders`, `write_orders`
   - `read_customers`, `write_customers`
   - `read_inventory`, `write_inventory`
   - `read_fulfillments`, `write_fulfillments`
5. Install the app and copy the Admin API access token

## Node Types

### shopify-product

Manage Shopify products.

**Actions:**
- `list` - List products with optional limit
- `get` - Get a specific product by ID
- `create` - Create a new product
- `update` - Update an existing product
- `delete` - Delete a product

**Example Configuration:**
```yaml
nodeType: shopify-product
config:
  storeUrl: https://mystore.myshopify.com
  apiKey: ${{ secrets.SHOPIFY_API_KEY }}
  apiSecret: ${{ secrets.SHOPIFY_API_SECRET }}
  action: list
  limit: 50
```

### shopify-order

Manage Shopify orders.

**Actions:**
- `list` - List orders with status filter
- `get` - Get a specific order by ID or name
- `update` - Update order details (note, tags)
- `fulfill` - Create fulfillment for an order

**Example Configuration:**
```yaml
nodeType: shopify-order
config:
  storeUrl: https://mystore.myshopify.com
  apiKey: ${{ secrets.SHOPIFY_API_KEY }}
  apiSecret: ${{ secrets.SHOPIFY_API_SECRET }}
  action: list
  status: open
  limit: 50
```

### shopify-customer

Manage Shopify customers.

**Actions:**
- `list` - List customers
- `get` - Get a customer by ID or email
- `create` - Create a new customer
- `update` - Update customer details
- `delete` - Delete a customer

**Example Configuration:**
```yaml
nodeType: shopify-customer
config:
  storeUrl: https://mystore.myshopify.com
  apiKey: ${{ secrets.SHOPIFY_API_KEY }}
  apiSecret: ${{ secrets.SHOPIFY_API_SECRET }}
  action: get
  email: customer@example.com
```

### shopify-inventory

Manage Shopify inventory levels.

**Actions:**
- `list` - List all inventory levels
- `get` - Get inventory for specific item
- `set` - Set inventory quantity at location
- `adjust` - Adjust inventory quantity

**Example Configuration:**
```yaml
nodeType: shopify-inventory
config:
  storeUrl: https://mystore.myshopify.com
  apiKey: ${{ secrets.SHOPIFY_API_KEY }}
  apiSecret: ${{ secrets.SHOPIFY_API_SECRET }}
  action: set
  inventoryItemId: "123456789"
  locationId: "987654321"
  quantity: 100
```

### shopify-fulfillment

Manage Shopify order fulfillments.

**Actions:**
- `list` - List fulfillments for an order
- `get` - Get specific fulfillment
- `create` - Create new fulfillment
- `update` - Update fulfillment
- `track` - Add tracking information

**Example Configuration:**
```yaml
nodeType: shopify-fulfillment
config:
  storeUrl: https://mystore.myshopify.com
  apiKey: ${{ secrets.SHOPIFY_API_KEY }}
  apiSecret: ${{ secrets.SHOPIFY_API_SECRET }}
  action: create
  orderId: "123456789"
  notifyCustomer: true
  trackingCompany: UPS
  trackingNumber: "1Z999AA10123456784"
```

## Output Format

All executors return a `StepResult` with an `Output` map containing:

- `success` - Boolean indicating operation success
- `product`, `order`, `customer`, `fulfillment`, `inventory_level` - The returned Shopify object
- `count` - Number of items returned (for list operations)
- `message` - Additional information or error messages

## Security Best Practices

1. **Use Secrets Management**: Store API credentials in a secure secrets manager
2. **Limit API Scopes**: Only request the minimum required API permissions
3. **Rotate Credentials**: Regularly rotate API tokens
4. **Use Private Apps**: Create private apps for server-to-server communication

## Error Handling

The skill returns descriptive error messages for common issues:

- Missing required parameters
- Invalid API credentials
- Network connectivity issues
- Shopify API rate limits
- Resource not found

## Rate Limiting

Shopify API has rate limits. The skill does not implement automatic retry logic, so consider implementing retry mechanisms in your workflow for production use.

## License

MIT License - See LICENSE file for details.

## Support

For issues and feature requests, please open an issue on the GitHub repository.

---

**Author**: Axiom Studio  
**Email**: engineering@axiomstudio.ai  
**Version**: 1.0.0
