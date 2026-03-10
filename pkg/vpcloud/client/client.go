package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	computeSdk "git.vptech.eu/veepee/foundation/products/infrastructure/cloud/sdk/compute-sdk-go"
)

// Client wraps the compute-sdk-go API client with convenience methods
// for the MCM driver. Clients are cached by baseURL+token (singleton pattern
// borrowed from hcloud provider).
type Client struct {
	sdk     *computeSdk.APIClient
	baseURL string
	token   string
}

var (
	singletons   = make(map[string]*Client)
	singletonsMu sync.Mutex
)

// GetClient returns a cached client for the given baseURL and token.
// Creates a new one if it doesn't exist yet.
func GetClient(baseURL, token string) *Client {
	key := baseURL + "||" + token

	singletonsMu.Lock()
	defer singletonsMu.Unlock()

	if c, ok := singletons[key]; ok {
		return c
	}

	conf := computeSdk.NewConfiguration()
	conf.Servers = computeSdk.ServerConfigurations{{URL: baseURL}}
	conf.UserAgent = "machine-controller-manager-provider-vpcloud/v0.2.2"
	conf.HTTPClient = &http.Client{Timeout: 30 * time.Second}

	c := &Client{
		sdk:     computeSdk.NewAPIClient(conf),
		baseURL: baseURL,
		token:   token,
	}
	singletons[key] = c
	return c
}

// authCtx injects the OAuth2 bearer token into the context for SDK calls.
func (c *Client) authCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, computeSdk.ContextAccessToken, c.token)
}

// CreateVM creates a new virtual machine via the Compute API.
func (c *Client) CreateVM(ctx context.Context, req CreateVMRequest) (*VM, error) {
	// Build SDK request.
	networks := make([]computeSdk.CreateNetwork, len(req.Networks))
	for i, n := range req.Networks {
		networks[i] = computeSdk.CreateNetwork{
			Code:  n.SubnetCode,
			Order: int32(n.Order),
		}
	}

	tags := make([]computeSdk.CreateTag, len(req.Tags))
	for i, t := range req.Tags {
		tags[i] = computeSdk.CreateTag{
			Key:   t.Key,
			Value: t.Value,
		}
	}

	cpu := int32(req.CPU)
	memory := int32(req.Memory)
	mainVolumeSize := int32(req.MainVolumeSize)
	managedBy := req.ManagedBy

	sdkReq := computeSdk.CreateVM{
		Name:           req.Name,
		Account:        req.Account,
		ImageCode:      req.ImageCode,
		Networks:       networks,
		Cpu:            &cpu,
		Memory:         &memory,
		MainVolumeSize: &mainVolumeSize,
		Tags:           tags,
		Vnc:            false,
		ManagedBy:      &managedBy,
	}

	// Optional fields.
	if req.UserDataBase64 != "" {
		sdkReq.UserDataBase64 = &req.UserDataBase64
	}
	if req.CPUCos > 0 {
		cpuCos := int32(req.CPUCos)
		sdkReq.CpuCos = &cpuCos
	}
	if req.CPUMake != "" {
		sdkReq.CpuMake = &req.CPUMake
	}
	if req.SSHKey != "" {
		sdkReq.SshKey = &req.SSHKey
	}
	if req.AvailabilityGroup != "" {
		sdkReq.AvailabilityGroup = &req.AvailabilityGroup
	}

	result, httpResp, err := c.sdk.VMAPI.CreateVM(c.authCtx(ctx)).Body(sdkReq).Execute()
	if err != nil {
		return nil, wrapAPIError("create VM", err, httpResp)
	}

	return sdkVMToVM(&result.Items), nil
}

// TerminateVM terminates a VM by its code (UUID).
func (c *Client) TerminateVM(ctx context.Context, vmCode string) error {
	_, httpResp, err := c.sdk.VMAPI.Terminate(c.authCtx(ctx), vmCode).Execute()
	if err != nil {
		return wrapAPIError(fmt.Sprintf("terminate VM %s", vmCode), err, httpResp)
	}
	return nil
}

// CleanVM cleans/resets a failed VM before termination.
func (c *Client) CleanVM(ctx context.Context, vmCode string) error {
	_, httpResp, err := c.sdk.VMAPI.Clean(c.authCtx(ctx), vmCode).Execute()
	if err != nil {
		return wrapAPIError(fmt.Sprintf("clean VM %s", vmCode), err, httpResp)
	}
	return nil
}

// GetVM retrieves a VM by its code (UUID).
func (c *Client) GetVM(ctx context.Context, vmCode string) (*VM, error) {
	result, httpResp, err := c.sdk.VMAPI.GetVM(c.authCtx(ctx), vmCode).Execute()
	if err != nil {
		return nil, wrapAPIError(fmt.Sprintf("get VM %s", vmCode), err, httpResp)
	}
	return sdkVMToVM(&result.Items), nil
}

// ListVMs lists VMs filtered by account and optional text filter.
// The Compute API does not support arbitrary tag-based filtering — it filters
// by account, image, and text. We filter by MCM tags client-side.
func (c *Client) ListVMs(ctx context.Context, query VMQuery) ([]VM, error) {
	apiCall := c.sdk.VMAPI.ListVMs(c.authCtx(ctx)).Account(query.Account)
	if query.TextFilter != "" {
		apiCall = apiCall.TextFilter(query.TextFilter)
	}

	result, httpResp, err := apiCall.Execute()
	if err != nil {
		return nil, wrapAPIError("list VMs", err, httpResp)
	}

	vms := make([]VM, 0, len(result.Items))
	for i := range result.Items {
		vm := sdkVMToVM(&result.Items[i])

		// Client-side tag filtering: skip VMs that don't match all required tags.
		if len(query.Tags) > 0 && !vmMatchesTags(vm, query.Tags) {
			continue
		}

		vms = append(vms, *vm)
	}

	return vms, nil
}

// WaitForStatus polls a VM until it reaches the desired status or the context is cancelled.
func (c *Client) WaitForStatus(ctx context.Context, vmCode string, desiredStatus string, pollInterval time.Duration) (*VM, error) {
	for {
		vm, err := c.GetVM(ctx, vmCode)
		if err != nil {
			return nil, err
		}

		if vm.Status == desiredStatus {
			return vm, nil
		}

		if vm.Status == VMStatusFailed || vm.Status == VMStatusTerminated {
			return vm, fmt.Errorf("VM %s reached terminal status %s while waiting for %s", vmCode, vm.Status, desiredStatus)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// sdkVMToVM converts a compute-sdk-go VM into our internal VM type.
func sdkVMToVM(sdkVM *computeSdk.VM) *VM {
	vm := &VM{
		Code:   sdkVM.Code,
		Name:   sdkVM.Name,
		CPU:    int(sdkVM.Cpu),
		Memory: int(sdkVM.Memory),
	}

	if sdkVM.State != nil {
		vm.Status = *sdkVM.State
	}
	if !sdkVM.CreatedAt.IsZero() {
		vm.CreatedAt = sdkVM.CreatedAt
	}
	if sdkVM.Datacenter != "" {
		vm.Datacenter = sdkVM.Datacenter
	}

	for _, n := range sdkVM.Networks {
		vmNet := VMNetwork{
			Order: int(n.Order),
		}
		if n.Subnet != nil {
			vmNet.SubnetCode = *n.Subnet
		}
		if n.PrivateIp != nil {
			vmNet.IPAddress = *n.PrivateIp
		}
		vm.Networks = append(vm.Networks, vmNet)
	}

	for _, t := range sdkVM.Tags {
		vm.Tags = append(vm.Tags, Tag{Key: t.Key, Value: t.Value})
	}

	return vm
}

// vmMatchesTags returns true if the VM has ALL the required tags.
func vmMatchesTags(vm *VM, requiredTags []Tag) bool {
	tagMap := make(map[string]string, len(vm.Tags))
	for _, t := range vm.Tags {
		tagMap[t.Key] = t.Value
	}
	for _, req := range requiredTags {
		if val, ok := tagMap[req.Key]; !ok || val != req.Value {
			return false
		}
	}
	return true
}

// wrapAPIError wraps a compute-sdk-go error into our APIError type.
func wrapAPIError(op string, err error, httpResp *http.Response) error {
	statusCode := 0
	body := ""
	if httpResp != nil {
		statusCode = httpResp.StatusCode
		if httpResp.Body != nil {
			b, readErr := io.ReadAll(io.LimitReader(httpResp.Body, 1024))
			if readErr == nil && len(b) > 0 {
				body = string(b)
			}
		}
	}
	msg := fmt.Sprintf("%s: %v", op, err)
	if body != "" {
		msg = fmt.Sprintf("%s (body: %s)", msg, body)
	}
	return &APIError{
		StatusCode: statusCode,
		Message:    msg,
	}
}

// IsNotFound returns true if the error is a 404 from the Compute API.
func IsNotFound(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}
