package vpcloud

import (
	"github.com/gardener/machine-controller-manager/pkg/util/provider/driver"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Driver implements the MCM driver.Driver interface for VpCloud Compute.
type Driver struct {
	Client   client.Client
	SSOToken string // base64-encoded "client_id:client_secret" for Keycloak
}

// NewDriver returns a new VpCloud MCM driver.
// ssoToken can be empty if only apiToken-based auth is used.
func NewDriver(c client.Client, ssoToken string) driver.Driver {
	return &Driver{
		Client:   c,
		SSOToken: ssoToken,
	}
}
