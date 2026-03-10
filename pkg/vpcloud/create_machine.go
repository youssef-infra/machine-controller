package vpcloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/gardener/machine-controller-manager/pkg/util/provider/driver"
	"github.com/gardener/machine-controller-manager/pkg/util/provider/machinecodes/codes"
	"github.com/gardener/machine-controller-manager/pkg/util/provider/machinecodes/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/machine-controller-manager-provider-vpcloud/pkg/vpcloud/apis"
	vpcloudclient "github.com/machine-controller-manager-provider-vpcloud/pkg/vpcloud/client"
)

const (
	createTimeout = 5 * time.Minute
	pollInterval  = 5 * time.Second
)

// CreateMachine creates a VM on VpCloud Compute and returns the provider ID.
//
// Flow (inspired by hcloud provider):
//  1. Decode and validate ProviderSpec
//  2. Authenticate via SSO credentials from Secret
//  3. Check if VM already exists by name (idempotency)
//  4. Create VM via Compute API (using compute-sdk-go)
//  5. Wait for VM to reach Running status
//  6. On any error after creation, clean up the partially-created VM
func (d *Driver) CreateMachine(ctx context.Context, req *driver.CreateMachineRequest) (*driver.CreateMachineResponse, error) {
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

	// secret.Data["userData"] is the raw cloud-init script (Kubernetes auto-decodes base64).
	// The Compute API's UserDataBase64 field expects base64-encoded data.
	machineName := req.Machine.Name

	// VpCloud VMs need DHCP, swap-disable, and hostname injected via cloud-init:
	// - DHCP: base image may not have a default netplan config
	// - Swap disable: kubelet refuses to start with swap enabled
	// - Hostname: kubelet must register with the MCM machine name, not the VpCloud default hostname
	userData := base64.StdEncoding.EncodeToString(injectVpcloudConfig(secret.Data["userData"], machineName))

	client := vpcloudclient.GetClient(providerSpec.APIEndpoint, token)

	vmName := apis.VMNamePrefix + machineName
	clusterName := getClusterName(req.Machine.Labels)

	// Idempotency: check if a VM with this name already exists.
	// The Compute API filters by account + text; we filter by tags client-side.
	existingVMs, err := client.ListVMs(ctx, vpcloudclient.VMQuery{
		Account:    providerSpec.AccountCode,
		TextFilter: vmName,
		Tags: []vpcloudclient.Tag{
			{Key: "mcm.gardener.cloud/cluster", Value: clusterName},
			{Key: "mcm.gardener.cloud/machine-name", Value: machineName},
		},
	})
	if err == nil {
		for _, vm := range existingVMs {
			if vm.Status != vpcloudclient.VMStatusTerminated && vm.Status != vpcloudclient.VMStatusArchived {
				// VM already exists — return its ProviderID.
				return &driver.CreateMachineResponse{
					ProviderID: apis.EncodeProviderID(providerSpec.Zone, vm.Code),
					NodeName:   machineName,
				}, nil
			}
		}
	}

	// Build network config.
	networks := make([]vpcloudclient.CreateNetwork, len(providerSpec.Networks))
	for i, n := range providerSpec.Networks {
		networks[i] = vpcloudclient.CreateNetwork{
			SubnetCode: n.SubnetCode,
			Order:      n.Order,
		}
	}

	// SSH key: use providerSpec if set, otherwise fall back to secret.
	sshKey := providerSpec.SSHKey
	if sshKey == "" {
		sshKey = string(secret.Data["sshPublicKey"])
	}

	// Tags aligned with hcloud convention for consistent MCM behavior.
	createReq := vpcloudclient.CreateVMRequest{
		Name:              vmName,
		Account:           providerSpec.AccountCode,
		ImageCode:         providerSpec.ImageCode,
		Networks:          networks,
		CPU:               providerSpec.CPU,
		Memory:            providerSpec.Memory,
		MainVolumeSize:    providerSpec.MainVolumeSize,
		UserDataBase64:    userData,
		ManagedBy:         "gardener",
		CPUCos:            providerSpec.CPUCos,
		CPUMake:           providerSpec.CPUMake,
		SSHKey:            sshKey,
		AvailabilityGroup: providerSpec.AvailabilityGroup,
		Tags: []vpcloudclient.Tag{
			{Key: "mcm.gardener.cloud/cluster", Value: clusterName},
			{Key: "mcm.gardener.cloud/role", Value: "node"},
			{Key: "mcm.gardener.cloud/machine-class", Value: req.MachineClass.Name},
			{Key: "mcm.gardener.cloud/machine-name", Value: machineName},
			{Key: "topology.kubernetes.io/zone", Value: providerSpec.Zone},
		},
	}

	vm, err := client.CreateVM(ctx, createReq)
	if err != nil {
		return nil, status.Error(codes.Unavailable, fmt.Sprintf("failed to create VM: %v", err))
	}

	// From here on, if anything fails, clean up the partially-created VM.
	resp, err := d.waitAndFinalize(ctx, client, vm, machineName, providerSpec.Zone)
	if err != nil {
		d.cleanupOnCreateError(ctx, client, vm.Code, vm.Status)
		return nil, err
	}

	return resp, nil
}

// waitAndFinalize waits for the VM to become Running and returns the response.
func (d *Driver) waitAndFinalize(ctx context.Context, client *vpcloudclient.Client, vm *vpcloudclient.VM, machineName, zone string) (*driver.CreateMachineResponse, error) {
	waitCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	vmCode := vm.Code
	vm, err := client.WaitForStatus(waitCtx, vmCode, vpcloudclient.VMStatusRunning, pollInterval)
	if err != nil {
		return nil, status.Error(codes.DeadlineExceeded, fmt.Sprintf("VM %s did not reach Running: %v", vmCode, err))
	}

	return &driver.CreateMachineResponse{
		ProviderID: apis.EncodeProviderID(zone, vm.Code),
		NodeName:   machineName,
	}, nil
}

// cleanupOnCreateError cleans up a partially-created VM to avoid orphaned resources.
// If the VM is in Failed state, call CleanVM first (borrowed from existing machine-controller).
// Then terminate. Pattern borrowed from hcloud's createMachineOnErrorCleanup.
func (d *Driver) cleanupOnCreateError(ctx context.Context, client *vpcloudclient.Client, vmCode string, vmStatus string) {
	cleanupCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// If VM is in failed state, clean it first before terminating.
	if vmStatus == vpcloudclient.VMStatusFailed {
		if err := client.CleanVM(cleanupCtx, vmCode); err != nil {
			klog.Errorf("Failed to clean VM %s during create error cleanup: %v", vmCode, err)
		}
	}

	if err := client.TerminateVM(cleanupCtx, vmCode); err != nil {
		klog.Errorf("Failed to terminate VM %s during create error cleanup: %v", vmCode, err)
	}
}

func getClusterName(labels map[string]string) string {
	if labels == nil {
		return "unknown"
	}
	if name, ok := labels["shoot-name"]; ok {
		return name
	}
	return "unknown"
}

// nodeAddresses converts VM network info into Kubernetes node addresses.
func nodeAddresses(vm *vpcloudclient.VM) []corev1.NodeAddress {
	var addresses []corev1.NodeAddress
	for _, net := range vm.Networks {
		if net.IPAddress != "" {
			addresses = append(addresses, corev1.NodeAddress{
				Type:    corev1.NodeInternalIP,
				Address: net.IPAddress,
			})
		}
	}
	if vm.Name != "" {
		addresses = append(addresses, corev1.NodeAddress{
			Type:    corev1.NodeHostName,
			Address: vm.Name,
		})
	}
	return addresses
}

// dhcpCloudConfigEntry enables DHCP on all ethernet interfaces.
// VpCloud VM images may not have a default netplan config.
const dhcpCloudConfigEntry = `- content: |
    #cloud-config
    network:
      version: 2
      ethernets:
        id0:
          match:
            name: "en*"
          dhcp4: true
        id1:
          match:
            name: "eth*"
          dhcp4: true
  type: text/cloud-config
`

// vpcloudCloudConfigEntry returns a single cloud-config entry that handles all VpCloud fixes:
//   - Swap disable: kubelet refuses to start if swap is enabled (VpCloud VMs have it on by default)
//   - Hostname override: kubelet must register with the MCM machine name, not the VpCloud default
//
// All bootcmd entries MUST be in a single cloud-config because cloud-init overwrites (not appends)
// when merging bootcmd from multiple cloud-config entries in a cloud-config-archive.
// We use "hostname" (not "hostnamectl") because D-Bus isn't running during bootcmd.
// preserve_hostname prevents cloud-init's set-hostname/update-hostname modules from reverting
// the hostname to VpCloud's metadata value.
func vpcloudCloudConfigEntry(machineName string) string {
	return fmt.Sprintf(`- content: |
    #cloud-config
    preserve_hostname: true
    hostname: %s
    swap:
      filename: ""
      size: 0
    bootcmd:
      - swapoff -a
      - sed -i '/swap/d' /etc/fstab
      - hostname %s
      - echo %s > /etc/hostname
  type: text/cloud-config
`, machineName, machineName, machineName)
}

// injectVpcloudConfig prepends VpCloud-specific cloud-config entries to a #cloud-config-archive.
// Injects DHCP config, swap disable, and hostname override (set to machineName so kubelet
// registers with the name MCM expects).
// If the userData is not a cloud-config-archive, it is returned unchanged.
func injectVpcloudConfig(userData []byte, machineName string) []byte {
	raw := string(userData)
	const header = "#cloud-config-archive\n"
	if !strings.HasPrefix(raw, header) {
		return userData
	}
	// Insert VpCloud config entries right after the header, before existing entries.
	return []byte(header + dhcpCloudConfigEntry + vpcloudCloudConfigEntry(machineName) + raw[len(header):])
}
