package vpcloud

import (
	"context"
	"fmt"

	"github.com/gardener/machine-controller-manager/pkg/util/provider/driver"
	"github.com/gardener/machine-controller-manager/pkg/util/provider/machinecodes/codes"
	"github.com/gardener/machine-controller-manager/pkg/util/provider/machinecodes/status"

	"github.com/machine-controller-manager-provider-vpcloud/pkg/vpcloud/apis"
	vpcloudclient "github.com/machine-controller-manager-provider-vpcloud/pkg/vpcloud/client"
)

// GetMachineStatus returns the status of a VM on VpCloud Compute.
func (d *Driver) GetMachineStatus(ctx context.Context, req *driver.GetMachineStatusRequest) (*driver.GetMachineStatusResponse, error) {
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

	providerID := req.Machine.Spec.ProviderID
	if providerID == "" {
		return nil, status.Error(codes.NotFound, "machine has no provider ID yet")
	}
	_, vmCode, err := apis.DecodeProviderID(providerID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid provider ID: %v", err))
	}

	client := vpcloudclient.GetClient(providerSpec.APIEndpoint, token)

	vm, err := client.GetVM(ctx, vmCode)
	if err != nil {
		if vpcloudclient.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("VM %s not found", vmCode))
		}
		return nil, status.Error(codes.Unavailable, fmt.Sprintf("failed to get VM %s: %v", vmCode, err))
	}

	return &driver.GetMachineStatusResponse{
		ProviderID: apis.EncodeProviderID(providerSpec.Zone, vm.Code),
		NodeName:   vm.Name,
	}, nil
}
