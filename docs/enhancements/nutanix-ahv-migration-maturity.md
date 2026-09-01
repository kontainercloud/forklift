---
title: nutanix-ahv-migration-maturity
authors:
  - "Tamal Saha"
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
   (partner/API access, licensing, support commitments) before Phase 3
   (warm migration) can be scoped as `implementable`. See
   [Warm migration / CBT feasibility](#gap-tier-3-warm-migration--change-tracking).
2. **Can virt-v2v's `-i disk` input mode run against a network-attached
   block device (nbdkit `curl`/`ssh` plugin) rather than a fully-downloaded
   local file?** This determines whether Phase 2 (conversion pod) can reuse
   the existing per-disk catalog-image HTTP endpoint directly, or whether it
   requires downloading the full image to local/ephemeral storage first
   (which would regress the "no double-copy" property CDI HTTP import
   currently has for cold migrations). Needs a virt-v2v spike, not just a
   documentation read.
3. **Are Nutanix categories exposed via an API endpoint Forklift's collector
   isn't calling yet, or does the Prism API not expose per-VM category
   assignment at all in the version/edition this codebase targets?** The
   inventory model (`pkg/controller/provider/model/nutanix/model.go`) has no
   `Categories` field, and no code in
   `pkg/controller/provider/container/nutanix/*.go` requests category data,
   so this is currently unverified rather than confirmed absent.
4. What is the actual minimum supported Prism Central version for this
   provider today? The CRT API requires `pc.2024.3+`; if Forklift's Nutanix
   support matrix intends to cover older Prism Central/Element deployments,
   warm migration would need to be gated behind a Prism version check,
   mirroring how oVirt's direct-LUN support is gated behind
   `engine >= 4.5.2.1` (see `ovirt-lun-migration.md`).

## Summary

Forklift's Nutanix AHV source provider (`pkg/controller/plan/adapter/nutanix`)
migrates powered-off VMs end-to-end today: it maps CPU/memory/firmware/disks/
network into a KubeVirt `VirtualMachineSpec`, exports each disk to a
per-VM catalog image via Nutanix's Image Service, and imports it into a PVC
through CDI's HTTP `DataVolume` importer. This is a working cold-migration
path, but it is materially less mature than the vSphere adapter, which is the
project's most-hardened provider and the de facto maturity bar. This document
inventories every capability gap between the two adapters, backed by direct
code comparison and (for the two gaps whose feasibility isn't decidable from
this codebase alone) external research into Nutanix's own API surface. It
proposes a phased roadmap to close the gaps, ordered by risk/effort and by
what blocks what.

## Motivation

Nutanix AHV is currently marketed as a supported Forklift source provider,
but several of its `Validator` methods are unconditional pass-through stubs,
it has no unit test coverage for validation logic, it silently skips guest
customization (driver injection, static-IP preservation) that vSphere
performs via virt-v2v, and it is entirely absent from the project's own
`docs/compatibility/*.md` feature matrices. A user migrating a Windows VM
without pre-installed virtio drivers, or a VM with an unmapped subnet, gets
no early warning today — the plan validates as "ready" and then fails (or
worse, silently produces an unbootable VM) during execution.

### Goals

- Enumerate every concrete behavioral gap between the Nutanix and vSphere
  adapters, with file/line evidence, not impressions.
- Distinguish gaps that are pure "wire it up" work (data already collected,
  logic already exists as a reusable helper) from gaps that require new
  inventory data, new Nutanix API integration, or upstream virt-v2v changes.
- Resolve (or explicitly flag as needing external validation) the two open
  feasibility questions that block realistic planning: warm migration and
  guest customization.
- Propose a phased delivery plan with dependencies made explicit, so later
  phases aren't blocked on speculative feasibility of earlier ones.

### Non-Goals

- This document does not propose closing every gap in one release. It is a
  roadmap, not a single implementation plan.
- It does not commit to warm migration shipping — Phase 3 is gated on the
  open questions above and may conclude "not currently feasible."
- It does not cover UI/console work (the `forklift-console-plugin` repo is
  out of scope here); this document is scoped to the `kubevirt/forklift`
  controller, CLI, and inventory layers.
- It does not redesign the existing cold-migration disk-transfer mechanism
  (catalog image + CDI HTTP import), which works today and is out of scope
  except where guest customization (Phase 2) requires touching it.

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
- **Validator** (`validator.go`, 104 lines): 17 interface methods, the
  majority of which are hardcoded to always pass.
- **Scheduler** (`pkg/controller/plan/scheduler/nutanix/scheduler.go`, 76
  lines): a single global `MaxInFlight` counter, no host-awareness.
- **Host handler** (`pkg/controller/host/handler/nutanix/handler.go`): an
  explicit no-op, on the reasoning that AHV has no ESXi-style per-host
  object model.
- **Inventory model** (`pkg/controller/provider/model/nutanix/model.go`):
  `VM` has `GuestOSID`, `GuestOSVersion`, `GuestToolsEnabled/Mounted/
  Reachable/Version`, `Categories map[string]string` (present in the struct
  but unpopulated by any collector code found), `NICs`, `Disks`,
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

The gaps are grouped into five tiers, ordered by a combination of risk,
effort, and whether the data/API needed to close them is already available.

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
| `PVCNameTemplate` | `validator.go:93-96`, stub | `Disk.UUID` covers the base case; `WinDriveLetter` needs guest-agent data (see Tier 4) | builds `PVCNameTemplateData` per disk, validates via shared `planbase.ValidatePVCNameTemplate` |

`MaintenanceMode` is a partial exception: vSphere checks a real
`Host.InMaintenanceMode` boolean, but the Nutanix `Host` model only has a
bare `State string` — closing this one requires mapping AHV host state
values to a boolean first (a small model/collector change, not just wiring),
and possibly confirming the Prism API even surfaces this per-host.

**Estimated effort:** small — a few days per method, mostly following the
vSphere pattern and reusing `planbase` helpers. This tier should ship first
and independently of every other tier; it has no dependency on open
questions.

### Gap Tier 2: Guest customization (conversion pod)

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
connection. Nutanix already produces exactly that shape of artifact — a
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

### Gap Tier 3: Warm migration / change tracking

vSphere's warm migration is a real precopy/checkpoint loop backed by CBT:
`GetSnapshotDeltas` calls VMware's `QueryChangedDiskAreas` API to fetch
changed disk regions between snapshot checkpoints. Nutanix's `client.go`
already declares the matching interface methods (`CreateSnapshot`,
`RemoveSnapshot`, `GetSnapshotDeltas`, `SetCheckpoints`,
`CheckSnapshotReady`, `CheckSnapshotRemove`, `client.go:268-297`), but every
one is a literal no-op today, and `Validator.WarmMigration()` returns
`false` unconditionally.

External research (not visible from this codebase) shows Nutanix does have
a CBT-equivalent primitive: the **v4 Changed Regions Tracking (CRT) API**
(`dataprotection/v4.1`, `compute-changed-regions` endpoint), which computes
byte-range deltas between two VM recovery points, or against an empty disk
for a full-disk baseline. Key facts from Nutanix's own developer
documentation:

- Two-step call: a discovery call to Prism Central returns the target Prism
  Element IP and a short-lived (15-minute) JWT authorization token; the
  actual changed-region computation call goes to Prism Element.
- Requires **Prism Central pc.2024.3 or later**.
- Requires VM recovery points (snapshots) to exist first — but these can be
  created on-demand via the v4 `dataprotection` API's `CreateRecoveryPoint`
  action, independent of a configured protection policy (Nutanix DR/Leap is
  not a hard prerequisite for creating the recovery point itself, based on
  available documentation).
- Response is paginated (up to 10,000 changed regions per call, with a
  `nextOffset` cursor) and includes `fileSize` for the source disk.
- Nutanix's own guidance for this API says *"If you are not a backup
  vendor, we recommend reaching out to your backup provider"* — this reads
  as targeting certified backup/DR partner integrations, which is Open
  Question #1 and needs to be resolved with Nutanix directly before
  committing engineering effort here.

**Sources consulted:**
[Nutanix v4 DR API Series Part 2: CBT and CRT](https://www.nutanix.dev/2025/01/15/nutanix-v4-disaster-recovery-api-series-part-2-changed-blocks-tracking-cbt-and-changed-regions-tracking-crt/),
[Create an On-Demand VM Recovery Point](https://portal.nutanix.com/page/documents/solutions/details?targetId=TN-2047-Data-Protection-for-AHV-Based-VMs:create-an-on-demand-vm-recovery-point.html),
[Create a Recovery Point for a VM (v4 API)](https://portal.nutanix.com/page/documents/solutions/details?targetId=TN-2209-Nutanix-v4-API-Backup-Solutions:create-a-recovery-point-for-a-vm.html).

If Forklift can obtain the necessary API access, the shape of a Nutanix
warm-migration precopy loop mirrors vSphere's directly: create an on-demand
recovery point → resolve changed regions against the previous recovery
point (or against empty for the first pass) → read only the changed byte
ranges from the corresponding catalog image/disk export → repeat on an
interval → on cutover, do a final delta pass, then power off the source and
finalize.

**Estimated effort:** unknown until Open Question #1 is resolved; treat as
a research spike deliverable, not an estimated implementation.

### Gap Tier 4: Feature parity

Gaps that are real and scoped, but smaller than Tiers 2–3:

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
  Nutanix's `Categories map[string]string` field exists on the `VM` struct
  but no collector code currently populates it (Open Question #3) — this
  needs an inventory-collection check before it can be scoped as a pure
  wiring task.
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
  ordering, however, is portable once Tier 4's shared-disk detection lands.

### Gap Tier 5: Tooling, CLI, tests, docs

Parity work, not research:

- **Tests.** No `validator_test.go` (vSphere's is 732 lines with real
  cases), no `destinationclient_test.go`, no `scheduler_test.go`, no
  suite-level harness for Nutanix. Each Tier 1–4 item landing should ship
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
  is architecturally inapplicable (AHV has no folder hierarchy); categories/
  projects as first-class inventory resources would be the meaningful
  equivalent, contingent on Open Question #3.
- **Docs.** Zero mentions of "nutanix" anywhere under `docs/`, including
  all six `docs/compatibility/*.md` feature matrices, which simply omit it
  as a row/column. There is no `nutanix-setup-guide.md` analogous to
  `docs/hyperv-setup-guide.md`. This should be corrected incrementally as
  each tier lands — e.g., add the compatibility-matrix row alongside Tier 1,
  add the setup guide once Tier 1 validation makes the provider trustworthy
  enough to document as supported.

### Security, Risks, and Mitigations

- **Silent misconfiguration today.** Because Tier 1 validators
  unconditionally pass, a plan with an unmapped network or storage
  container currently reaches `Ready` and only fails during execution,
  potentially after other VMs in the same plan have already started
  migrating. This is a correctness/UX risk, not a security risk, and is the
  primary justification for prioritizing Tier 1 first.
- **Guest customization gap has a security dimension.** Without LUKS/NBDE
  support (Tier 2), encrypted-disk VMs cannot be safely migrated at all
  today — the disk would import undecrypted or fail. This should be called
  out explicitly in user-facing docs as an unsupported case until Tier 2
  lands, rather than left implicit.
- **CRT API credentials/token handling.** If Tier 3 proceeds, the
  15-minute JWT tokens returned by the discovery call are short-lived
  bearer credentials scoped to a specific recovery point; they should be
  handled with the same care Forklift already applies to the Prism Central
  download-cookie secrets in `builder.go` (`ensureDownloadCookieSecret`),
  i.e. stored in a Kubernetes `Secret`, never logged, rotated per poll
  cycle rather than cached long-term.
- **Partner-program dependency.** If Open Question #1 resolves to "CRT
  requires a Nutanix backup-vendor partnership," that's a business/legal
  dependency external to engineering effort, and Tier 3 should be
  explicitly marked blocked rather than estimated until resolved.

## Design Details

### Proposed phasing

| Phase | Scope | Depends on | Ships independently? |
|---|---|---|---|
| Phase 0 | Resolve Open Questions #1–#4 (Nutanix partner conversation, virt-v2v spike, category-API check, min PC version decision) | — | Yes — pure research |
| Phase 1 | Tier 1 validator correctness + `validator_test.go` + compatibility-matrix docs update | — | Yes |
| Phase 2 | Tier 4 items with no external dependency: OS/Preference mapping, shared/excluded-disk model extension + validation | Phase 1 (shares validator test patterns) | Yes |
| Phase 3 | Tier 2 guest customization (conversion pod) | Phase 0's virt-v2v spike result | No — blocked on Phase 0 |
| Phase 4 | Tier 3 warm migration | Phase 0's Nutanix partner-access result, and reuses Phase 3's disk-access patterns if any | No — blocked on Phase 0, likely also on Phase 3 |
| Ongoing | Tier 5 tooling/CLI/tests/docs | Tracks alongside each phase above | N/A |

Phases 1 and 2 should be scoped as normal `implementable` enhancements once
this document's tiering is agreed on; Phases 3 and 4 should remain
`provisional` until Phase 0 closes their respective open questions.

### Test Plan

- **Phase 1:** unit tests per validator method against fixture inventory
  data (mirroring vSphere's `validator_test.go` structure — happy path,
  missing-mapping path, boundary sizes for `InvalidDiskSizes`).
- **Phase 2:** unit tests for OS-ID mapping table coverage; integration
  test migrating a VM with a shared disk to confirm the validator now
  rejects/flags it rather than silently passing.
- **Phase 3/4:** to be defined once feasibility is confirmed; expect at
  minimum an integration test against a real (or Nutanix-provided sandbox)
  Prism Central instance, since neither virt-v2v block-device streaming nor
  the CRT API can be meaningfully unit-tested against mocks alone.
- All phases: no existing Nutanix or vSphere test should regress; run
  `go test ./pkg/controller/plan/adapter/... ./pkg/controller/plan/scheduler/...`.

### Upgrade / Downgrade Strategy

No CRD schema changes are required for Phase 1 or Phase 2 — both are
adapter-internal logic changes plus (for Phase 2's shared-disk detection) a
non-breaking inventory model field addition, consistent with how vSphere
and oVirt already model these fields. Phase 3 (conversion pod) may require
new `PlanSpec` fields analogous to `ConversionTempStorageClass`/
`ConversionTempStorageSize`, which are additive and optional. Phase 4 (warm
migration) would reuse the existing `MigrationWarm` type and `Cutover`/
`VMCutover` fields already defined in `pkg/apis/forklift/v1beta1/{plan,
migration}.go` — no new API surface anticipated there either.

### Key Code Locations

| Component | File |
|---|---|
| Nutanix adapter wiring | `pkg/controller/plan/adapter/nutanix/adapter.go` |
| Nutanix builder (VM spec, DataVolumes) | `pkg/controller/plan/adapter/nutanix/builder.go` |
| Nutanix validator (target of Tier 1) | `pkg/controller/plan/adapter/nutanix/validator.go` |
| Nutanix client (snapshot stubs, target of Tier 3) | `pkg/controller/plan/adapter/nutanix/client.go` |
| Nutanix v4 image/catalog handling | `pkg/controller/plan/adapter/nutanix/image_v4.go` |
| Nutanix scheduler | `pkg/controller/plan/scheduler/nutanix/scheduler.go` |
| Nutanix inventory model | `pkg/controller/provider/model/nutanix/model.go` |
| Nutanix inventory collector | `pkg/controller/provider/container/nutanix/{collector,resource_vm,prism}.go` |
| vSphere validator (reference) | `pkg/controller/plan/adapter/vsphere/validator.go` |
| vSphere builder (reference) | `pkg/controller/plan/adapter/vsphere/builder.go` |
| vSphere scheduler (reference) | `pkg/controller/plan/scheduler/vsphere/scheduler.go` |
| virt-v2v conversion-pod architecture | `docs/use-of-virt-v2v-in-forklift.md` |
| Shared `planbase` validation helpers | `pkg/controller/plan/adapter/base/` |
| Compatibility matrices (docs gap) | `docs/compatibility/*.md` |

## Implementation History

- 2026-09-02 — Initial gap analysis and phased roadmap drafted.

## Drawbacks

- This is a large, multi-release effort; splitting it across five tiers
  and four phases risks the project shipping Phase 1 and then
  deprioritizing Phases 2–4 indefinitely, leaving Nutanix permanently at
  "cold migration, no guest customization" maturity. Phase sequencing
  should be revisited at each phase boundary rather than treated as a
  fire-and-forget backlog.
- Phases 3 and 4 depend on external factors (Nutanix partner access, an
  unproven virt-v2v integration path) that engineering cannot unilaterally
  resolve, which makes them poor candidates for hard release commitments
  until Phase 0 concludes.

## Alternatives

1. **Skip guest customization; document virtio-driver pre-installation as a
   hard migration prerequisite.** Lower engineering cost than Phase 3, but
   pushes real operational burden onto users and diverges from vSphere's
   UX, where drivers are injected automatically. Reasonable as an interim
   position while Phase 0's virt-v2v spike is pending, not as a permanent
   substitute.
2. **Pursue array/CSI-level offload (Tier 4's populator gap) before warm
   migration**, on the theory that large-scale migrations benefit more from
   faster cold-copy than from warm-migration's reduced cutover window. This
   re-orders Phase 3/4 relative to Tier 4's populator item; worth revisiting
   once Phase 0 clarifies warm migration's actual feasibility and cost.
3. **Treat warm migration as permanently out of scope** if Phase 0
   confirms the CRT API is not available to Forklift without a Nutanix
   partnership Forklift's maintainers are unwilling or unable to pursue. In
   that case, Phase 4 should be formally marked `rejected` (per this
   template's status vocabulary) rather than left `provisional`
   indefinitely.

## Infrastructure Needed

- Access to a Nutanix Prism Central environment on `pc.2024.3+` for API
  research and later integration testing (Phase 0 onward).
- A contact point at Nutanix (partner engineering or API support) to
  resolve Open Question #1 before Phase 4 can be scoped.
