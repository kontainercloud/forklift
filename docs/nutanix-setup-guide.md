# Nutanix AHV Provider -- Setup Guide

This guide walks through setting up and testing the Nutanix AHV provider
end-to-end using CLI only. It is intended for engineers starting from
scratch, and assumes you already have a running Nutanix cluster (Prism
Element or Prism Central) with at least one VM to migrate.

**Prerequisites on your workstation:** `oc`, `jq`, `curl`.

**Current maturity (read this first):** Nutanix cold migration works
end-to-end today, but the adapter is materially less mature than vSphere's
-- see `docs/enhancements/nutanix-ahv-migration-maturity.md` for the full
gap analysis. In particular, as of this writing: only **cold migration**
is supported (`spec.type: cold`; warm and live are not); there is **no
guest customization** step (no virtio driver injection, no static-IP
preservation, no LUKS/NBDE remediation) since Nutanix has no virt-v2v
conversion pod; and **shared-disk migration is not supported** (AHV's
Volume Group multi-attach mechanism isn't modeled in Forklift's inventory
yet). Plan accordingly -- a Windows VM without pre-installed VirtIO
drivers, for example, may migrate but fail to boot.

---

## Table of Contents

1. [Nutanix Cluster Prerequisites](#1-nutanix-cluster-prerequisites)
2. [Verify Connectivity from Linux](#2-verify-connectivity-from-linux)
3. [OpenShift Cluster Prerequisites](#3-openshift-cluster-prerequisites)
4. [Create the Nutanix Provider](#4-create-the-nutanix-provider)
5. [Discover Inventory](#5-discover-inventory)
6. [Create Network and Storage Maps](#6-create-network-and-storage-maps)
7. [Create a Migration Plan](#7-create-a-migration-plan)
8. [Start the Migration](#8-start-the-migration)
9. [Monitoring and Verification](#9-monitoring-and-verification)

---

## 1. Nutanix Cluster Prerequisites

You need:

- A Nutanix cluster reachable over HTTPS on port **9440**, either **Prism
  Element** (a single cluster's own management endpoint) or **Prism
  Central** (a multi-cluster management plane). Forklift auto-detects
  which one it's talking to; see `spec.settings.prismType` below if you
  need to override that detection.
- Credentials for a user with read access to VMs, subnets, storage
  containers, and (for cold migration) permission to create/delete
  Images. Forklift's cold-migration path creates a short-lived, per-disk
  Image Service image for each disk being migrated, then deletes it once
  the transfer completes.
- At least one powered-off VM to migrate. (Cold migration only -- see
  the maturity note above.)
- The cluster's TLS certificate, if you don't want to use
  `insecureSkipVerify` (testing only). Export it with, e.g.:

  ```bash
  openssl s_client -connect <prism-ip>:9440 -showcerts </dev/null 2>/dev/null | \
      openssl x509 -outform PEM > nutanix-ca.crt
  ```

If you're running **Prism Central**, note that Nutanix's newer `v4` API
surface (used for some image/storage-container operations) is only
exposed via Prism Central, not bare Prism Element -- see the maturity doc's
Tier 0 section if you're troubleshooting API-version-related errors.

---

## 2. Verify Connectivity from Linux

```bash
# Network reachability
ping -c 3 <prism-ip>

# HTTPS/API reachability (expects a 401 without credentials, not a connection error)
curl -sk -o /dev/null -w '%{http_code}\n' https://<prism-ip>:9440/api/nutanix/v3/prism_central

# With credentials, confirm you can authenticate and list VMs
curl -sk -u '<user>:<password>' \
    -X POST 'https://<prism-ip>:9440/api/nutanix/v3/vms/list' \
    -H 'Content-Type: application/json' \
    -d '{"kind":"vm","length":5}' | jq .
```

A `200` with a JSON body (even an empty `entities: []`) confirms
credentials and connectivity are correct before you create the Forklift
Provider.

---

## 3. OpenShift Cluster Prerequisites

### 3.1 Forklift (MTV) Operator

```bash
oc get pods -n openshift-mtv | grep forklift
```

### 3.2 OpenShift Virtualization

```bash
oc get csv -n openshift-cnv | grep kubevirt
```

### 3.3 OpenShift Host Provider

Forklift auto-creates an OpenShift "host" provider as the migration
destination. Verify it exists and is `Ready`:

```bash
oc get providers -n openshift-mtv
```

Unlike Hyper-V, Nutanix cold migration needs no additional CSI driver --
disk transfer goes through CDI's standard HTTP `DataVolume` importer
directly against the cluster's per-disk catalog-image URL.

---

## 4. Create the Nutanix Provider

### Step 1: Create the Secret

```bash
cat <<'EOF' | oc apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: nutanix-secret
  namespace: openshift-mtv
type: Opaque
stringData:
  user: "admin"                       # <-- Nutanix username
  password: "your-password"           # <-- Nutanix password
  insecureSkipVerify: "true"          # <-- For testing; use ca.crt in production
EOF
```

**Secret fields reference:**

| Field | Required | Description |
|-------|----------|--------------|
| `user` | Yes | Nutanix username |
| `password` | Yes | Nutanix password |
| `ca.crt` | No | PEM-encoded CA certificate (preferred over `insecureSkipVerify`) |
| `cacert` | No | Deprecated alias for `ca.crt`, removed in a future Forklift release |
| `insecureSkipVerify` | No | Set to `"true"` to skip TLS verification (testing only). When set without a CA cert, Forklift fetches and trusts the server's own certificate automatically so CDI's HTTP importer has something to validate against. |

### Step 2: Create the Provider

```bash
cat <<'EOF' | oc apply -f -
apiVersion: forklift.konveyor.io/v1beta1
kind: Provider
metadata:
  name: nutanix-source
  namespace: openshift-mtv
spec:
  type: nutanix
  url: "https://<prism-ip>:9440"      # <-- Prism Element or Prism Central endpoint
  secret:
    name: nutanix-secret
    namespace: openshift-mtv
EOF
```

**Optional provider settings** (`spec.settings`):

| Key | Description |
|-----|-------------|
| `prismType` | `"central"` or `"element"`. Overrides Forklift's automatic Prism Central/Element detection; only needed if auto-detection picks the wrong mode for your environment. |
| `clusterUuid` | Scopes inventory collection to a single cluster when the provider URL points at a Prism Central managing multiple clusters. Omit to collect from every cluster Prism Central manages. |

### Step 3: Wait for the Provider to become Ready

```bash
oc get provider nutanix-source -n openshift-mtv -w
```

To inspect conditions if something goes wrong:

```bash
oc get provider nutanix-source -n openshift-mtv \
    -o jsonpath='{range .status.conditions[*]}{.type}{": "}{.message}{"\n"}{end}'
```

---

## 5. Discover Inventory

```bash
PROVIDER_UID=$(oc get provider nutanix-source -n openshift-mtv -o jsonpath='{.metadata.uid}')
TOKEN=$(oc whoami -t)
CTRL_POD=$(oc get pods -n openshift-mtv -l control-plane=controller-manager \
    -o jsonpath='{.items[0].metadata.name}')
```

### List Networks (subnets)

```bash
oc exec $CTRL_POD -n openshift-mtv -c inventory -- \
    curl -sk -H "Authorization: Bearer $TOKEN" \
    "https://localhost:8443/providers/nutanix/${PROVIDER_UID}/networks" | jq .
```

### List Storage Containers

```bash
oc exec $CTRL_POD -n openshift-mtv -c inventory -- \
    curl -sk -H "Authorization: Bearer $TOKEN" \
    "https://localhost:8443/providers/nutanix/${PROVIDER_UID}/storagecontainers" | jq .
```

### List VMs

```bash
oc exec $CTRL_POD -n openshift-mtv -c inventory -- \
    curl -sk -H "Authorization: Bearer $TOKEN" \
    "https://localhost:8443/providers/nutanix/${PROVIDER_UID}/vms" | jq .
```

Note each VM's `uuid` field -- you'll need it for the migration plan and,
if you use `excludeDisks`, each disk's `uuid` from its VM-detail view.

### VM Detail (disks, NICs, categories, concerns)

```bash
VM_UUID="<vm-uuid>"   # <-- Replace with a VM id from the list above

oc exec $CTRL_POD -n openshift-mtv -c inventory -- \
    curl -sk -H "Authorization: Bearer $TOKEN" \
    "https://localhost:8443/providers/nutanix/${PROVIDER_UID}/vms/${VM_UUID}" | jq .
```

---

## 6. Create Network and Storage Maps

### Step 4: NetworkMap

```bash
cat <<'EOF' | oc apply -f -
apiVersion: forklift.konveyor.io/v1beta1
kind: NetworkMap
metadata:
  name: nutanix-netmap
  namespace: openshift-mtv
spec:
  provider:
    source:
      name: nutanix-source
      namespace: openshift-mtv
    destination:
      name: host
      namespace: openshift-mtv
  map:
    - source:
        id: "<subnet-uuid>"            # <-- From inventory (Step 5)
      destination:
        type: pod                      # pod | multus | ignored
EOF
```

A NIC whose subnet has **no** mapping entry is silently dropped from the
migrated VM spec rather than failing the migration -- double-check every
subnet your VMs use has an entry here.

### Step 5: StorageMap

```bash
oc get storageclasses
```

```bash
cat <<'EOF' | oc apply -f -
apiVersion: forklift.konveyor.io/v1beta1
kind: StorageMap
metadata:
  name: nutanix-storagemap
  namespace: openshift-mtv
spec:
  provider:
    source:
      name: nutanix-source
      namespace: openshift-mtv
    destination:
      name: host
      namespace: openshift-mtv
  map:
    - source:
        id: "<storage-container-uuid>"  # <-- From inventory (Step 5)
      destination:
        storageClass: "<your-storage-class>"
EOF
```

Verify both maps:

```bash
oc get networkmaps,storagemaps -n openshift-mtv
```

---

## 7. Create a Migration Plan

```bash
cat <<'EOF' | oc apply -f -
apiVersion: forklift.konveyor.io/v1beta1
kind: Plan
metadata:
  name: nutanix-test-plan
  namespace: openshift-mtv
spec:
  provider:
    source:
      name: nutanix-source
      namespace: openshift-mtv
    destination:
      name: host
      namespace: openshift-mtv
  map:
    network:
      name: nutanix-netmap
      namespace: openshift-mtv
    storage:
      name: nutanix-storagemap
      namespace: openshift-mtv
  targetNamespace: openshift-mtv        # <-- Namespace for the migrated VM
  vms:
    - id: "<vm-uuid>"                   # <-- From inventory (Step 5)
EOF
```

`spec.type` defaults to `cold` -- Nutanix supports no other value today.

**Optional per-VM fields supported for Nutanix:**

| Field | Description |
|-------|-------------|
| `pvcNameTemplate` | Template for PVC names; `{{.DiskIndex}}` numbers non-CDROM disks in inventory order. |
| `excludeDisks` | Disk **UUIDs** to skip (not vSphere-style bus addresses -- see `docs/compatibility/vm-fields.md`). Excluding every disk, or the boot disk, is flagged during validation. |

Wait for the plan to be `Ready`:

```bash
oc get plan nutanix-test-plan -n openshift-mtv -w
```

Check plan conditions and VM validation status:

```bash
oc get plan nutanix-test-plan -n openshift-mtv \
    -o jsonpath='{range .status.conditions[*]}{.type}{": "}{.message}{"\n"}{end}'
```

---

## 8. Start the Migration

```bash
cat <<'EOF' | oc apply -f -
apiVersion: forklift.konveyor.io/v1beta1
kind: Migration
metadata:
  name: nutanix-test-migration
  namespace: openshift-mtv
spec:
  plan:
    name: nutanix-test-plan
    namespace: openshift-mtv
EOF
```

---

## 9. Monitoring and Verification

### Watch migration progress

```bash
oc get migration nutanix-test-migration -n openshift-mtv -w
```

### Check plan VM status

```bash
oc get plan nutanix-test-plan -n openshift-mtv -o yaml | \
    yq '.status.migration.vms'
```

### Controller logs (inventory side)

```bash
CTRL_POD=$(oc get pods -n openshift-mtv -l control-plane=controller-manager \
    -o jsonpath='{.items[0].metadata.name}')

oc logs $CTRL_POD -n openshift-mtv -c inventory | grep -i nutanix | tail -30
```

### Provider server logs

```bash
NUTANIX_POD=$(oc get pods -n openshift-mtv | grep nutanix-source | awk '{print $1}')

oc logs $NUTANIX_POD -n openshift-mtv | tail -30
```

### Check for the migrated VM

```bash
oc get vm -n openshift-mtv
oc get vmi -n openshift-mtv
```

If the VM doesn't boot, check first whether its disks were attached via
AHV's PCI adapter type (mapped to KubeVirt's virtio bus) and the guest
doesn't have virtio drivers pre-installed -- this is the most common
cause today, since Nutanix has no guest-customization/driver-injection
step (see the maturity doc's Tier 3).

---

## Cleanup

```bash
oc delete migration nutanix-test-migration -n openshift-mtv
oc delete plan nutanix-test-plan -n openshift-mtv
oc delete storagemap nutanix-storagemap -n openshift-mtv
oc delete networkmap nutanix-netmap -n openshift-mtv
oc delete provider nutanix-source -n openshift-mtv
oc delete secret nutanix-secret -n openshift-mtv
```
