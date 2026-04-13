# Azure Skill

Microsoft Azure cloud operations for Atlas agents. This skill enables AI agents to interact with Azure services including Virtual Machines, AKS (Azure Kubernetes Service), Storage Accounts, Blob Storage, Azure Functions, and Resource Groups.

## Overview

The Azure skill provides a comprehensive set of node types for managing Azure infrastructure. It supports common operations like listing, starting, stopping VMs, managing AKS clusters, uploading/downloading blobs, and invoking Azure Functions.

## Node Types

### `azure-vm-list`

List all virtual machines in a subscription or resource group.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | No | Filter by resource group name |

**Output:**

```json
{
  "vms": [
    {
      "name": "my-vm",
      "id": "/subscriptions/.../resourceGroups/myRG/providers/Microsoft.Compute/virtualMachines/my-vm",
      "location": "eastus",
      "powerState": "running",
      "vmSize": "Standard_DS1_v2",
      "osType": "Linux"
    }
  ],
  "count": 1
}
```

---

### `azure-vm-start`

Start a stopped virtual machine.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | Yes | Resource group name |
| vmName | string | Yes | Virtual machine name |

**Output:**

```json
{
  "success": true,
  "vmName": "my-vm",
  "status": "starting"
}
```

---

### `azure-vm-stop`

Stop a running virtual machine.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | Yes | Resource group name |
| vmName | string | Yes | Virtual machine name |
| skipShutdown | boolean | No | Skip graceful shutdown (default: false) |

**Output:**

```json
{
  "success": true,
  "vmName": "my-vm",
  "status": "stopping"
}
```

---

### `azure-vm-restart`

Restart a virtual machine.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | Yes | Resource group name |
| vmName | string | Yes | Virtual machine name |

---

### `azure-aks-list`

List all Azure Kubernetes Service clusters.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | No | Filter by resource group name |

**Output:**

```json
{
  "clusters": [
    {
      "name": "my-aks-cluster",
      "id": "/subscriptions/.../resourceGroups/myRG/providers/Microsoft.ContainerService/managedClusters/my-aks-cluster",
      "location": "eastus",
      "kubernetesVersion": "1.28",
      "provisioningState": "Succeeded",
      "nodeResourceGroup": "MC_myRG_my-aks-cluster_eastus"
    }
  ],
  "count": 1
}
```

---

### `azure-aks-get-credentials`

Get kubeconfig credentials for an AKS cluster.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | Yes | Resource group name |
| clusterName | string | Yes | AKS cluster name |

**Output:**

```json
{
  "success": true,
  "kubeconfig": "...",
  "message": "Credentials retrieved successfully"
}
```

---

### `azure-storage-list`

List all storage accounts.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | No | Filter by resource group name |

---

### `azure-blob-upload`

Upload a file to Azure Blob Storage.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| storageAccount | string | Yes | Storage account name |
| container | string | Yes | Container name |
| blobName | string | Yes | Destination blob name |
| content | string | Yes | File content (text or base64) |
| contentType | string | No | Content type (e.g., application/json) |

---

### `azure-blob-download`

Download a blob from Azure Blob Storage.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| storageAccount | string | Yes | Storage account name |
| container | string | Yes | Container name |
| blobName | string | Yes | Blob name to download |

---

### `azure-function-list`

List all Azure Functions in a subscription.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | No | Filter by resource group name |

---

### `azure-function-invoke`

Invoke an Azure Function.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| resourceGroup | string | Yes | Resource group name |
| functionName | string | Yes | Function app name |
| functionName | string | Yes | Function name |
| method | string | No | HTTP method (default: POST) |
| body | object | No | Request body |
| headers | object | No | Request headers |

---

### `azure-resource-group-list`

List all resource groups.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |

---

### `azure-resource-group-create`

Create a new resource group.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| subscriptionId | string | Yes | Azure subscription ID |
| clientId | string | Yes | Azure AD app client ID |
| clientSecret | string | Yes | Azure AD app client secret |
| tenantId | string | Yes | Azure AD tenant ID |
| name | string | Yes | Resource group name |
| location | string | Yes | Azure region (e.g., eastus, westus2) |
| tags | object | No | Tags to apply |

---

## Authentication

All node types require Azure AD service principal authentication with the following credentials:

1. **Subscription ID** - Your Azure subscription ID
2. **Client ID** - Azure AD application (service principal) client ID
3. **Client Secret** - Azure AD application client secret
4. **Tenant ID** - Azure AD tenant ID

### Creating a Service Principal

```bash
# Create service principal
az ad sp create-for-rbac --name "my-app" --role Contributor --scopes /subscriptions/<subscription-id>

# Output will contain:
# - appId (use as clientId)
# - password (use as clientSecret)
# - tenant (use as tenantId)
```

### Required Permissions

The service principal needs appropriate RBAC roles:
- **Contributor** - For managing resources
- **Reader** - For listing resources
- **Azure Kubernetes Service Cluster User** - For AKS credentials

## Usage Examples

### List VMs and Start a Stopped One

```yaml
# Step 1: List VMs
- type: azure-vm-list
  config:
    subscriptionId: "{{secrets.azure.subscriptionId}}"
    clientId: "{{secrets.azure.clientId}}"
    clientSecret: "{{secrets.azure.clientSecret}}"
    tenantId: "{{secrets.azure.tenantId}}"

# Step 2: Start a VM
- type: azure-vm-start
  config:
    subscriptionId: "{{secrets.azure.subscriptionId}}"
    clientId: "{{secrets.azure.clientId}}"
    clientSecret: "{{secrets.azure.clientSecret}}"
    tenantId: "{{secrets.azure.tenantId}}"
    resourceGroup: "my-resource-group"
    vmName: "my-vm"
```

### Upload a File to Blob Storage

```yaml
- type: azure-blob-upload
  config:
    subscriptionId: "{{secrets.azure.subscriptionId}}"
    clientId: "{{secrets.azure.clientId}}"
    clientSecret: "{{secrets.azure.clientSecret}}"
    tenantId: "{{secrets.azure.tenantId}}"
    storageAccount: "mystorageaccount"
    container: "mycontainer"
    blobName: "data/report.json"
    content: '{"status": "completed"}'
    contentType: "application/json"
```

## Error Handling

All node types return structured error responses:

```json
{
  "error": "Virtual machine 'my-vm' not found in resource group 'my-rg'",
  "code": "ResourceNotFound"
}
```

## License

MIT License - See [LICENSE](LICENSE) for details.