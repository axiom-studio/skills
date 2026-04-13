# Braintree Skill

Braintree payment processing for Atlas agents. Process transactions, manage customers, and handle subscriptions.

## Overview

The Braintree skill provides comprehensive node types for Braintree payment processing. It supports transaction operations, customer management, and subscription handling.

## Node Types

### `braintree-transaction-sale`

Process a sale transaction.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| environment | string | Yes | Environment (sandbox, production) |
| merchantId | string | Yes | Merchant ID |
| publicKey | string | Yes | Public key |
| privateKey | string | Yes | Private key |
| amount | string | Yes | Transaction amount |
| paymentMethodNonce | string | Yes | Payment method nonce |
| orderId | string | No | Order ID |

**Output:**

```json
{
  "success": true,
  "transactionId": "abc123",
  "status": "submitted_for_settlement",
  "amount": "99.99",
  "currencyIsoCode": "USD"
}
```

### `braintree-transaction-void`

Void a transaction.

### `braintree-transaction-refund`

Refund a transaction.

### `braintree-transaction-get`

Get transaction details.

### `braintree-customer-create`

Create a customer.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| environment | string | Yes | Environment |
| merchantId | string | Yes | Merchant ID |
| publicKey | string | Yes | Public key |
| privateKey | string | Yes | Private key |
| firstName | string | No | First name |
| lastName | string | No | Last name |
| email | string | No | Email |
| phone | string | No | Phone |

### `braintree-customer-list`

List customers.

### `braintree-subscription-create`

Create a subscription.

### `braintree-subscription-cancel`

Cancel a subscription.

## Authentication

Get credentials from Braintree Control Panel.

## Usage Examples

```yaml
# Process sale
- type: braintree-transaction-sale
  config:
    environment: "sandbox"
    merchantId: "{{secrets.braintree.merchantId}}"
    publicKey: "{{secrets.braintree.publicKey}}"
    privateKey: "{{secrets.braintree.privateKey}}"
    amount: "99.99"
    paymentMethodNonce: "nonce-from-client"

# Create customer
- type: braintree-customer-create
  config:
    environment: "sandbox"
    merchantId: "{{secrets.braintree.merchantId}}"
    publicKey: "{{secrets.braintree.publicKey}}"
    privateKey: "{{secrets.braintree.privateKey}}"
    firstName: "John"
    lastName: "Doe"
    email: "john@example.com"
```

## License

MIT License - See [LICENSE](LICENSE) for details.