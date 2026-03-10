package apis

const (
	// Provider is the provider type identifier for VpCloud.
	Provider = "vpcloud-compute"
	// V1Alpha1 is the API version for VpCloud machine provider specs.
	V1Alpha1 = "mcm.gardener.cloud/v1alpha1"

	// VMNamePrefix is prepended to Machine names to create VM names.
	// Differentiates Gardener-managed VMs from the old machine controller's "platform-k8s-" prefix.
	VMNamePrefix = "gardener-"
)

// ProviderSpec is the provider-specific configuration stored in a MachineClass.
type ProviderSpec struct {
	APIVersion        string          `json:"apiVersion,omitempty"`
	APIEndpoint       string          `json:"apiEndpoint"`
	AccountCode       string          `json:"accountCode"`
	Zone              string          `json:"zone"`
	ImageCode         string          `json:"imageCode"`
	CPU               int             `json:"cpu"`
	Memory            int             `json:"memory"`
	MainVolumeSize    int             `json:"mainVolumeSize"`
	Networks          []NetworkConfig `json:"networks"`
	CPUCos            int             `json:"cpuCos,omitempty"`            // CPU class of service: 1=besteffort, 2=standard, 3=priority
	CPUMake           string          `json:"cpuMake,omitempty"`           // CPU vendor preference: "intel" or "amd"
	SSHKey            string          `json:"sshKey,omitempty"`            // SSH public key to inject into the VM
	AvailabilityGroup string          `json:"availabilityGroup,omitempty"` // HA placement group code
}

// NetworkConfig describes a network interface to attach to a VM.
type NetworkConfig struct {
	SubnetCode string `json:"subnetCode"`
	Order      int    `json:"order"`
}
