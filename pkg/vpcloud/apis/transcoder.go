package apis

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// EncodeProviderID encodes a zone and VM code into a ProviderID.
// Format: vpcloud:///<zone>/<vm-code>
func EncodeProviderID(zone, vmCode string) string {
	return fmt.Sprintf("vpcloud:///%s/%s", url.PathEscape(zone), url.PathEscape(vmCode))
}

// DecodeProviderID extracts the zone and VM code from a ProviderID.
func DecodeProviderID(providerID string) (zone string, vmCode string, err error) {
	if !strings.HasPrefix(providerID, "vpcloud:///") {
		return "", "", fmt.Errorf("invalid provider ID scheme, expected vpcloud:///: %s", providerID)
	}

	path := strings.TrimPrefix(providerID, "vpcloud:///")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid provider ID format, expected vpcloud:///<zone>/<vm-code>: %s", providerID)
	}

	zone, err = url.PathUnescape(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("invalid zone in provider ID: %w", err)
	}

	vmCode, err = url.PathUnescape(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("invalid vm code in provider ID: %w", err)
	}

	return zone, vmCode, nil
}

// DecodeProviderSpec unmarshals and validates a ProviderSpec from raw JSON bytes.
func DecodeProviderSpec(raw []byte) (*ProviderSpec, error) {
	if raw == nil {
		return nil, fmt.Errorf("provider spec is nil")
	}

	var spec ProviderSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal provider spec: %w", err)
	}

	if errs := ValidateProviderSpec(&spec); len(errs) > 0 {
		return nil, fmt.Errorf("invalid provider spec: %v", errs)
	}

	return &spec, nil
}

// ValidateProviderSpec validates a ProviderSpec and returns all validation errors.
func ValidateProviderSpec(spec *ProviderSpec) []error {
	var errs []error

	if spec.APIEndpoint == "" {
		errs = append(errs, fmt.Errorf("apiEndpoint is required"))
	}
	if spec.AccountCode == "" {
		errs = append(errs, fmt.Errorf("accountCode is required"))
	}
	if spec.ImageCode == "" {
		errs = append(errs, fmt.Errorf("imageCode is required"))
	}
	if spec.Zone == "" {
		errs = append(errs, fmt.Errorf("zone is required"))
	}
	if spec.CPU <= 0 {
		errs = append(errs, fmt.Errorf("cpu must be > 0"))
	}
	if spec.Memory <= 0 {
		errs = append(errs, fmt.Errorf("memory must be > 0"))
	}
	if len(spec.Networks) == 0 {
		errs = append(errs, fmt.Errorf("at least one network is required"))
	}

	return errs
}
