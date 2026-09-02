---
title: nutanix-ahv-migration-maturity
authors:
  - "@tamalsaha"
reviewers:
  - TBD
approvers:
  - TBD
creation-date: 2026-09-02
last-updated: 2026-09-02
status: provisional
see-also:
  - "/enhancements/ovirt-lun-migration.md"
  - "/enhancements/vsphere-copy-offload-populator.md"
  - "/enhancements/shared-disks.md"
  - "/enhancements/pvc-name-template-simplification.md"
---

# Nutanix AHV Migration: Reaching vSphere Parity

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] User-facing documentation is created

## Open Questions

1. **Is Nutanix's v4 Changed Regions Tracking (CRT) API actually usable by
   Forklift in production**, or is it gated to certified backup-vendor
   partners? Nutanix's own developer documentation for the API says *"If you
   are not a backup vendor, we recommend reaching out to your backup
   provider,"* which reads as a partner-program soft-gate rather than a
   technical restriction. This needs a direct conversation with Nutanix
   (partner/API access, licensing, support commitments) before Phase 5
   (warm migration) can be scoped as `implementable`. See
   [Warm migration / CBT feasibility](#gap-tier-4-warm-migration--change-tracking).
2. **Can virt-v2v's `-i disk` input mode run against a network-attached
   block device (nbdkit `curl`/`ssh` plugin) rather than a fully-downloaded
   local file?** This determines whether Phase 4 (conversion pod) can reuse
   the existing per-disk catalog-image HTTP endpoint directly, or whether it
   requires downloading the full image to local/ephemeral storage first
   (which would regress the "no double-copy" property CDI HTTP import
   currently has for cold migrations). Needs a virt-v2v spike, not just a
   documentation read.
3. ~~Are Nutanix categories exposed via an API endpoint Forklift's collector
   isn't calling yet?~~ **Resolved during review — they already are.**
   `pkg/controller/provider/container/nutanix/resource_vm.go:40` sets
   `m.Categories = e.Metadata.Categories` from the standard v3 VM entity's
   `metadata.categories` field, populated by the same `listAllV3[vmEntity]`
   call the collector already makes — no new API integration needed. An
   earlier draft of this document stated the opposite (categories
   "unpopulated by any collector code found") based on an incomplete grep
   that checked the model struct and container-package file list but not
   the actual field-mapping code; that was wrong and is corrected throughout
   this document. See the corrected Tier 2 entry for
   tag/category → label mapping, now reclassified as low-risk wiring work.
4. **What is the actual, GA-supported minimum Prism Central/AOS version for
   the `compute-changed-regions` endpoint specifically** — not just the
   `dataprotection` namespace it lives in? Evidence collected so far is
   inconsistent: Nutanix's January 2025 blog introducing Changed Regions
   Tracking cites `pc.2024.3+`; the current (as of this research) v4 API
   reference matrix lists the `dataprotection` namespace as GA starting at
   PC 7.3 / AOS 7.3; and community forum posts show the endpoint in active
   use against PC 7.5 / AOS 11.0.0.1 (the `11.0.0.1` in that report doesn't
   match AOS's own 6.x/7.x numbering scheme and could reflect a different
   component's version string — treat that data point as unverified rather
   than authoritative). Nutanix's v4 versioning scheme explicitly
   distinguishes EA (`.aN` suffix) and RC (`.bN` suffix) stages from GA, and
   states *"EA APIs can be dropped or changed at any time without prior
   notice"* and *"RC ... not recommended for production use."*
   Namespace-level GA does not by itself prove the specific
   `compute-changed-regions` endpoint has left EA/RC — this needs to be
   confirmed against Nutanix's dataprotection v4 API changelog/release
   notes (or directly with Nutanix) before gating any implementation on a
   specific version floor, mirroring how oVirt's direct-LUN support is
   gated behind `engine >= 4.5.2.1` (see `ovirt-lun-migration.md`).

   This question also has a practical timing dimension, per Nutanix's
   published AOS release/support lifecycle (`endoflife.date/nutanix-aos`,
   itself sourced from Nutanix's own EOL/support-lifecycle pages):

   | AOS release | Released | End of Maintenance | End of Support |
   |---|---|---|---|
   | 7.6 (latest as of this writing) | Jul 27, 2026 | Oct 31, 2027 | Jul 31, 2028 |
   | 7.5 | Dec 8, 2025 | Mar 31, 2027 | Dec 31, 2027 |
   | 7.3 (cited `dataprotection` GA floor) | Jun 24, 2025 | **Sep 30, 2026** | Jun 30, 2027 |
   | 7.0 | Dec 4, 2024 | Mar 31, 2026 (already past) | Dec 31, 2026 |
   | 6.10 (LTS) | Oct 7, 2024 | Jan 31, 2026 (already past) | Oct 31, 2026 |

   AOS 7.3 — the version most often cited as the `dataprotection`/`vmm`/
   `clustermgmt` v4 GA floor — exits active maintenance essentially
   immediately (Sep 30, 2026) and moves into troubleshooting-only support.
   By the time any Nutanix warm-migration implementation could realistically
   ship, targeting AOS 7.3 as the supported floor would mean targeting a
   release already past active maintenance. A more realistic floor to
   design and test against is AOS 7.5 or 7.6, both still in full active
   maintenance with multi-year runway. This doesn't change the underlying
   feasibility question, but it does mean Phase 0's version research should
   explicitly target 7.5/7.6 behavior, not just confirm 7.3 GA status.
5. **What is Forklift's position on the Nutanix legacy-API deprecation
   timeline** (see [Gap Tier 0](#gap-tier-0-legacy-prism-api-deprecation-time-bound))?
   Specifically: should the v2.0/v3 → v4 migration for existing,
   already-shipped inventory-collection and image-transfer code be treated
   as its own time-boxed project distinct from this maturity roadmap, and
   who owns tracking Nutanix's release calendar against it?

## Summary

Forklift's Nutanix AHV source provider (`pkg/controller/plan/adapter/nutanix`)
migrates powered-off VMs end-to-end today: it maps CPU/memory/firmware/disks/
network into a KubeVirt `VirtualMachineSpec`, exports each disk to a
per-VM catalog image via Nutanix's Image Service, and imports it into a PVC
through CDI's HTTP `DataVolume` importer. This is a working cold-migration
path, but it is materially less mature than the vSphere adapter, which is the
project's most-hardened provider and the de facto maturity bar. This document
inventories every capability gap between the two adapters, backed by direct
code comparison and (for the gaps whose feasibility isn't decidable from this
codebase alone) external research into Nutanix's own API surface and
published deprecation schedule. It proposes a phased roadmap to close the
gaps, ordered by risk/effort and by what blocks what — and separately flags a
newly-identified, dated risk: parts of the *already-shipped* Nutanix adapter
depend on Prism API versions Nutanix has now scheduled for removal.

## Motivation

Nutanix AHV is currently marketed as a supported Forklift source provider,
but several of its `Validator` methods are unconditional pass-through stubs,
it has no unit test coverage for validation logic, it silently skips guest
customization (driver injection, static-IP preservation) that vSphere
performs via virt-v2v, and it is entirely absent from the project's own
`docs/compatibility/*.md` feature matrices. A user migrating a Windows VM
without pre-installed virtio drivers, or a VM with an unmapped subnet, gets
no early warning today — the plan validates as "ready" and then fails (or
worse, silently produces an unbootable VM) during execution. Separately,
Nutanix has published a dated end-of-life notice for the Prism API versions
(v0.8/v1/v2/v3) that most of the existing Nutanix inventory collector and
Prism-Element disk-transfer path still use — a maturity roadmap that ignores
this would be solving yesterday's problem while a real deadline approaches.

### Goals

- Enumerate every concrete behavioral gap between the Nutanix and vSphere
  adapters, with file/line evidence, not impressions.
- Distinguish gaps that are pure "wire it up" work (data already collected,
  logic already exists as a reusable helper) from gaps that require new
  inventory data, new Nutanix API integration, or upstream virt-v2v changes.
- Identify and date any risk to the adapter's *current* functionality from
  Nutanix's own platform roadmap, not just gaps relative to vSphere.
- Resolve (or explicitly flag as needing external validation) the open
  feasibility questions that block realistic planning: warm migration and
  guest customization.
- Propose a phased delivery plan with dependencies made explicit, so later
  phases aren't blocked on speculative feasibility of earlier ones.

### Non-Goals

- This document does not propose closing every gap in one release. It is a
  roadmap, not a single implementation plan.
- It does not commit to warm migration shipping — Phase 5 is gated on the
  open questions above and may conclude "not currently feasible."
- It does not cover UI/console work (the `forklift-console-plugin` repo is
  out of scope here); this document is scoped to the `kubevirt/forklift`
  controller, CLI, and inventory layers.
- It does not redesign the existing cold-migration disk-transfer mechanism
  (catalog image + CDI HTTP import) beyond what Tier 0's API migration and
  Tier 3's guest customization require.

## Background

### Current Nutanix AHV migration architecture

- **Provider** (`pkg/controller/plan/adapter/nutanix/adapter.go`): wires up
  `Builder`, `Validator`, `Client`, `DestinationClient`.
- **Builder** (`builder.go`): maps VM spec fields, builds one CDI
  `DataVolume` per non-CDROM disk sourced from an HTTP catalog-image URL —
  either Prism Element's direct Basic-Auth `v3` image download, or Prism
  Central's `v4` image service with a resolved redirect+cookie handshake
  (`image_v4.go`).
- **Client** (`client.go`): implements the `PreTransferActions`/`Finalize`
  lifecycle that creates and later deletes per-disk catalog images, plus
  stub implementations of the warm-migration snapshot interface
  (`CreateSnapshot`, `RemoveSnapshot`, `GetSnapshotDeltas`, `SetCheckpoints`,
  `CheckSnapshotReady`, `CheckSnapshotRemove`) that all currently return
  zero values.
- **Validator** (`validator.go`, 104 lines): all 20 methods of the
  `base.Validator` interface are implemented, but the majority are
  hardcoded to always pass rather than checking real state.
- **Scheduler** (`pkg/controller/plan/scheduler/nutanix/scheduler.go`, 76
  lines): a single global `MaxInFlight` counter, no host-awareness.
- **Host handler** (`pkg/controller/host/handler/nutanix/handler.go`): an
  explicit no-op, on the reasoning that AHV has no ESXi-style per-host
  object model.
- **Inventory collector** (`pkg/controller/provider/container/nutanix/`):
  lists clusters, hosts, VMs, and subnets via `listAllV3` (Prism `v3` REST
  kind API); lists Prism Element storage containers via `v2.0`
  (`storageContainersV2Path`); lists Prism Element images via the `v3`
  `image` kind; detects Prism Central via a `v3` self-describing endpoint
  (`prismCentralPath`). Only Prism Central images (`vmm v4.0`) and Prism
  Central storage containers (`clustermgmt v4.0`) have already been ported
  to v4.
- **Inventory model** (`pkg/controller/provider/model/nutanix/model.go`):
  `VM` has `GuestOSID`, `GuestOSVersion`, `GuestToolsEnabled/Mounted/
  Reachable/Version`, `Categories map[string]string` (populated from the v3
  VM entity's `metadata.categories` in
  `container/nutanix/resource_vm.go:40`), `NICs`, `Disks`,
  `Concerns`. `Disk` has `UUID`, `AdapterType`, `DeviceIndex`,
  `DiskSizeBytes`, `StorageContainerUUID`, `IsCdrom` — no `Shared` or
  `BusAddress`-equivalent field. `Host` has a bare `State string`, no
  boolean maintenance-mode flag.

### vSphere as the maturity baseline

The vSphere adapter (`pkg/controller/plan/adapter/vsphere`) supports both
cold and warm migration; routes disk copy either through CDI's VDDK importer
or through a virt-v2v conversion pod for guest customization
(`docs/use-of-virt-v2v-in-forklift.md` documents the conversion-pod
architecture in detail); implements every `Validator` method against real
inventory state; has a 326-line scheduler with host-capacity awareness and
shared-disk creator/consumer ordering; supports volume-populator storage
offload (XCOPY, CSI-native import); maps VMware `GuestID` and tags to
KubeVirt OS labels and destination annotations; and has full unit test
coverage (`validator_test.go`, `builder_test.go`, `scheduler_test.go`,
`destinationclient_test.go`, a suite harness).

## Proposal

The gaps are grouped into six tiers, numbered 0–5. Tier 0 is distinct from
the rest: it is not a maturity gap relative to vSphere, but a dated,
external risk to code that already ships today. Tiers 1–5 retain the
original vSphere-parity framing, ordered by a combination of risk, effort,
and whether the data/API needed to close them is already available.

### Gap Tier 0: Legacy Prism API deprecation (time-bound)

Nutanix published an End-of-Life bulletin (initial version December 5,
2024; updated June 17, 2026) for **Legacy API versions v0.8, v1, v2, and
v3** across Prism Element and Prism Central. The dated milestones:

| Milestone | Date |
|---|---|
| End-of-Support-Life announcement | Dec 5, 2024 |
| End-of-General-Availability (last AOS/PC release still containing legacy APIs) | Aligned with the AOS & PC release targeted for **Q2 CY2027** |
| Phased removal begins (legacy APIs and dependent CLIs/UIs start being removed from PC/PE) | Starting with the **Q4 CY2027** release |
| Last date of support | Aligned with the EOSL date of that Q2 CY2027 release under Nutanix's standard support-lifecycle policy (i.e., support continues for that release per normal EOSL terms, extending beyond the Q4 CY2027 removal-start date for customers who stay on it) |

**Source:** Nutanix "End of Life Announcement Bulletin - Legacy APIs
versions v0.8, v1, v2, and v3 for Prism" (PDF,
`download.nutanix.com/misc/LegacyAPI-EOLNotification.pdf`).

This directly affects code shipping in this repository **today**, not just
proposed new work. Confirmed via grep of the current tree:

- `pkg/controller/provider/container/nutanix/client.go` — cluster, host, VM,
  and subnet inventory collection all go through `listAllV3[...]` (the
  legacy `v3` REST kind API): `listAllV3[clusterEntity](r, "cluster", ...)`,
  `listAllV3[hostEntity](r, "host", ...)`, `listAllV3[vmEntity](r, "vm",
  ...)`, `listAllV3[networkEntity](r, "subnet", ...)`.
- `pkg/controller/provider/container/nutanix/image_api.go` — Prism Element
  image listing uses the legacy `v3` `"image"` kind
  (`listImagesElement`/`listAllV3[imageEntity]`).
- `pkg/controller/provider/container/nutanix/prism.go` /
  `storage_api.go` — Prism Element storage-container listing uses
  `storageContainersV2Path = "/api/nutanix/v2.0/storage_containers"`.
- `pkg/controller/provider/container/nutanix/prism.go` /
  `pkg/controller/plan/adapter/nutanix/client.go` — Prism-mode
  auto-detection probes `prismCentralPath = "/api/nutanix/v3/
  prism_central"`.
- `pkg/controller/plan/adapter/nutanix/builder.go` — the Prism Element
  cold-migration disk-download path builds URLs against the legacy `v3`
  image-file endpoint (`/api/nutanix/v3/images/%s/file`), and
  `clusterExternalIP` (used to rewrite Prism Central download redirects to
  the cluster VIP) calls `ListV3[Cluster]`.

Only two paths have already been ported to `v4`: Prism Central image
listing/creation (`vmm v4.0/content/images`, `image_v4.go`) and Prism
Central storage-container listing (`clustermgmt v4.0/config/
storage-containers`, `storage_api.go`). Everything else — the entire VM/
host/cluster/subnet inventory collector, and the whole Prism Element
disk-transfer path — is on APIs Nutanix has now dated for removal.

**Why this matters for sequencing:** unlike Tiers 1–5, this has an external
deadline that isn't under this project's control, and it touches the same
client/collector code that several later tiers (Tier 1's `StorageMapped`/
`NetworksMapped`/`MaintenanceMode`, Tier 2's category collection) will also
need to modify. Doing the legacy-API migration first avoids
rebasing that other work on soon-to-be-replaced client code.

**Estimated effort:** medium — this is a systematic client-layer port
(v3 → v4 for cluster/host/VM/subnet listing and Prism Element image
handling; v2.0 → v4 for Prism Element storage containers), not new feature
design. The runway (last-GA release ~Q2 CY2027, phased removal starting
~Q4 CY2027, both roughly a year or more out from this document's writing)
is real but not immediate, so this should be scheduled deliberately rather
than treated as a fire drill — see Open Question #5.

### Gap Tier 1: Validator correctness

These `Validator` methods return a hardcoded pass today even though the
inventory fields they'd need to check already exist in the Nutanix model
*and* are already consumed by the builder for other purposes. Closing these
requires no new inventory collection — only porting logic and, where
possible, reusing shared helpers from `pkg/controller/plan/adapter/base`
that vSphere already uses.

| Method | Nutanix today | Data available | vSphere reference |
|---|---|---|---|
| `StorageMapped` | `validator.go:28-31`, always `true` | `Disk.StorageContainerUUID` (used in `builder.go:436`) | iterates `vm.Disks`, checks `disk.Datastore.ID` against `Map.Storage.Status.Refs` |
| `NetworksMapped` / `NICNetworkRefs` | `validator.go:37-40,46-49`, stub | `NIC.SubnetUUID` (used in `builder.go:327`) | iterates `vm.Networks` against `Map.Network.Status.Refs` |
| `InvalidDiskSizes` | `validator.go:83-86`, stub | `Disk.DiskSizeBytes` (used in `builder.go:749`) | flags disk files with `capacity <= 0` |
| `MacConflicts` | `validator.go:88-91`, stub | `NIC.MACAddress` (used in `builder.go:342`) + shared `planbase.CheckMacConflicts` helper | checks source MACs against destination inventory |
| `GuestToolsInstalled` | `validator.go:98-100`, hardcoded `true` | `VM.GuestToolsEnabled/Mounted/Reachable/Version` — collected, currently unused anywhere | checks VMware Tools status when VM is powered on |
| `PVCNameTemplate` | `validator.go:93-96`, stub | `Disk.UUID` covers the base case; `WinDriveLetter` (`plan.go:609`, vSphere-only today, sourced from VMware Tools guest-info) would need an equivalent Nutanix Guest Tools-derived field — a separate, smaller inventory gap, not part of Tier 3's conversion-pod work | builds `PVCNameTemplateData` per disk, validates via shared `planbase.ValidatePVCNameTemplate` |

`MaintenanceMode` is a partial exception: vSphere checks a real
`Host.InMaintenanceMode` boolean, but the Nutanix `Host` model only has a
bare `State string` — closing this one requires mapping AHV host state
values to a boolean first (a small model/collector change, not just wiring),
and possibly confirming the Prism API even surfaces this per-host.

**Estimated effort:** small — a few days per method, mostly following the
vSphere pattern and reusing `planbase` helpers. This tier has no dependency
on the open questions, but does touch the same collector client Tier 0
migrates — see Proposed Phasing below for sequencing.

### Gap Tier 2: Feature parity

Gaps that are real and scoped, but smaller than Tiers 4–5:

- **Shared/excluded disk detection.** vSphere's `SharedDisks`/
  `ExcludedDisks` validators inspect real state (`disk.Shared`,
  `disk.BusAddress`); Nutanix hardcodes a pass (`validator.go:59-65`). The
  Nutanix `Disk` model has no `Shared` field and no `BusAddress`
  equivalent — only `AdapterType` + `DeviceIndex`, from which a composite
  identifier (e.g. `scsi:0`) could be synthesized, but `ExcludeDisks`
  plan-spec semantics currently assume the vSphere-style string format.
  Requires a small inventory model extension, not just wiring.
- **OS/Preference/Template mapping.** `TemplateLabels`/`PreferenceName`
  (`builder.go:803-806, 862-865`) are empty stubs. The data pipe already
  exists: `VM.GuestOSID`/`GuestOSVersion` are populated from AHV's
  `Spec.Resources.GuestOSID` and Nutanix Guest Tools
  (`container/nutanix/resource_vm.go:55,138-152`). This needs an AHV
  guest-ID → osinfo-ID mapping table analogous to vSphere's ~40-entry
  `osMap`, then wiring into the two builder methods. Low risk, self
  contained.
- **Tag/category → label/annotation mapping.**
  `SourceVMLabelsAndAnnotations` (`builder.go:886-889`) is a one-line TODO.
  Unlike earlier drafts of this document claimed, the data is already
  collected: `Categories map[string]string` on the `VM` struct is populated
  from the v3 VM entity's `metadata.categories`
  (`container/nutanix/resource_vm.go:40`). This is pure wiring work —
  build a sanitization/mapping pass analogous to vSphere's tag→label logic
  (`vsphere/builder.go:2861+`) and wire it into
  `SourceVMLabelsAndAnnotations`. **Estimated effort:** small.
- **Volume populators / storage offload.**
  `SupportsVolumePopulators()` returns `false` unconditionally; all
  transfer goes through CDI HTTP import of a temp catalog image, with no
  array-side clone/XCOPY-equivalent path. This has no obvious 1:1 Nutanix
  analogue (AHV has no XCOPY primitive); a CSI-native-clone populator would
  be the natural equivalent if/when destination storage backends support
  it. This is a new-feature-scale item, not a port, and should be scoped
  independently — see `vsphere-copy-offload-populator.md` for the pattern
  to follow.
- **Scheduler host-awareness.** vSphere's scheduler
  (`scheduler.go`, 326 lines) does per-host in-flight/pending tracking,
  disk-count-based cost, and shared-disk creator/consumer ordering.
  Nutanix's (76 lines) is a single global counter. Since AHV has no
  ESXi-style per-host object model in Forklift's inventory
  (`pkg/controller/host/handler/nutanix/handler.go` is a deliberate no-op),
  host-affinity scheduling for Nutanix would need new design work, not a
  straight port — it's unclear what "host" would even mean as a scheduling
  dimension for AHV clusters in this codebase today. Shared-disk-aware
  ordering, however, is portable once this tier's shared-disk detection
  lands.

**Estimated effort:** mixed and mostly independent per item — OS/Preference
mapping and category mapping are small, self-contained wiring tasks;
shared/excluded-disk detection is medium (needs the inventory model
extension noted above); volume populators and scheduler host-awareness are
both new-feature-scale design efforts, not ports, and should be scoped as
their own follow-on enhancements rather than estimated here.

### Gap Tier 3: Guest customization (conversion pod)

vSphere's disk copy for cold migrations can route through a virt-v2v
conversion pod (`PodEnvironment`, `builder.go:202-312` in the vSphere
adapter) that performs in-guest customization: virtio driver injection,
`/etc/fstab`/network config rewriting for static-IP preservation, and
LUKS/NBDE disk decryption. Nutanix has none of this — `PodEnvironment`
(`builder.go:822-824`) is a stub returning `nil, nil`, `ConversionPodConfig`
returns an empty result, and `builder.go:772` carries an explicit TODO:
*"remove this when Nutanix has a conversion step."*

**Practical consequence:** an AHV guest without virtio drivers pre-installed
will not boot on KubeVirt after a Forklift migration today. This is the
single biggest gap standing between "works in a lab with virtio-ready
images" and general production readiness.

virt-v2v itself is not inherently vSphere-specific — per
`docs/use-of-virt-v2v-in-forklift.md`, it supports an `-i disk` local-file
input mode (`virt-v2v -i disk disk.img -o kubevirt [...]`), which is
architecture-agnostic: it just needs a block device or file, not a vCenter
connection. That same source document describes this mode as "mainly
useful for testing" rather than a hardened production path, which weakens
(without ruling out) the case for reusing it here — Open Question #2's
virt-v2v spike should specifically assess whether `-i disk` is production-
ready or would itself need hardening as part of this work. Nutanix already
produces exactly that shape of artifact — a
disk image reachable over HTTP via the catalog-image mechanism
(`elementHTTPSource`/`centralHTTPSource` in `builder.go`). The open question
(#2 above) is whether that HTTP source can be presented to virt-v2v as a
block device via nbdkit's `curl` plugin (streaming, no full download) or
whether it requires downloading the full image into pod-local storage first
— which works but loses the "single copy" property CDI HTTP import
currently has, and would need a temp-storage strategy analogous to the
existing `ConversionTempStorageClass`/`ConversionTempStorageSize` plan
fields.

**Estimated effort:** large, and effort is not the main uncertainty — the
open question above is. This should start with a virt-v2v spike (Open
Question #2), not a full implementation commitment.

### Gap Tier 4: Warm migration / change tracking

vSphere's warm migration is a real precopy/checkpoint loop backed by CBT:
`GetSnapshotDeltas` calls VMware's `QueryChangedDiskAreas` API to fetch
changed disk regions between snapshot checkpoints. Nutanix's `client.go`
already declares the matching interface methods (`CreateSnapshot`,
`RemoveSnapshot`, `GetSnapshotDeltas`, `SetCheckpoints`,
`CheckSnapshotReady`, `CheckSnapshotRemove`, `client.go:268-297`), but every
one is a literal no-op today, and `Validator.WarmMigration()` returns
`false` unconditionally.

External research shows Nutanix does have a CBT-equivalent primitive: the
**v4 Changed Regions Tracking (CRT) API** (`dataprotection` namespace,
`compute-changed-regions` endpoint), which computes byte-range deltas
between two VM recovery points, or against an empty disk for a full-disk
baseline. The mechanical details below come from a single Nutanix blog post
and Nutanix's own solutions-portal pages (not independently verified against
a live API call or a second corroborating source), so treat specifics like
exact call counts, token lifetimes, and pagination limits as "as documented"
rather than confirmed by direct testing:

- Two-step call, as documented: a discovery call to Prism Central returns
  the target Prism Element IP and a short-lived (15-minute) JWT
  authorization token; the actual changed-region computation call goes to
  Prism Element.
- **Version requirement is not yet pinned down precisely.** Nutanix's
  January 2025 introductory blog post cites `pc.2024.3+`; the current v4
  API reference matrix lists the `dataprotection` namespace as reaching GA
  at PC 7.3 / AOS 7.3; community forum evidence shows the endpoint used
  against PC 7.5 / AOS 11.0.0.1. These are not necessarily contradictory —
  the capability may have shipped as EA/RC around PC 2024.3 and reached GA
  later — but the *specific* `compute-changed-regions` endpoint's GA status
  independent of the broader namespace has not been independently
  confirmed here. Nutanix's versioning scheme treats EA/RC as explicitly
  unsupported for production, so this must be resolved before committing
  to an implementation, not just before committing to a release date.
- Requires VM recovery points (snapshots) to exist first — but these can be
  created on-demand via the v4 `dataprotection` API's `CreateRecoveryPoint`
  action, independent of a configured protection policy (Nutanix DR/Leap is
  not a hard prerequisite for creating the recovery point itself, based on
  available documentation).
- Response is paginated, per the same documentation (up to 10,000 changed
  regions per call, with a `nextOffset` cursor) and includes `fileSize` for
  the source disk.
- Nutanix's own guidance for this API says *"If you are not a backup
  vendor, we recommend reaching out to your backup provider"* — this reads
  as targeting certified backup/DR partner integrations, which is Open
  Question #1 and needs to be resolved with Nutanix directly before
  committing engineering effort here.

**Sources consulted:**
[Nutanix v4 DR API Series Part 2: CBT and CRT](https://www.nutanix.dev/2025/01/15/nutanix-v4-disaster-recovery-api-series-part-2-changed-blocks-tracking-cbt-and-changed-regions-tracking-crt/),
[Create an On-Demand VM Recovery Point](https://portal.nutanix.com/page/documents/solutions/details?targetId=TN-2047-Data-Protection-for-AHV-Based-VMs:create-an-on-demand-vm-recovery-point.html),
[Create a Recovery Point for a VM (v4 API)](https://portal.nutanix.com/page/documents/solutions/details?targetId=TN-2209-Nutanix-v4-API-Backup-Solutions:create-a-recovery-point-for-a-vm.html),
[Nutanix v4 API Reference](https://www.nutanix.dev/api-reference-v4/),
[Nutanix v4 API Versioning Scheme and Types](https://www.nutanix.dev/nutanix-v4-api-versioning-scheme-and-types/),
[Nutanix Legacy API End-of-Life Announcement Bulletin (PDF)](https://download.nutanix.com/misc/LegacyAPI-EOLNotification.pdf),
[Nutanix AOS release/support lifecycle](https://endoflife.date/nutanix-aos).

If Forklift can obtain the necessary API access, the shape of a Nutanix
warm-migration precopy loop mirrors vSphere's directly: create an on-demand
recovery point → resolve changed regions against the previous recovery
point (or against empty for the first pass) → read only the changed byte
ranges from the corresponding catalog image/disk export → repeat on an
interval → on cutover, do a final delta pass, then power off the source and
finalize.

**Estimated effort:** unknown until Open Questions #1 and #4 are resolved;
treat as a research spike deliverable, not an estimated implementation.

### Gap Tier 5: Tooling, CLI, tests, docs

Parity work, not research:

- **Tests.** No `validator_test.go` (vSphere's is 692 lines with real
  cases), no `destinationclient_test.go`, no `scheduler_test.go`, no
  suite-level harness for Nutanix. Each Tier 0–4 item landing should ship
  its own tests rather than deferring this to a cleanup pass.
- **CLI (`kubectl-mtv`).** vSphere has a dedicated
  `create/provider/vsphere` package with its own interactive flow;
  Nutanix is routed through a generic path (`create/provider/create.go`
  calls `generic.CreateProvider(...)` after `validateNutanixOptions`). vSphere
  also has dedicated inventory listers (`get/inventory/datastores.go`,
  `networks.go`, `folders.go`, `datacenters.go`) with no Nutanix
  counterparts (some, like folders/datacenters, may be legitimately
  inapplicable to AHV's flatter topology).
- **Inventory web layer.** vSphere models folders/datacenters/tree/
  custom-fields as first-class inventory resources
  (`pkg/controller/provider/web/vsphere/{datacenter,datastore,folder,
  customfielddef,tree}.go`); none have a Nutanix equivalent. Some of this
  is architecturally inapplicable (AHV has no folder hierarchy); categories
  are already collected into the inventory model (see Tier 2) but not yet
  exposed as a first-class inventory web resource the way vSphere exposes
  custom fields.
- **Docs.** Zero mentions of "nutanix" anywhere under `docs/`, including
  all eight `docs/compatibility/*.md` feature matrices, which simply omit it
  as a row/column. There is no `nutanix-setup-guide.md` analogous to
  `docs/hyperv-setup-guide.md`. This should be corrected incrementally as
  each tier lands — e.g., add the compatibility-matrix row alongside Tier 1,
  add the setup guide once Tier 1 validation makes the provider trustworthy
  enough to document as supported.

**Estimated effort:** small per item, but numerous; treat as ongoing work
tracked alongside each other tier rather than a single deliverable (see
Proposed Phasing below).

### Security, Risks, and Mitigations

- **Silent misconfiguration today.** Because Tier 1 validators
  unconditionally pass, a plan with an unmapped network or storage
  container currently reaches `Ready` and only fails during execution,
  potentially after other VMs in the same plan have already started
  migrating. This is a correctness/UX risk, not a security risk, and is the
  primary justification for prioritizing Tier 1 early.
- **Guest customization gap has a security dimension.** Without LUKS/NBDE
  support (Tier 3), encrypted-disk VMs cannot be safely migrated at all
  today — the disk would import undecrypted or fail. This should be called
  out explicitly in user-facing docs as an unsupported case until Tier 3
  lands, rather than left implicit.
- **CRT API credentials/token handling.** If Tier 4 proceeds, the
  15-minute JWT tokens returned by the discovery call are short-lived
  bearer credentials scoped to a specific recovery point; they should be
  handled with the same care Forklift already applies to the Prism Central
  download-cookie secrets in `builder.go` (`ensureDownloadCookieSecret`),
  i.e. stored in a Kubernetes `Secret`, never logged, rotated per poll
  cycle rather than cached long-term.
- **Partner-program dependency.** If Open Question #1 resolves to "CRT
  requires a Nutanix backup-vendor partnership," that's a business/legal
  dependency external to engineering effort, and Tier 4 should be
  explicitly marked blocked rather than estimated until resolved.
- **Deferred legacy-API migration risk.** If Tier 0 is deprioritized behind
  the vSphere-parity tiers, the project risks reaching Nutanix's Q2 CY2027
  last-GA-release milestone (or the Q4 CY2027 start of phased removal)
  with core inventory collection still on deprecated APIs. This should be
  tracked on its own timeline independent of how the rest of the roadmap
  is prioritized (Open Question #5).

## Design Details

### Proposed phasing

| Phase | Scope | Depends on | Ships independently? |
|---|---|---|---|
| Phase 0 | Resolve Open Questions #1–#5 (Nutanix partner conversation, virt-v2v spike, category-API check, `compute-changed-regions` GA-status confirmation, Tier 0 sequencing decision) | — | Yes — pure research |
| Phase 1 | Tier 0 legacy API migration (v3/v2.0 → v4 for cluster/host/VM/subnet inventory and Prism Element image/storage-container handling) | — | Yes |
| Phase 2 | Tier 1 validator correctness + `validator_test.go` + compatibility-matrix docs update | Benefits from Phase 1 landing first (shares the same client code) but not strictly blocked on it | Yes |
| Phase 3 | Tier 2 items with no external dependency: OS/Preference mapping, shared/excluded-disk model extension + validation, category→label mapping | Not strictly blocked on Phase 2, but the shared/excluded-disk validator work benefits from landing after it (same test-fixture patterns) | Yes |
| Phase 4 | Tier 3 guest customization (conversion pod) | Phase 0's virt-v2v spike result | No — blocked on Phase 0 |
| Phase 5 | Tier 4 warm migration | Phase 0's Nutanix partner-access and version-confirmation results, and reuses Phase 4's disk-access patterns if any | No — blocked on Phase 0, likely also on Phase 4 |
| Ongoing | Tier 5 tooling/CLI/tests/docs | Tracks alongside each phase above | N/A |

Phases 1–3 should be scoped as normal `implementable` enhancements once
this document's tiering is agreed on; Phases 4 and 5 should remain
`provisional` until Phase 0 closes their respective open questions.

### Test Plan

- **Phase 1:** unit tests confirming v4 client calls produce equivalent
  inventory records to the current v3/v2.0 calls (regression-style,
  fixture-based); no behavioral change is intended, so test coverage
  should focus on parity, not new validation logic.
- **Phase 2:** unit tests per validator method against fixture inventory
  data (mirroring vSphere's `validator_test.go` structure — happy path,
  missing-mapping path, boundary sizes for `InvalidDiskSizes`).
- **Phase 3:** unit tests for OS-ID mapping table coverage; integration
  test migrating a VM with a shared disk to confirm the validator now
  rejects/flags it rather than silently passing.
- **Phase 4/5:** to be defined once feasibility is confirmed; expect at
  minimum an integration test against a real (or Nutanix-provided sandbox)
  Prism Central instance, since neither virt-v2v block-device streaming nor
  the CRT API can be meaningfully unit-tested against mocks alone.
- All phases: no existing Nutanix or vSphere test should regress; run
  `go test ./pkg/controller/plan/adapter/... ./pkg/controller/plan/scheduler/...`.

### Upgrade / Downgrade Strategy

Phase 1 changes the Prism API versions used internally but should be
behaviorally transparent to users — no CRD or plan-spec changes. No CRD
schema changes are required for Phase 2 or Phase 3 either — both are
adapter-internal logic changes plus (for Phase 3's shared-disk detection) a
non-breaking inventory model field addition, consistent with how vSphere
and oVirt already model these fields. Phase 4 (conversion pod) may require
new `PlanSpec` fields analogous to `ConversionTempStorageClass`/
`ConversionTempStorageSize`, which are additive and optional. Phase 5 (warm
migration) would reuse the existing `MigrationWarm` type and `Cutover`/
`VMCutover` fields already defined in `pkg/apis/forklift/v1beta1/{plan,
migration}.go` — no new API surface anticipated there either.

### Key Code Locations

| Component | File |
|---|---|
| Nutanix adapter wiring | `pkg/controller/plan/adapter/nutanix/adapter.go` |
| Nutanix builder (VM spec, DataVolumes) | `pkg/controller/plan/adapter/nutanix/builder.go` |
| Nutanix validator (target of Tier 1) | `pkg/controller/plan/adapter/nutanix/validator.go` |
| Nutanix client (snapshot stubs, target of Tier 4; legacy v3 usage, target of Tier 0) | `pkg/controller/plan/adapter/nutanix/client.go` |
| Nutanix v4 image/catalog handling | `pkg/controller/plan/adapter/nutanix/image_v4.go` |
| Nutanix inventory collector (target of Tier 0) | `pkg/controller/provider/container/nutanix/{client,prism,image_api,storage_api}.go` |
| Nutanix scheduler | `pkg/controller/plan/scheduler/nutanix/scheduler.go` |
| Nutanix inventory model | `pkg/controller/provider/model/nutanix/model.go` |
| vSphere validator (reference) | `pkg/controller/plan/adapter/vsphere/validator.go` |
| vSphere builder (reference) | `pkg/controller/plan/adapter/vsphere/builder.go` |
| vSphere scheduler (reference) | `pkg/controller/plan/scheduler/vsphere/scheduler.go` |
| virt-v2v conversion-pod architecture | `docs/use-of-virt-v2v-in-forklift.md` |
| Shared `planbase` validation helpers | `pkg/controller/plan/adapter/base/` |
| Compatibility matrices (docs gap) | `docs/compatibility/*.md` |
| Nutanix legacy API EOL bulletin | `https://download.nutanix.com/misc/LegacyAPI-EOLNotification.pdf` |

## Implementation History

- 2026-09-02 — Initial gap analysis and phased roadmap drafted.
- 2026-09-02 — Added Tier 0 (legacy Prism API deprecation) after
  discovering Nutanix's dated EOL bulletin for v0.8/v1/v2/v3 APIs, which
  the current inventory collector and Prism Element disk-transfer path
  depend on; corrected the warm-migration version-requirement claim
  (previously stated as a settled `pc.2024.3+` fact) to reflect conflicting
  evidence across sources, now tracked as Open Question #4.
- 2026-09-02 — Grounded Open Question #4 in Nutanix's published AOS
  release/support lifecycle; flagged AOS 7.5/7.6 as the realistic test
  target since AOS 7.3 (the commonly-cited GA floor) exits active
  maintenance almost immediately.
- 2026-09-02 — Adversarial review pass: corrected a false claim (repeated
  in three sections) that Nutanix categories are uncollected — they are
  (`resource_vm.go:40`), closing Open Question #3 and reclassifying
  category→label mapping as low-risk wiring rather than needing new
  inventory work. Also fixed a wrong interface-method count (17→20), a
  wrong vSphere test line count (732→692), a wrong compatibility-doc count
  (six→eight), three tier-number cross-reference errors introduced by
  earlier renumbering passes, an unsubstantiated phase-dependency claim,
  and added hedging to CRT API mechanical details and the virt-v2v `-i
  disk` production-readiness caveat where confidence had outrun evidence.

## Drawbacks

- This is a large, multi-release effort; splitting it across six tiers
  and six phases risks the project shipping Phase 1 or 2 and then
  deprioritizing the rest indefinitely, leaving Nutanix permanently at
  "cold migration, no guest customization" maturity. Phase sequencing
  should be revisited at each phase boundary rather than treated as a
  fire-and-forget backlog.
- Phases 4 and 5 depend on external factors (Nutanix partner access, an
  unproven virt-v2v integration path, an unconfirmed API GA status) that
  engineering cannot unilaterally resolve, which makes them poor candidates
  for hard release commitments until Phase 0 concludes.
- Tier 0's timeline is set by Nutanix, not this project; if Phase 0's
  research is wrong about the runway (e.g. if Nutanix accelerates the
  schedule in a future bulletin revision, as it already revised once
  between the Dec 2024 and June 2026 versions), Phase 1 may need to be
  pulled forward on short notice.

## Alternatives

1. **Skip guest customization; document virtio-driver pre-installation as a
   hard migration prerequisite.** Lower engineering cost than Phase 4, but
   pushes real operational burden onto users and diverges from vSphere's
   UX, where drivers are injected automatically. Reasonable as an interim
   position while Phase 0's virt-v2v spike is pending, not as a permanent
   substitute.
2. **Pursue array/CSI-level offload (Tier 2's populator gap) before warm
   migration**, on the theory that large-scale migrations benefit more from
   faster cold-copy than from warm-migration's reduced cutover window. This
   re-orders Phase 4/5 relative to Tier 2's populator item; worth revisiting
   once Phase 0 clarifies warm migration's actual feasibility and cost.
3. **Treat warm migration as permanently out of scope** if Phase 0
   confirms the CRT API is not available to Forklift without a Nutanix
   partnership Forklift's maintainers are unwilling or unable to pursue, or
   that `compute-changed-regions` remains EA/RC indefinitely. In that case,
   Phase 5 should be formally marked `rejected` (per this template's status
   vocabulary) rather than left `provisional` indefinitely.
4. **Defer Tier 0 entirely until closer to Nutanix's Q2 CY2027 milestone.**
   Cheaper in the short term, but risks a late scramble, and means every
   other tier's client-layer work done in the interim gets built against
   APIs slated for removal, likely requiring rework.

## Infrastructure Needed

- Access to a Nutanix Prism Central environment on **AOS/PC 7.5 or 7.6**
  for API research and later integration testing (Phase 0 onward). AOS 7.3
  is the version most often cited as the `dataprotection`/`vmm`/
  `clustermgmt` v4 GA floor, but it exits active maintenance Sep 30, 2026
  (essentially immediately relative to this document); 7.5/7.6 have
  multi-year maintenance runway and are the more realistic target for any
  implementation that would ship after Phase 0 completes.
- A contact point at Nutanix (partner engineering or API support) to
  resolve Open Questions #1 and #4 before Phase 5 can be scoped.
