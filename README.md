# Machine Controller Manager Provider VpCloud

MCM driver that translates Gardener machine requests into VpCloud Compute API calls.

## What this does

When Gardener needs a worker node for a Shoot cluster, it creates a `Machine` resource. The Machine Controller Manager (MCM) picks it up and calls this driver. The driver talks to the VpCloud Compute API to create/delete/monitor VMs.

```
Gardener creates Machine resource
        │
        ▼
  MCM (generic controller)
        │
        ▼
  This driver (vpcloud provider)
        │
        ▼
  compute-sdk-go (VpCloud's auto-generated Go SDK)
        │
        ▼
  VpCloud Compute API (https://compute.cloud.vptech.eu)
        │
        ▼
  VM boots with cloud-init → kubelet starts → Node joins cluster
```

## The 4 operations

| Operation | What happens | SDK call |
|-----------|-------------|----------|
| **CreateMachine** | Creates a VM, injects cloud-init, waits until Running | `VMAPI.CreateVM()` then polls `VMAPI.GetVM()` |
| **DeleteMachine** | Terminates a VM, waits until Terminated | `VMAPI.Terminate()` |
| **GetMachineStatus** | Checks if a VM still exists and its status | `VMAPI.GetVM()` |
| **ListMachines** | Finds all VMs belonging to a cluster+zone | `VMAPI.ListVMs()` + client-side tag filter |

## How VMs are identified

Each VM gets a **ProviderID** that includes the zone for topology-aware scheduling:

```
vpcloud:///<zone>/<vm-code>
```

Example: `vpcloud:///fr1/a1b2c3d4-e5f6-7890-abcd-ef1234567890`

**VM naming**: VMs are named `gardener-<machine-name>` to distinguish them from VMs managed by the existing machine-controller (which uses the `platform-k8s-` prefix).

## How VMs are tagged

Every VM is created with tags following the hcloud convention so MCM can find them:

```
mcm.gardener.cloud/cluster=<shoot-name>       → "this VM belongs to this Shoot"
mcm.gardener.cloud/role=node                   → "this is a worker node"
mcm.gardener.cloud/machine-class=<class-name>  → "created by this MachineClass"
mcm.gardener.cloud/machine-name=<machine-name> → "corresponds to this Machine resource"
topology.kubernetes.io/zone=<zone>             → "VM is in this availability zone"
```

`ListMachines` filters by `account` + text search on the API side, then matches `cluster` + `role` + `zone` tags client-side.

## Compute API client

Uses **`compute-sdk-go` v1.39.0** — the same auto-generated Go SDK used by the team's existing machine-controller at `/infrastructure/machine-controller`. This replaces the hand-written HTTP client we had before.

The SDK is initialized with the Compute API base URL and an OAuth2 bearer token injected via context:

```go
conf := computeSdk.NewConfiguration()
conf.BasePath = "https://compute.cloud.vptech.eu"
client := computeSdk.NewAPIClient(conf)

ctx := context.WithValue(ctx, computeSdk.ContextAccessToken, token)
vm, _, err := client.VMAPI.CreateVM(ctx).Body(createReq).Execute()
```

Clients are cached by baseURL+token (singleton pattern from hcloud).

## Authentication

The driver reads credentials from a Kubernetes Secret at runtime. On every operation (create, delete, list, status), it exchanges username/password for a fresh JWT via Keycloak — no manual token rotation needed.

### `.env` file (in project root)

Only 4 variables are used by `deploy-credentials.sh`:

```bash
cat > .env << 'EOF'
# USED — deployed to the cluster
SSO_TOKEN=<base64 of client_id:client_secret>
VPCLOUD_USERNAME=<sso-username>
VPCLOUD_PASSWORD=<sso-password>
VPCLOUD_ACCOUNT_CODE=<vpcloud-account-uuid>

# NOT USED by code — reference only (for manual curl testing)
# These are the components inside SSO_TOKEN. To regenerate:
#   echo -n "client_id:client_secret" | base64
#SSO_CLIENT_ID=content-servertester
#SSO_CLIENT_SECRET=<keycloak-client-secret>
#SSO_ENDPOINT=https://ssoco.platform.vpgrp.net/auth/realms/vpgrp/protocol/openid-connect/token
EOF
```

| Variable | Used by | Description |
|----------|---------|-------------|
| `SSO_TOKEN` | `deploy-credentials.sh` → ControllerDeployment | Base64 of `client_id:client_secret`, becomes `VEEPEE_SSO_TOKEN` env var |
| `VPCLOUD_USERNAME` | `deploy-credentials.sh` → Garden Secret | SSO username for Keycloak token exchange |
| `VPCLOUD_PASSWORD` | `deploy-credentials.sh` → Garden Secret | SSO password for Keycloak token exchange |
| `VPCLOUD_ACCOUNT_CODE` | `deploy-credentials.sh` → Garden Secret | VpCloud business account UUID |

### Two auth modes

**Mode A — Direct token** (fallback):
```yaml
data:
  apiToken: <base64 bearer token>
  userData: <base64 cloud-init>
```

**Mode B — SSO credentials** (recommended, auto-refresh):
```yaml
data:
  username: <base64 SSO username>
  password: <base64 SSO password>
  userData: <base64 cloud-init>
```

Mode B exchanges username/password for a JWT via a direct Keycloak HTTP call on every operation. Tokens auto-refresh — no manual rotation needed.

Requires the `VEEPEE_SSO_TOKEN` env var (base64-encoded `clientId:clientSecret`). This is injected automatically by the extension's ControlPlane webhook.

### How credentials flow

```
Garden Secret (username, password)
  └──► gardenlet syncs to Seed → cloudprovider Secret
          └──► MCM sidecar reads username/password per operation
                  └──► credentials.go + VEEPEE_SSO_TOKEN → Keycloak → fresh JWT
                          └──► Compute API call with Bearer token
```

### Manual token test

```bash
# Uncomment SSO_CLIENT_ID, SSO_CLIENT_SECRET, SSO_ENDPOINT in .env first
source .env
curl -s -X POST "$SSO_ENDPOINT" \
  -d "grant_type=password" \
  -d "client_id=$SSO_CLIENT_ID" \
  -d "client_secret=$SSO_CLIENT_SECRET" \
  -d "username=$VPCLOUD_USERNAME" \
  -d "password=$VPCLOUD_PASSWORD"
```

## ProviderSpec (MachineClass config)

```json
{
  "apiEndpoint": "https://compute.cloud.vptech.eu",
  "accountCode": "<account-uuid>",
  "zone": "fr1",
  "imageCode": "<os-image-uuid>",
  "cpu": 4,
  "memory": 8,
  "mainVolumeSize": 50,
  "cpuCos": 2,
  "cpuMake": "intel",
  "sshKey": "",
  "availabilityGroup": "",
  "networks": [
    { "subnetCode": "<subnet-uuid>", "order": 0 }
  ]
}
```

**New fields from existing machine-controller:**

| Field | Type | Description |
|-------|------|-------------|
| `cpuCos` | int | CPU class of service: 1=besteffort, 2=standard, 3=priority |
| `cpuMake` | string | CPU vendor preference: "intel" or "amd" |
| `sshKey` | string | SSH public key to inject into the VM |
| `availabilityGroup` | string | HA placement group code |

## Key behaviors

### Idempotent create
Before creating a VM, the driver lists VMs by account + text filter (`gardener-<name>`) and checks tags. If a matching VM exists, it returns the existing VM's ProviderID.

### Error cleanup on create
If CreateMachine fails after the VM was created:
1. If VM is in `failed` state → call `CleanVM` first (borrowed from existing machine-controller)
2. Then call `TerminateVM` to avoid orphaned resources

### Delete fallback
DeleteMachine first tries ProviderID. If that fails, it falls back to text search + tag match. If the VM is already gone, it returns success (idempotent).

### Singleton API client
SDK clients are cached by `baseURL+token` to avoid creating new clients on every MCM call (pattern from hcloud).

### VM state machine
```
init → creating → pending → starting → running → stopping → stopped → terminating → terminated
                                      ↘ migrating ↗
Any state → failed
```

### Timeouts and polling
- **CreateMachine**: polls every 5 seconds, times out after 5 minutes
- **DeleteMachine**: polls every 5 seconds, times out after 5 minutes
- **Error cleanup**: 60 second timeout (CleanVM + TerminateVM)

### Error code mapping
| Situation | MCM code |
|-----------|----------|
| Bad ProviderSpec, missing credentials, invalid ProviderID | `InvalidArgument` |
| Compute API call failed | `Unavailable` |
| VM not found | `NotFound` |
| VM didn't reach desired status in time | `DeadlineExceeded` |

## Project structure

```
cmd/machine-controller/main.go          # Entry point — wires up MCM with this driver
pkg/vpcloud/
├── plugin.go                            # Driver struct with SsoService dependency
├── credentials.go                       # Token extraction: apiToken direct or SSO via controllers framework
├── create_machine.go                    # CreateMachine with idempotency + error cleanup
├── delete_machine.go                    # DeleteMachine with ProviderID/name fallback
├── get_machine_status.go                # GetMachineStatus → SDK GetVM
├── list_machines.go                     # ListMachines: account filter + client-side tags
├── get_volume_ids.go                    # Stubs (Ceph CSI handles volumes)
├── apis/
│   ├── provider_spec.go                 # ProviderSpec struct (with cpuCos, cpuMake, etc.)
│   └── transcoder.go                    # ProviderID encode/decode + ProviderSpec validation
└── client/
    ├── client.go                        # compute-sdk-go wrapper with singleton cache
    └── types.go                         # Driver-level request/response types
kubernetes/
├── secret.yaml                          # Example credentials Secret
├── machine-class.yaml                   # Example MachineClass
└── machine.yaml                         # Example Machine
```

## Data centers

- Region: `europe`
- Zones: `fr1` (France), `fr2` (France), `nl1` (Netherlands)

## Build and deploy

```bash
make build          # Build binary to bin/
make docker-build   # Build Docker image
make docker-push    # Push to registry
```

Image: `foundation-platform-docker.registry.vptech.eu/machine-controller-manager-provider-vpcloud:v0.1.0`

## Based on

1. **`compute-sdk-go` v1.39.0** — VpCloud's auto-generated Go SDK for the Compute API (same as existing machine-controller)
2. **Existing machine-controller auth pattern** — SSO token exchange via direct Keycloak HTTP call (replaced the `sso-auth-client/v3` library to avoid scope compatibility issues)
3. **Existing machine-controller** (`/infrastructure/machine-controller`) — Team's current VM controller. Adopted: SDK usage, auth chain (credentials → SSO → token), CreateVM fields (cpuCos, cpuMake, sshKey, availabilityGroup), CleanVM for failed VMs, VM naming convention
4. **`machine-controller-manager-provider-hcloud`** by 23technologies — Hetzner Cloud MCM driver. Adopted: singleton client cache, ProviderID with zone, error cleanup pattern, idempotent create, delete fallback, transcoder package, tag convention, MCM error code mapping
5. **`gardener/pkg/provider-local/`** — Gardener's built-in local provider. Used for initial `driver.Driver` interface structure
