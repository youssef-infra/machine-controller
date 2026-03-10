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

// ListMachines lists all VMs belonging to the cluster and zone.
//
// The Compute API filters by account + text. We use "gardener-" as text filter
// to narrow to Gardener-managed VMs, then filter client-side by MCM tags
// (cluster + role + zone) following the hcloud convention.
func (d *Driver) ListMachines(ctx context.Context, req *driver.ListMachinesRequest) (*driver.ListMachinesResponse, error) {
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

	clusterName := getClusterName(req.MachineClass.Labels)

	// Query VMs by account + text filter "gardener-" to narrow results.
	// Then filter client-side by cluster + role + zone tags (hcloud convention).
	vms, err := client.ListVMs(ctx, vpcloudclient.VMQuery{
		Account:    providerSpec.AccountCode,
		TextFilter: apis.VMNamePrefix,
		Tags: []vpcloudclient.Tag{
			{Key: "mcm.gardener.cloud/cluster", Value: clusterName},
			{Key: "mcm.gardener.cloud/role", Value: "node"},
			{Key: "topology.kubernetes.io/zone", Value: providerSpec.Zone},
		},
	})
	if err != nil {
		return nil, status.Error(codes.Unavailable, fmt.Sprintf("failed to list VMs: %v", err))
	}

	machineList := make(map[string]string)
	for _, vm := range vms {
		if vm.Status == vpcloudclient.VMStatusTerminated || vm.Status == vpcloudclient.VMStatusArchived {
			continue
		}
		providerID := apis.EncodeProviderID(providerSpec.Zone, vm.Code)
		machineList[providerID] = vm.Name
	}

	return &driver.ListMachinesResponse{
		MachineList: machineList,
	}, nil
}
