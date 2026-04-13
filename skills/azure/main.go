package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
)

// ============================================================================
// AZURE CLIENT CACHE
// ============================================================================

type AzureClientCache struct {
	cred            azcore.TokenCredential
	resourceGroups  map[string]*armresources.ResourceGroupsClient
	virtualMachines map[string]*armcompute.VirtualMachinesClient
	managedClusters map[string]*armcontainerservice.ManagedClustersClient
	storageAccounts map[string]*armstorage.AccountsClient
	functionApps    map[string]*armappservice.WebAppsClient
}

var azureClientCache = &AzureClientCache{
	resourceGroups:  make(map[string]*armresources.ResourceGroupsClient),
	virtualMachines: make(map[string]*armcompute.VirtualMachinesClient),
	managedClusters: make(map[string]*armcontainerservice.ManagedClustersClient),
	storageAccounts: make(map[string]*armstorage.AccountsClient),
	functionApps:    make(map[string]*armappservice.WebAppsClient),
}

// AzureConfig holds Azure authentication configuration
type AzureConfig struct {
	SubscriptionID string `json:"subscription_id"`
	TenantID       string `json:"tenant_id"`
	ClientID       string `json:"client_id"`
	ClientSecret   string `json:"client_secret"`
}

// getAzureConfig extracts Azure configuration from step config
func getAzureConfig(config map[string]interface{}) AzureConfig {
	return AzureConfig{
		SubscriptionID: getString(config, "subscription_id"),
		TenantID:       getString(config, "tenant_id"),
		ClientID:       getString(config, "client_id"),
		ClientSecret:   getString(config, "client_secret"),
	}
}

// getAzureCredential creates an Azure credential
func getAzureCredential(cfg AzureConfig) (azcore.TokenCredential, error) {
	if cfg.TenantID != "" && cfg.ClientID != "" && cfg.ClientSecret != "" {
		return azidentity.NewClientSecretCredential(cfg.TenantID, cfg.ClientID, cfg.ClientSecret, nil)
	}
	return azidentity.NewDefaultAzureCredential(nil)
}

// getARMClientOptions creates ARM client options
func getARMClientOptions() *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.AzurePublic,
		},
	}
}

// getResourceGroupsClient gets or creates a resource groups client
func getResourceGroupsClient(cfg AzureConfig) (*armresources.ResourceGroupsClient, error) {
	cacheKey := cfg.SubscriptionID
	if client, ok := azureClientCache.resourceGroups[cacheKey]; ok {
		return client, nil
	}

	cred, err := getAzureCredential(cfg)
	if err != nil {
		return nil, err
	}

	client, err := armresources.NewResourceGroupsClient(cfg.SubscriptionID, cred, getARMClientOptions())
	if err != nil {
		return nil, err
	}

	azureClientCache.resourceGroups[cacheKey] = client
	azureClientCache.cred = cred
	return client, nil
}

// getVirtualMachinesClient gets or creates a virtual machines client
func getVirtualMachinesClient(cfg AzureConfig) (*armcompute.VirtualMachinesClient, error) {
	cacheKey := cfg.SubscriptionID
	if client, ok := azureClientCache.virtualMachines[cacheKey]; ok {
		return client, nil
	}

	cred, err := getAzureCredential(cfg)
	if err != nil {
		return nil, err
	}

	client, err := armcompute.NewVirtualMachinesClient(cfg.SubscriptionID, cred, getARMClientOptions())
	if err != nil {
		return nil, err
	}

	azureClientCache.virtualMachines[cacheKey] = client
	azureClientCache.cred = cred
	return client, nil
}

// getManagedClustersClient gets or creates a managed clusters client
func getManagedClustersClient(cfg AzureConfig) (*armcontainerservice.ManagedClustersClient, error) {
	cacheKey := cfg.SubscriptionID
	if client, ok := azureClientCache.managedClusters[cacheKey]; ok {
		return client, nil
	}

	cred, err := getAzureCredential(cfg)
	if err != nil {
		return nil, err
	}

	client, err := armcontainerservice.NewManagedClustersClient(cfg.SubscriptionID, cred, getARMClientOptions())
	if err != nil {
		return nil, err
	}

	azureClientCache.managedClusters[cacheKey] = client
	azureClientCache.cred = cred
	return client, nil
}

// getStorageAccountsClient gets or creates a storage accounts client
func getStorageAccountsClient(cfg AzureConfig) (*armstorage.AccountsClient, error) {
	cacheKey := cfg.SubscriptionID
	if client, ok := azureClientCache.storageAccounts[cacheKey]; ok {
		return client, nil
	}

	cred, err := getAzureCredential(cfg)
	if err != nil {
		return nil, err
	}

	client, err := armstorage.NewAccountsClient(cfg.SubscriptionID, cred, getARMClientOptions())
	if err != nil {
		return nil, err
	}

	azureClientCache.storageAccounts[cacheKey] = client
	azureClientCache.cred = cred
	return client, nil
}

// getFunctionAppsClient gets or creates a function apps client
func getFunctionAppsClient(cfg AzureConfig) (*armappservice.WebAppsClient, error) {
	cacheKey := cfg.SubscriptionID
	if client, ok := azureClientCache.functionApps[cacheKey]; ok {
		return client, nil
	}

	cred, err := getAzureCredential(cfg)
	if err != nil {
		return nil, err
	}

	client, err := armappservice.NewWebAppsClient(cfg.SubscriptionID, cred, getARMClientOptions())
	if err != nil {
		return nil, err
	}

	azureClientCache.functionApps[cacheKey] = client
	azureClientCache.cred = cred
	return client, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case []byte:
			return string(val)
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

func getBool(config map[string]interface{}, key string) bool {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case bool:
			return val
		case string:
			return val == "true" || val == "1" || val == "yes"
		case int:
			return val != 0
		case float64:
			return val != 0
		}
	}
	return false
}

func splitResourceID(id string) map[string]string {
	result := make(map[string]string)
	parts := strings.Split(id, "/")
	for i := 0; i < len(parts)-1; i += 2 {
		if i+1 < len(parts) {
			result[parts[i]] = parts[i+1]
		}
	}
	return result
}

// ============================================================================
// VM LIST EXECUTOR
// ============================================================================

type VMListExecutor struct{}

func (e *VMListExecutor) Type() string {
	return "azure-vm-list"
}

func (e *VMListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroup := getString(step.Config, "resource_group")

	client, err := getVirtualMachinesClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	var vms []map[string]interface{}

	if resourceGroup != "" {
		pager := client.NewListPager(resourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": fmt.Sprintf("failed to list VMs: %v", err)},
				}, nil
			}
			for _, vm := range page.Value {
				vms = append(vms, vmToMap(vm))
			}
		}
	} else {
		pager := client.NewListAllPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": fmt.Sprintf("failed to list VMs: %v", err)},
				}, nil
			}
			for _, vm := range page.Value {
				vms = append(vms, vmToMap(vm))
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"virtual_machines": vms,
			"count":            len(vms),
		},
	}, nil
}

func vmToMap(vm *armcompute.VirtualMachine) map[string]interface{} {
	result := map[string]interface{}{
		"name":             "",
		"id":               "",
		"location":         "",
		"provisioning_state": "",
		"vm_size":          "",
		"os_type":          "",
	}

	if vm.Name != nil {
		result["name"] = *vm.Name
	}
	if vm.ID != nil {
		result["id"] = *vm.ID
	}
	if vm.Location != nil {
		result["location"] = *vm.Location
	}
	if vm.Properties != nil {
		if vm.Properties.ProvisioningState != nil {
			result["provisioning_state"] = *vm.Properties.ProvisioningState
		}
		if vm.Properties.HardwareProfile != nil && vm.Properties.HardwareProfile.VMSize != nil {
			result["vm_size"] = string(*vm.Properties.HardwareProfile.VMSize)
		}
		if vm.Properties.StorageProfile != nil && vm.Properties.StorageProfile.OSDisk != nil && vm.Properties.StorageProfile.OSDisk.OSType != nil {
			result["os_type"] = string(*vm.Properties.StorageProfile.OSDisk.OSType)
		}
		if vm.Properties.NetworkProfile != nil && len(vm.Properties.NetworkProfile.NetworkInterfaces) > 0 {
			nics := make([]string, 0)
			for _, nic := range vm.Properties.NetworkProfile.NetworkInterfaces {
				if nic.ID != nil {
					nics = append(nics, *nic.ID)
				}
			}
			result["network_interfaces"] = nics
		}
	}

	return result
}

// ============================================================================
// VM START EXECUTOR
// ============================================================================

type VMStartExecutor struct{}

func (e *VMStartExecutor) Type() string {
	return "azure-vm-start"
}

func (e *VMStartExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroup := getString(step.Config, "resource_group")
	vmName := getString(step.Config, "vm_name")

	if resourceGroup == "" || vmName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "resource_group and vm_name are required"},
		}, nil
	}

	client, err := getVirtualMachinesClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	poller, err := client.BeginStart(ctx, resourceGroup, vmName, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to start VM: %v", err)},
		}, nil
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("VM start operation failed: %v", err)},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"status":         "success",
			"message":        fmt.Sprintf("VM '%s' started successfully", vmName),
			"vm_name":        vmName,
			"resource_group": resourceGroup,
		},
	}, nil
}

// ============================================================================
// VM STOP EXECUTOR
// ============================================================================

type VMStopExecutor struct{}

func (e *VMStopExecutor) Type() string {
	return "azure-vm-stop"
}

func (e *VMStopExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroup := getString(step.Config, "resource_group")
	vmName := getString(step.Config, "vm_name")
	skipShutdown := getBool(step.Config, "skip_shutdown")

	if resourceGroup == "" || vmName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "resource_group and vm_name are required"},
		}, nil
	}

	client, err := getVirtualMachinesClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	opts := &armcompute.VirtualMachinesClientBeginPowerOffOptions{
		SkipShutdown: to.Ptr(skipShutdown),
	}

	poller, err := client.BeginPowerOff(ctx, resourceGroup, vmName, opts)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to stop VM: %v", err)},
		}, nil
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("VM stop operation failed: %v", err)},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"status":         "success",
			"message":        fmt.Sprintf("VM '%s' stopped successfully", vmName),
			"vm_name":        vmName,
			"resource_group": resourceGroup,
			"skip_shutdown":  skipShutdown,
		},
	}, nil
}

// ============================================================================
// VM RESTART EXECUTOR
// ============================================================================

type VMRestartExecutor struct{}

func (e *VMRestartExecutor) Type() string {
	return "azure-vm-restart"
}

func (e *VMRestartExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroup := getString(step.Config, "resource_group")
	vmName := getString(step.Config, "vm_name")

	if resourceGroup == "" || vmName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "resource_group and vm_name are required"},
		}, nil
	}

	client, err := getVirtualMachinesClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	poller, err := client.BeginRestart(ctx, resourceGroup, vmName, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to restart VM: %v", err)},
		}, nil
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("VM restart operation failed: %v", err)},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"status":         "success",
			"message":        fmt.Sprintf("VM '%s' restarted successfully", vmName),
			"vm_name":        vmName,
			"resource_group": resourceGroup,
		},
	}, nil
}

// ============================================================================
// AKS LIST EXECUTOR
// ============================================================================

type AKSListExecutor struct{}

func (e *AKSListExecutor) Type() string {
	return "azure-aks-list"
}

func (e *AKSListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroup := getString(step.Config, "resource_group")

	client, err := getManagedClustersClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	var clusters []map[string]interface{}

	if resourceGroup != "" {
		pager := client.NewListByResourceGroupPager(resourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": fmt.Sprintf("failed to list AKS clusters: %v", err)},
				}, nil
			}
			for _, cluster := range page.Value {
				clusters = append(clusters, aksClusterToMap(cluster))
			}
		}
	} else {
		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": fmt.Sprintf("failed to list AKS clusters: %v", err)},
				}, nil
			}
			for _, cluster := range page.Value {
				clusters = append(clusters, aksClusterToMap(cluster))
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"clusters": clusters,
			"count":    len(clusters),
		},
	}, nil
}

func aksClusterToMap(cluster *armcontainerservice.ManagedCluster) map[string]interface{} {
	result := map[string]interface{}{
		"name":               "",
		"id":                 "",
		"location":           "",
		"kubernetes_version": "",
		"fqdn":               "",
		"provisioning_state": "",
		"power_state":        "",
	}

	if cluster.Name != nil {
		result["name"] = *cluster.Name
	}
	if cluster.ID != nil {
		result["id"] = *cluster.ID
	}
	if cluster.Location != nil {
		result["location"] = *cluster.Location
	}
	if cluster.Properties != nil {
		if cluster.Properties.KubernetesVersion != nil {
			result["kubernetes_version"] = *cluster.Properties.KubernetesVersion
		}
		if cluster.Properties.Fqdn != nil {
			result["fqdn"] = *cluster.Properties.Fqdn
		}
		if cluster.Properties.ProvisioningState != nil {
			result["provisioning_state"] = *cluster.Properties.ProvisioningState
		}
		if cluster.Properties.PowerState != nil && cluster.Properties.PowerState.Code != nil {
			result["power_state"] = string(*cluster.Properties.PowerState.Code)
		}
		if cluster.Properties.AgentPoolProfiles != nil {
			agentPools := make([]map[string]interface{}, 0)
			for _, pool := range cluster.Properties.AgentPoolProfiles {
				poolMap := map[string]interface{}{
					"name":    "",
					"count":   0,
					"vm_size": "",
					"os_type": "",
					"mode":    "",
				}
				if pool.Name != nil {
					poolMap["name"] = *pool.Name
				}
				if pool.Count != nil {
					poolMap["count"] = *pool.Count
				}
				if pool.VMSize != nil {
					poolMap["vm_size"] = *pool.VMSize
				}
				if pool.OSType != nil {
					poolMap["os_type"] = string(*pool.OSType)
				}
				if pool.Mode != nil {
					poolMap["mode"] = string(*pool.Mode)
				}
				agentPools = append(agentPools, poolMap)
			}
			result["agent_pools"] = agentPools
		}
	}

	return result
}

// ============================================================================
// AKS GET CREDENTIALS EXECUTOR
// ============================================================================

type AKSGetCredentialsExecutor struct{}

func (e *AKSGetCredentialsExecutor) Type() string {
	return "azure-aks-get-credentials"
}

func (e *AKSGetCredentialsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroup := getString(step.Config, "resource_group")
	clusterName := getString(step.Config, "cluster_name")

	if resourceGroup == "" || clusterName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "resource_group and cluster_name are required"},
		}, nil
	}

	client, err := getManagedClustersClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Get the cluster
	cluster, err := client.Get(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to get cluster: %v", err)},
		}, nil
	}

	var kubeconfig string

	// Get admin credentials
	adminCreds, err := client.ListClusterAdminCredentials(ctx, resourceGroup, clusterName, nil)
	if err == nil && adminCreds.Kubeconfigs != nil && len(adminCreds.Kubeconfigs) > 0 {
		kubeconfig = string(adminCreds.Kubeconfigs[0].Value)
	} else {
		// Try user credentials if admin credentials fail
		userCreds, err := client.ListClusterUserCredentials(ctx, resourceGroup, clusterName, nil)
		if err == nil && userCreds.Kubeconfigs != nil && len(userCreds.Kubeconfigs) > 0 {
			kubeconfig = string(userCreds.Kubeconfigs[0].Value)
		} else if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": fmt.Sprintf("failed to get cluster credentials: %v", err)},
			}, nil
		}
	}

	clusterInfo := map[string]interface{}{
		"name":               "",
		"location":           "",
		"kubernetes_version": "",
		"fqdn":               "",
		"provisioning_state": "",
	}

	if cluster.Name != nil {
		clusterInfo["name"] = *cluster.Name
	}
	if cluster.Location != nil {
		clusterInfo["location"] = *cluster.Location
	}
	if cluster.Properties != nil {
		if cluster.Properties.KubernetesVersion != nil {
			clusterInfo["kubernetes_version"] = *cluster.Properties.KubernetesVersion
		}
		if cluster.Properties.Fqdn != nil {
			clusterInfo["fqdn"] = *cluster.Properties.Fqdn
		}
		if cluster.Properties.ProvisioningState != nil {
			clusterInfo["provisioning_state"] = *cluster.Properties.ProvisioningState
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"kubeconfig":   kubeconfig,
			"cluster_info": clusterInfo,
			"message":      fmt.Sprintf("Retrieved credentials for AKS cluster '%s'", clusterName),
		},
	}, nil
}

// ============================================================================
// STORAGE LIST EXECUTOR
// ============================================================================

type StorageListExecutor struct{}

func (e *StorageListExecutor) Type() string {
	return "azure-storage-list"
}

func (e *StorageListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroup := getString(step.Config, "resource_group")

	client, err := getStorageAccountsClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	var accounts []map[string]interface{}

	if resourceGroup != "" {
		pager := client.NewListByResourceGroupPager(resourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": fmt.Sprintf("failed to list storage accounts: %v", err)},
				}, nil
			}
			for _, account := range page.Value {
				accounts = append(accounts, storageAccountToMap(account))
			}
		}
	} else {
		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": fmt.Sprintf("failed to list storage accounts: %v", err)},
				}, nil
			}
			for _, account := range page.Value {
				accounts = append(accounts, storageAccountToMap(account))
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"storage_accounts": accounts,
			"count":            len(accounts),
		},
	}, nil
}

func storageAccountToMap(account *armstorage.Account) map[string]interface{} {
	result := map[string]interface{}{
		"name":               "",
		"id":                 "",
		"location":           "",
		"sku_name":           "",
		"kind":               "",
		"provisioning_state": "",
		"primary_endpoints":  make(map[string]string),
	}

	if account.Name != nil {
		result["name"] = *account.Name
	}
	if account.ID != nil {
		result["id"] = *account.ID
	}
	if account.Location != nil {
		result["location"] = *account.Location
	}
	if account.SKU != nil && account.SKU.Name != nil {
		result["sku_name"] = string(*account.SKU.Name)
	}
	if account.Kind != nil {
		result["kind"] = *account.Kind
	}
	if account.Properties != nil {
		if account.Properties.ProvisioningState != nil {
			result["provisioning_state"] = *account.Properties.ProvisioningState
		}
		if account.Properties.PrimaryEndpoints != nil {
			endpoints := make(map[string]string)
			if account.Properties.PrimaryEndpoints.Blob != nil {
				endpoints["blob"] = *account.Properties.PrimaryEndpoints.Blob
			}
			if account.Properties.PrimaryEndpoints.File != nil {
				endpoints["file"] = *account.Properties.PrimaryEndpoints.File
			}
			if account.Properties.PrimaryEndpoints.Queue != nil {
				endpoints["queue"] = *account.Properties.PrimaryEndpoints.Queue
			}
			if account.Properties.PrimaryEndpoints.Table != nil {
				endpoints["table"] = *account.Properties.PrimaryEndpoints.Table
			}
			result["primary_endpoints"] = endpoints
		}
	}

	return result
}

// ============================================================================
// BLOB UPLOAD EXECUTOR
// ============================================================================

type BlobUploadExecutor struct{}

func (e *BlobUploadExecutor) Type() string {
	return "azure-blob-upload"
}

func (e *BlobUploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	accountName := getString(step.Config, "account_name")
	containerName := getString(step.Config, "container_name")
	blobName := getString(step.Config, "blob_name")
	sourcePath := getString(step.Config, "source_path")
	sourceData := getString(step.Config, "source_data")
	accountKey := getString(step.Config, "account_key")
	resourceGroup := getString(step.Config, "resource_group")

	if accountName == "" || containerName == "" || blobName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "account_name, container_name, and blob_name are required"},
		}, nil
	}

	if sourcePath == "" && sourceData == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "either source_path or source_data is required"},
		}, nil
	}

	// Get storage account key if not provided
	if accountKey == "" {
		accountKey = os.Getenv("AZURE_STORAGE_ACCOUNT_KEY")
	}

	if accountKey == "" && resourceGroup != "" {
		// Try to get the key from Azure
		storageClient, err := getStorageAccountsClient(cfg)
		if err == nil {
			keys, err := storageClient.ListKeys(ctx, resourceGroup, accountName, nil)
			if err == nil && keys.Keys != nil && len(keys.Keys) > 0 && keys.Keys[0].Value != nil {
				accountKey = *keys.Keys[0].Value
			}
		}
	}

	if accountKey == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "account_key is required (provide in config, set AZURE_STORAGE_ACCOUNT_KEY env var, or provide resource_group to fetch automatically)"},
		}, nil
	}

	// Create blob client
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to create blob credential: %v", err)},
		}, nil
	}

	blobURL := fmt.Sprintf("https://%s.blob.core.windows.net", accountName)
	blobClient, err := azblob.NewClientWithSharedKeyCredential(blobURL, credential, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to create blob client: %v", err)},
		}, nil
	}

	// Ensure container exists
	_, _ = blobClient.CreateContainer(ctx, containerName, nil)

	var uploadErr error
	if sourceData != "" {
		// Upload from base64 encoded data
		data, err := base64.StdEncoding.DecodeString(sourceData)
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": fmt.Sprintf("failed to decode source_data: %v", err)},
			}, nil
		}
		_, uploadErr = blobClient.UploadBuffer(ctx, containerName, blobName, data, nil)
	} else {
		// Upload from file
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": fmt.Sprintf("failed to read source file: %v", err)},
			}, nil
		}
		_, uploadErr = blobClient.UploadBuffer(ctx, containerName, blobName, data, nil)
	}

	if uploadErr != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to upload blob: %v", uploadErr)},
		}, nil
	}

	blobFullURL := fmt.Sprintf("https://%s.blob.core.windows.net/%s/%s", accountName, containerName, blobName)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"status":         "success",
			"message":        fmt.Sprintf("Blob '%s' uploaded successfully", blobName),
			"account_name":   accountName,
			"container_name": containerName,
			"blob_name":      blobName,
			"blob_url":       blobFullURL,
		},
	}, nil
}

// ============================================================================
// BLOB DOWNLOAD EXECUTOR
// ============================================================================

type BlobDownloadExecutor struct{}

func (e *BlobDownloadExecutor) Type() string {
	return "azure-blob-download"
}

func (e *BlobDownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	accountName := getString(step.Config, "account_name")
	containerName := getString(step.Config, "container_name")
	blobName := getString(step.Config, "blob_name")
	destPath := getString(step.Config, "dest_path")
	returnData := getBool(step.Config, "return_data")
	accountKey := getString(step.Config, "account_key")
	resourceGroup := getString(step.Config, "resource_group")

	if accountName == "" || containerName == "" || blobName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "account_name, container_name, and blob_name are required"},
		}, nil
	}

	// Get storage account key if not provided
	if accountKey == "" {
		accountKey = os.Getenv("AZURE_STORAGE_ACCOUNT_KEY")
	}

	if accountKey == "" && resourceGroup != "" {
		storageClient, err := getStorageAccountsClient(cfg)
		if err == nil {
			keys, err := storageClient.ListKeys(ctx, resourceGroup, accountName, nil)
			if err == nil && keys.Keys != nil && len(keys.Keys) > 0 && keys.Keys[0].Value != nil {
				accountKey = *keys.Keys[0].Value
			}
		}
	}

	if accountKey == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "account_key is required"},
		}, nil
	}

	// Create blob client
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to create blob credential: %v", err)},
		}, nil
	}

	blobURL := fmt.Sprintf("https://%s.blob.core.windows.net", accountName)
	blobClient, err := azblob.NewClientWithSharedKeyCredential(blobURL, credential, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to create blob client: %v", err)},
		}, nil
	}

	// Download the blob
	downloadResponse, err := blobClient.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to download blob: %v", err)},
		}, nil
	}
	defer downloadResponse.Body.Close()

	// Read the blob data
	blobData, err := io.ReadAll(downloadResponse.Body)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to read blob data: %v", err)},
		}, nil
	}

	result := map[string]interface{}{
		"status":         "success",
		"message":        fmt.Sprintf("Blob '%s' downloaded successfully", blobName),
		"account_name":   accountName,
		"container_name": containerName,
		"blob_name":      blobName,
		"content_type":   downloadResponse.ContentType,
		"blob_size":      len(blobData),
	}

	// Save to file if dest_path is provided
	if destPath != "" {
		err = os.WriteFile(destPath, blobData, 0644)
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": fmt.Sprintf("failed to write to dest_path: %v", err)},
			}, nil
		}
		result["dest_path"] = destPath
	}

	// Return base64 encoded data if requested
	if returnData {
		result["data"] = base64.StdEncoding.EncodeToString(blobData)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ============================================================================
// FUNCTION LIST EXECUTOR
// ============================================================================

type FunctionListExecutor struct{}

func (e *FunctionListExecutor) Type() string {
	return "azure-function-list"
}

func (e *FunctionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroup := getString(step.Config, "resource_group")

	client, err := getFunctionAppsClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	var functions []map[string]interface{}

	if resourceGroup != "" {
		pager := client.NewListByResourceGroupPager(resourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": fmt.Sprintf("failed to list function apps: %v", err)},
				}, nil
			}
			for _, app := range page.Value {
				if isFunctionApp(app) {
					functions = append(functions, functionAppToMap(app))
				}
			}
		}
	} else {
		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": fmt.Sprintf("failed to list function apps: %v", err)},
				}, nil
			}
			for _, app := range page.Value {
				if isFunctionApp(app) {
					functions = append(functions, functionAppToMap(app))
				}
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"function_apps": functions,
			"count":         len(functions),
		},
	}, nil
}

func isFunctionApp(app *armappservice.Site) bool {
	if app.Kind == nil {
		return false
	}
	kind := *app.Kind
	return kind == "functionapp" || strings.Contains(kind, "functionapp,")
}

func functionAppToMap(app *armappservice.Site) map[string]interface{} {
	result := map[string]interface{}{
		"name":              "",
		"id":                "",
		"location":          "",
		"kind":              "",
		"state":             "",
		"default_host_name": "",
		"host_names":        []string{},
	}

	if app.Name != nil {
		result["name"] = *app.Name
	}
	if app.ID != nil {
		result["id"] = *app.ID
	}
	if app.Location != nil {
		result["location"] = *app.Location
	}
	if app.Kind != nil {
		result["kind"] = *app.Kind
	}
	if app.Properties != nil {
		result["state"] = app.Properties.State
		if app.Properties.DefaultHostName != nil {
			result["default_host_name"] = *app.Properties.DefaultHostName
		}
		if app.Properties.HostNames != nil {
			hostNames := make([]string, 0)
			for _, hn := range app.Properties.HostNames {
				if hn != nil {
					hostNames = append(hostNames, *hn)
				}
			}
			result["host_names"] = hostNames
		}
	}

	return result
}

// ============================================================================
// FUNCTION INVOKE EXECUTOR
// ============================================================================

type FunctionInvokeExecutor struct{}

func (e *FunctionInvokeExecutor) Type() string {
	return "azure-function-invoke"
}

func (e *FunctionInvokeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	functionName := getString(step.Config, "function_name")
	functionApp := getString(step.Config, "function_app")
	resourceGroup := getString(step.Config, "resource_group")
	httpMethod := getString(step.Config, "http_method")
	functionKey := getString(step.Config, "function_key")
	requestBody := getString(step.Config, "request_body")

	if httpMethod == "" {
		httpMethod = "POST"
	}

	if functionName == "" || functionApp == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "function_name and function_app are required"},
		}, nil
	}

	client, err := getFunctionAppsClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Get the function app to determine the URL
	app, err := client.Get(ctx, resourceGroup, functionApp, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to get function app: %v", err)},
		}, nil
	}

	// Construct the function URL
	var baseURL string
	if app.Properties != nil && app.Properties.HostNames != nil && len(app.Properties.HostNames) > 0 && app.Properties.HostNames[0] != nil {
		baseURL = fmt.Sprintf("https://%s", *app.Properties.HostNames[0])
	} else {
		baseURL = fmt.Sprintf("https://%s.azurewebsites.net", functionApp)
	}

	// Build the function URL with key if provided
	functionURL := fmt.Sprintf("%s/api/%s", baseURL, functionName)
	if functionKey != "" {
		functionURL = fmt.Sprintf("%s?code=%s", functionURL, functionKey)
	}

	// Create HTTP request
	var reqBody io.Reader
	if requestBody != "" {
		reqBody = strings.NewReader(requestBody)
	}

	httpReq, err := http.NewRequestWithContext(ctx, httpMethod, functionURL, reqBody)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to create HTTP request: %v", err)},
		}, nil
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Execute the request
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to invoke function: %v", err)},
		}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to read response: %v", err)},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"status_code":   resp.StatusCode,
			"status":        resp.Status,
			"response_body": string(respBody),
			"function_name": functionName,
			"function_app":  functionApp,
			"method":        httpMethod,
			"url":           functionURL,
		},
	}, nil
}

// ============================================================================
// RESOURCE GROUP LIST EXECUTOR
// ============================================================================

type ResourceGroupListExecutor struct{}

func (e *ResourceGroupListExecutor) Type() string {
	return "azure-resource-group-list"
}

func (e *ResourceGroupListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)

	client, err := getResourceGroupsClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	pager := client.NewListPager(nil)

	var resourceGroups []map[string]interface{}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": fmt.Sprintf("failed to list resource groups: %v", err)},
			}, nil
		}
		for _, rg := range page.Value {
			resourceGroups = append(resourceGroups, resourceGroupToMap(rg))
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"resource_groups": resourceGroups,
			"count":           len(resourceGroups),
		},
	}, nil
}

func resourceGroupToMap(rg *armresources.ResourceGroup) map[string]interface{} {
	result := map[string]interface{}{
		"name":               "",
		"id":                 "",
		"location":           "",
		"provisioning_state": "",
		"tags":               make(map[string]string),
		"managed_by":         "",
	}

	if rg.Name != nil {
		result["name"] = *rg.Name
	}
	if rg.ID != nil {
		result["id"] = *rg.ID
	}
	if rg.Location != nil {
		result["location"] = *rg.Location
	}
	if rg.ManagedBy != nil {
		result["managed_by"] = *rg.ManagedBy
	}
	if rg.Properties != nil {
		if rg.Properties.ProvisioningState != nil {
			result["provisioning_state"] = *rg.Properties.ProvisioningState
		}
	}
	if rg.Tags != nil {
		tags := make(map[string]string)
		for k, v := range rg.Tags {
			if v != nil {
				tags[k] = *v
			}
		}
		result["tags"] = tags
	}

	return result
}

// ============================================================================
// RESOURCE GROUP CREATE EXECUTOR
// ============================================================================

type ResourceGroupCreateExecutor struct{}

func (e *ResourceGroupCreateExecutor) Type() string {
	return "azure-resource-group-create"
}

func (e *ResourceGroupCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := getAzureConfig(step.Config)
	resourceGroupName := getString(step.Config, "resource_group")
	location := getString(step.Config, "location")
	tagsJSON := getString(step.Config, "tags")

	if resourceGroupName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "resource_group name is required"},
		}, nil
	}

	if location == "" {
		location = "eastus" // Default location
	}

	// Parse tags if provided
	var tagsMap map[string]*string
	if tagsJSON != "" {
		var tags map[string]string
		err := json.Unmarshal([]byte(tagsJSON), &tags)
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": fmt.Sprintf("failed to parse tags: %v", err)},
			}, nil
		}
		tagsMap = make(map[string]*string)
		for k, v := range tags {
			tagsMap[k] = to.Ptr(v)
		}
	}

	client, err := getResourceGroupsClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Create the resource group
	result, err := client.CreateOrUpdate(ctx, resourceGroupName, armresources.ResourceGroup{
		Name:     to.Ptr(resourceGroupName),
		Location: to.Ptr(location),
		Tags:     tagsMap,
	}, nil)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to create resource group: %v", err)},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"status":            "success",
			"message":           fmt.Sprintf("Resource group '%s' created successfully", resourceGroupName),
			"resource_group":    resourceGroupToMap(&result.ResourceGroup),
			"resource_group_name": resourceGroupName,
			"location":          location,
		},
	}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50080"
	}

	server := grpc.NewSkillServer("skill-azure", "1.0.0")

	// Register all node type executors
	server.RegisterExecutor("azure-vm-list", &VMListExecutor{})
	server.RegisterExecutor("azure-vm-start", &VMStartExecutor{})
	server.RegisterExecutor("azure-vm-stop", &VMStopExecutor{})
	server.RegisterExecutor("azure-vm-restart", &VMRestartExecutor{})
	server.RegisterExecutor("azure-aks-list", &AKSListExecutor{})
	server.RegisterExecutor("azure-aks-get-credentials", &AKSGetCredentialsExecutor{})
	server.RegisterExecutor("azure-storage-list", &StorageListExecutor{})
	server.RegisterExecutor("azure-blob-upload", &BlobUploadExecutor{})
	server.RegisterExecutor("azure-blob-download", &BlobDownloadExecutor{})
	server.RegisterExecutor("azure-function-list", &FunctionListExecutor{})
	server.RegisterExecutor("azure-function-invoke", &FunctionInvokeExecutor{})
	server.RegisterExecutor("azure-resource-group-list", &ResourceGroupListExecutor{})
	server.RegisterExecutor("azure-resource-group-create", &ResourceGroupCreateExecutor{})

	fmt.Printf("Starting skill-azure gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
