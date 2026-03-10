package vpcloud

import (
	"context"

	"github.com/gardener/machine-controller-manager/pkg/util/provider/driver"
)

// GetVolumeIDs returns the volume IDs for PVs attached to VpCloud VMs.
// Stub implementation — volume management is handled by Ceph CSI.
func (d *Driver) GetVolumeIDs(_ context.Context, req *driver.GetVolumeIDsRequest) (*driver.GetVolumeIDsResponse, error) {
	return &driver.GetVolumeIDsResponse{}, nil
}

// GenerateMachineClassForMigration is not needed for VpCloud.
func (d *Driver) GenerateMachineClassForMigration(_ context.Context, _ *driver.GenerateMachineClassForMigrationRequest) (*driver.GenerateMachineClassForMigrationResponse, error) {
	return &driver.GenerateMachineClassForMigrationResponse{}, nil
}

// InitializeMachine is a no-op for VpCloud.
func (d *Driver) InitializeMachine(_ context.Context, _ *driver.InitializeMachineRequest) (*driver.InitializeMachineResponse, error) {
	return &driver.InitializeMachineResponse{}, nil
}
