package vpcloud

import (
	"context"
	"fmt"
	"time"

	"github.com/gardener/machine-controller-manager/pkg/util/provider/driver"
	"github.com/gardener/machine-controller-manager/pkg/util/provider/machinecodes/codes"
	"github.com/gardener/machine-controller-manager/pkg/util/provider/machinecodes/status"

	"github.com/machine-controller-manager-provider-vpcloud/pkg/vpcloud/apis"
	vpcloudclient "github.com/machine-controller-manager-provider-vpcloud/pkg/vpcloud/client"
)

const (
	terminateTimeout = 5 * time.Minute
)

// DeleteMachine terminates a VM on VpCloud Compute.
// Idempotent: returns success if the VM is already gone.
func (d *Driver) DeleteMachine(ctx context.Context, req *driver.DeleteMachineRequest) (*driver.DeleteMachineResponse, error) {
	providerSpec, err := apis.DecodeProviderSpec(req.MachineClass.ProviderSpec.Raw)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to decode provider spec: %v", err))
	}

	secret := req.Secret
	if secret == nil {
		return nil, status.Error(codes.InvalidArgument, "secret is required")
	}

	token, err := getToken(secret, d.SSOToken)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to get credentials: %v", err))
	}

	client := vpcloudclient.GetClient(providerSpec.APIEndpoint, token)

	// Try to get VM code from ProviderID first, fall back to name-based lookup.
	vmCode, err := d.resolveVMCode(ctx, client, req, providerSpec)
	if err != nil {
		// If we can't find the VM at all, it's already gone — success.
		if vpcloudclient.IsNotFound(err) {
			return &driver.DeleteMachineResponse{}, nil
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve VM: %v", err))
	}

	if err := client.TerminateVM(ctx, vmCode); err != nil {
		if vpcloudclient.IsNotFound(err) {
			return &driver.DeleteMachineResponse{}, nil
		}
		return nil, status.Error(codes.Unavailable, fmt.Sprintf("failed to terminate VM %s: %v", vmCode, err))
	}

	// Wait for the VM to reach Terminated status.
	waitCtx, cancel := context.WithTimeout(ctx, terminateTimeout)
	defer cancel()

	_, err = client.WaitForStatus(waitCtx, vmCode, vpcloudclient.VMStatusTerminated, pollInterval)
	if err != nil {
		if vpcloudclient.IsNotFound(err) {
			return &driver.DeleteMachineResponse{}, nil
		}
		return nil, status.Error(codes.DeadlineExceeded, fmt.Sprintf("VM %s did not terminate: %v", vmCode, err))
	}

	return &driver.DeleteMachineResponse{}, nil
}

// resolveVMCode gets the VM code either from the ProviderID or by name-based lookup.
func (d *Driver) resolveVMCode(ctx context.Context, client *vpcloudclient.Client, req *driver.DeleteMachineRequest, providerSpec *apis.ProviderSpec) (string, error) {
	providerID := req.Machine.Spec.ProviderID

	// Preferred: extract from ProviderID.
	if providerID != "" {
		_, vmCode, err := apis.DecodeProviderID(providerID)
		if err == nil {
			return vmCode, nil
		}
	}

	// Fallback: find by VM name (gardener-<machine-name>) via text filter + tag match.
	machineName := req.Machine.Name
	vmName := apis.VMNamePrefix + machineName

	vms, err := client.ListVMs(ctx, vpcloudclient.VMQuery{
		Account:    providerSpec.AccountCode,
		TextFilter: vmName,
		Tags: []vpcloudclient.Tag{
			{Key: "mcm.gardener.cloud/machine-name", Value: machineName},
		},
	})
	if err != nil {
		return "", err
	}

	for _, vm := range vms {
		if vm.Status != vpcloudclient.VMStatusTerminated && vm.Status != vpcloudclient.VMStatusArchived {
			return vm.Code, nil
		}
	}

	return "", &vpcloudclient.APIError{StatusCode: 404, Message: fmt.Sprintf("VM for machine %s not found", machineName)}
}
