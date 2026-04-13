# PayPal Skill

PayPal payment operations for Atlas agents. Process payments, manage orders, handle refunds, and payouts.

## Overview

The PayPal skill provides comprehensive node types for PayPal payment processing. It supports order creation, payment capture, refunds, payouts, and transaction management.

## Node Types

### `paypal-order-create`

Create a new PayPal order.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| clientId | string | Yes | PayPal client ID |
| clientSecret | string | Yes | PayPal client secret |
| sandbox | boolean | No | Use sandbox (default: false) |
| intent | string | No | Order intent (CAPTURE, AUTHORIZE) |
| amount | string | Yes | Amount (e.g., "99.99") |
| currency | string | No | Currency code (default: USD) |
| description | string | No | Order description |

**Output:**

```json
{
  "success": true,
  "orderId": "5O190127TN364715T",
  "status": "CREATED",
  "approveUrl": "https://www.paypal.com/checkoutnow?token=..."
}
```

### `paypal-order-capture`

Capture a PayPal order.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| clientId | string | Yes | PayPal client ID |
| clientSecret | string | Yes | PayPal client secret |
| sandbox | boolean | No | Use sandbox |
| orderId | string | Yes | Order ID |

### `paypal-order-get`

Get order details.

### `paypal-payment-list`

List payments.

### `paypal-refund`

Process a refund.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| clientId | string | Yes | PayPal client ID |
| clientSecret | string | Yes | PayPal client secret |
| sandbox | boolean | No | Use sandbox |
| captureId | string | Yes | Capture ID to refund |
| amount | string | No | Refund amount |
| reason | string | No | Refund reason |

### `paypal-payout`

Create a payout.

### `paypal-transaction-list`

List transactions.

### `paypal-invoice-create`

Create an invoice.

## Authentication

Get credentials from PayPal Developer Dashboard.

**Required:**
- Client ID
- Client Secret

## Usage Examples

```yaml
# Create order
- type: paypal-order-create
  config:
    clientId: "{{secrets.paypal.clientId}}"
    clientSecret: "{{secrets.paypal.clientSecret}}"
    intent: "CAPTURE"
    amount: "99.99"
    currency: "USD"
    description: "Order #123"

# Capture order
- type: paypal-order-capture
  config:
    clientId: "{{secrets.paypal.clientId}}"
    clientSecret: "{{secrets.paypal.clientSecret}}"
    orderId: "5O190127TN364715T"

# Refund
- type: paypal-refund
  config:
    clientId: "{{secrets.paypal.clientId}}"
    clientSecret: "{{secrets.paypal.clientSecret}}"
    captureId: "abc123"
    amount: "50.00"
    reason: "Customer request"
```

## License

MIT License - See [LICENSE](LICENSE) for details.