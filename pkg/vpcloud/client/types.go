package client

import "time"

// CreateVMRequest is the driver-level request for creating a VM.
// Mapped to compute-sdk-go's CreateVM in client.go.
type CreateVMRequest struct {
	Name              string          `json:"name"`
	Account           string          `json:"account"`
	ImageCode         string          `json:"imageCode"`
	Networks          []CreateNetwork `json:"networks"`
	CPU               int             `json:"cpu"`
	Memory            int             `json:"memory"`
	MainVolumeSize    int             `json:"mainVolumeSize"`
	UserDataBase64    string          `json:"userDataBase64,omitempty"`
	ManagedBy         string          `json:"managedBy,omitempty"`
	Tags              []Tag           `json:"tags,omitempty"`
	CPUCos            int             `json:"cpuCos,omitempty"`            // 1=besteffort, 2=standard, 3=priority
	CPUMake           string          `json:"cpuMake,omitempty"`           // "intel" or "amd"
	SSHKey            string          `json:"sshKey,omitempty"`            // SSH public key
	AvailabilityGroup string          `json:"availabilityGroup,omitempty"` // HA placement group
}

// CreateNetwork specifies a network interface for a VM.
type CreateNetwork struct {
	SubnetCode string `json:"subnetCode"`
	Order      int    `json:"order"`
}

// Tag is a key-value pair attached to a VM for identification.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// VM represents a virtual machine returned by the Compute API.
type VM struct {
	Code       string      `json:"code"`
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Networks   []VMNetwork `json:"networks,omitempty"`
	CPU        int         `json:"cpu,omitempty"`
	Memory     int         `json:"memory,omitempty"`
	Tags       []Tag       `json:"tags,omitempty"`
	CreatedAt  time.Time   `json:"createdAt,omitempty"`
	Datacenter string      `json:"datacenter,omitempty"`
}

// VMNetwork represents a network interface attached to a VM.
type VMNetwork struct {
	SubnetCode string `json:"subnetCode,omitempty"`
	IPAddress  string `json:"ipAddress,omitempty"`
	Order      int    `json:"order"`
}

// VMQuery is used to filter VMs when listing.
// Account is required. TextFilter and Tags are optional.
// Tags are filtered client-side since the Compute API doesn't support tag-based queries.
type VMQuery struct {
	Account    string `json:"account"`
	TextFilter string `json:"textFilter,omitempty"`
	Tags       []Tag  `json:"tags,omitempty"`
}

// APIError represents an error returned by the Compute API.
type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// VM status constants matching the Compute API state machine:
// init → creating → pending → starting → running → stopping → stopped → terminating → terminated
// Any state can transition to "failed".
const (
	VMStatusInit        = "init"
	VMStatusCreating    = "creating"
	VMStatusPending     = "pending"
	VMStatusStarting    = "starting"
	VMStatusRunning     = "running"
	VMStatusMigrating   = "migrating"
	VMStatusStopping    = "stopping"
	VMStatusStopped     = "stopped"
	VMStatusTerminating = "terminating"
	VMStatusTerminated  = "terminated"
	VMStatusArchived    = "archived"
	VMStatusFailed      = "failed"
)
