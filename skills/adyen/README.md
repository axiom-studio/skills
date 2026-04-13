# Adyen Skill

Adyen payment platform operations for Atlas agents. Process payments, captures, refunds, and manage payouts.

## Overview

The Adyen skill provides comprehensive node types for Adyen payment platform. It supports payment processing, captures, refunds, cancellations, and payout operations.

## Node Types

### `adyen-payment`

Process a payment.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Adyen API key |
| merchantAccount | string | Yes | Merchant account |
| amount | object | Yes | Amount (currency, value) |
| reference | string | Yes | Payment reference |
| paymentMethod | object | Yes | Payment method details |
| shopperEmail | string | No | Shopper email |
| shopperReference | string | No | Shopper reference |

**Output:**

```json
{
  "success": true,
  "pspReference": "8816178952380553",
  "resultCode": "Authorised",
  "merchantReference": "order-123"
}
```

### `adyen-capture`

Capture a payment.

### `adyen-refund`

Refund a payment.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Adyen API key |
| merchantAccount | string | Yes | Merchant account |
| pspReference | string | Yes | Original payment reference |
| amount | object | No | Refund amount |
| reference | string | No | Refund reference |

### `adyen-cancel`

Cancel a payment.

### `adyen-payment-list`

List payments.

### `adyen-payment-get`

Get payment details.

### `adyen-payout`

Process a payout.

### `adyen-3ds-authenticate`

Authenticate with 3D Secure.

## Authentication

Get API key from Adyen Customer Area > Developers > API credentials.

## Usage Examples

```yaml
# Process payment
- type: adyen-payment
  config:
    apiKey: "{{secrets.adyen.apiKey}}"
    merchantAccount: "MyMerchantAccount"
    amount:
      currency: "USD"
      value: 9999
    reference: "order-123"
    paymentMethod:
      type: "scheme"
      number: "4111111111111111"
      expiryMonth: "03"
      expiryYear: "2030"
      holderName: "John Doe"
      cvc: "737"
    shopperEmail: "john@example.com"

# Refund
- type: adyen-refund
  config:
    apiKey: "{{secrets.adyen.apiKey}}"
    merchantAccount: "MyMerchantAccount"
    pspReference: "8816178952380553"
    amount:
      currency: "USD"
      value: 9999
    reference: "refund-order-123"
```

## License

MIT License - See [LICENSE](LICENSE) for details.