---
title: hyperv-migration-maturity
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
  - "/enhancements/shared-disks.md"
---

# Hyper-V Migration: Reaching vSphere Parity

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] User-facing documentation is created

## Open Questions

1. **Which of Hyper-V's three RCT consumption paths should Forklift build
   on for warm migration?** Microsoft documents three ways to read
   Resilient Change Tracking data (see
   [Tier 1](#gap-tier-1-warm-migration--resilient-change-tracking-rct)):
   WMI Export (remote-friendly, but Hyper-V materializes a full delta VHD
   server-side before handoff), the `QueryChangesVirtualDisk` Win32 API
   (byte-range-efficient, but **local-only** — Microsoft's own docs state
   it "can only be accessed locally" and cannot read shared VHDs), and the
   Remote Shared Virtual Disk Protocol (shared-VHDX-specific, niche). Given
   Forklift's existing architecture is 100% remote (WinRM from the
   controller, no host-side agent), the Win32 API path would require a new
   architectural component — a native helper running on the Hyper-V host —
   that doesn't exist today. This needs a decision before implementation:
   accept the WMI Export path's data-volume overhead, or invest in a
   host-side agent to unlock the efficient Win32 path. **Either choice
   still leaves the same open sub-problem** (see Tier 1's revised scope):
   there is no existing mechanism in this codebase to apply a changed-region
   delta onto an already-populated target PVC — CDI's checkpoint-aware
   VDDK importer (which solves this for vSphere) has no Hyper-V analogue,
   since Hyper-V's DataVolumes are `Blank`, filled once by virt-v2v. This
   delta-ingestion design question should be scoped and answered before
   either RCT-consumption path is committed to.
2. **Does RCT actually have a minimum VM configuration version gate, and
   if so, what is it?** Secondary sources (blog posts, vendor forum
   threads) claim VM configuration version 8.0+ is required. This claim
   does **not** hold up against Microsoft's own authoritative documentation:
   Microsoft's VM-configuration-version feature matrix
   (`learn.microsoft.com/.../upgrade-virtual-machine-version-in-hyper-v-on-windows-or-windows-server`,
   fetched and checked directly — table titled "minimum virtual machine
   configuration version required to use some Hyper-V features") lists 20
   named features from configuration version 5.0 through 12.0 and does
   **not** mention Resilient Change Tracking, changed-block tracking, or
   backup/reference-points anywhere. Either RCT has no formal
   configuration-version gate, or the gate exists but isn't listed in that
   particular table — this is genuinely unresolved, not settled fact, and
   should be treated as such everywhere it's referenced in this document
   until confirmed against a real down-level VM (Open Question #1's lab
   access) or Microsoft's `Get-VHD`/RCT-specific documentation directly.
   Regardless of the answer, `Disk.RCTEnabled`'s `Get-VHD`-based `RctId`
   check should still correctly report RCT as unavailable on any VM where
   it isn't actually engaged, independent of the reason.
3. **Is Shared VHDX / passthrough-disk support (Tier 3) worth the
   collector work given how niche guest clustering is** in Hyper-V
   deployments Forklift customers are likely to have? Unlike Nutanix's
   analogous gap (where shared/excluded-disk detection closes a real,
   commonly-hit validation blind spot), Shared VHDX is specifically a
   guest-clustering feature (SQL Server / Scale-Out File Server guest
   clusters). Worth sizing actual customer demand before committing
   collector + validator + builder work here.
4. **Two separable questions for the host-handler gap (Tier 2):** (a) is
   porting the vSphere handler pattern for Hyper-V `Host` CR-freshness
   alone worth doing, given it doesn't by itself touch Plan behavior; and
   (b) independent of that, should a cluster node in `Paused`/`Down` state
   actually **block** migration execution rather than only warn, which
   today is true for vSphere too (`MaintenanceMode` is a `CategoryWarn`
   condition everywhere, not just for Hyper-V) — this is a product
   decision, not something this document can resolve unilaterally. See
   Tier 2 for the corrected mechanics.

## Summary

Forklift's Hyper-V source provider (`pkg/controller/plan/adapter/hyperv`)
is substantially more mature than the project's other non-vSphere
providers: it already routes disk transfer through a real virt-v2v
conversion pod (SMB-mounted VHDX files read directly by virt-v2v into a
blank DataVolume, not a CDI HTTP import), already preserves static IPs and
picks KubeVirt templates from virt-v2v-detected guest OS, already has a
`Validator` with real logic instead of stubs for most methods, already
supports Windows failover clusters (multi-node routing over WinRM), and
already has a working end-to-end setup guide (`docs/hyperv-setup-guide.md`)
and full compatibility-matrix coverage in `docs/compatibility/*.md`. This
document does the same systematic gap analysis performed for the Nutanix
adapter (companion design doc, not yet merged as of this writing:
`kontainercloud/forklift#1`), but the findings are different in kind:
rather than a broad list
of unimplemented stubs, the standout finding is that **the collector
already gathers and tests Resilient Change Tracking (RCT) status per disk
— clearly built in anticipation of warm migration — but nothing downstream
in the adapter ever reads it.** The rest of the gaps are smaller and more
targeted: a host-handler no-op that contradicts the codebase's own
cluster-awareness, an uncollected shared-disk/passthrough-disk concept,
thinner test coverage than vSphere, and some CLI/tooling parity items.

## Motivation

Hyper-V is close enough to production-grade that the natural next step
isn't "make the basics work" (as with Nutanix) but "finish what's already
half-built." The clearest example: `Disk.RCTEnabled` is a real,
actively-collected, cache-preserved, unit-tested field
(`model/hyperv/model.go:157`, `client_test.go` has ~15 assertions on it)
that nothing in `Validator`, `Client`, or `Builder` ever consumes. Someone
built the detection layer specifically anticipating warm migration and
then the work stopped there. Surfacing this clearly, plus the smaller
correctness and coverage gaps, gives the project a concrete, mostly
low-risk punch list rather than a ground-up build.

### Goals

- Identify every place Hyper-V diverges from vSphere-level maturity, with
  file/line evidence.
- Give particular weight to gaps where infrastructure already exists but
  is unused — these are the cheapest, highest-leverage items to close.
- Resolve (or flag as needing a decision) the open questions blocking
  warm-migration feasibility, since Microsoft's own API documentation
  gives a much clearer answer here than was available for Nutanix.
- Flag places where an apparent gap is actually architecturally
  inapplicable, so effort isn't wasted chasing non-gaps.

### Non-Goals

- This does not commit to implementing warm migration — Open Question #1
  must be resolved first, and the answer could reasonably be "not worth
  the host-agent investment."
- Does not cover UI/console work (`forklift-console-plugin` is a separate
  repo).
- Does not revisit the disk-transfer architecture itself (SMB CSI mount +
  virt-v2v conversion pod), which already works and is not a gap.
- Does not propose adding tag/category mapping or volume populators —
  investigation below found both architecturally inapplicable to Hyper-V
  as currently designed, not merely unimplemented.

## Background

### Current Hyper-V migration architecture

- **Inventory collection** (`pkg/controller/provider/container/hyperv`):
  WinRM + PowerShell (`pkg/lib/hyperv/driver/winrm.go`,
  `pkg/lib/hyperv/powershell/scripts.go`) against the Hyper-V host or a
  failover-cluster node, using batched `Get-VM`/`Get-VHD`/WMI KVP-exchange
  queries (`BatchGetVMDetails`, `BatchGetVMHardware`,
  `BatchGetVHDCapacity`).
- **`HyperVProviderServer`** (`pkg/controller/hyperv/*`,
  `cmd/hyperv-provider-server`): a separate CR/controller that provisions
  a static PV/PVC via the SMB CSI driver, mounting the Hyper-V host's SMB
  share (containing `.vhdx` files) into the cluster, plus a small sidecar
  that validates expected disk paths exist under the mount.
- **Builder** (`builder.go`): maps CPU/memory/firmware/TPM/disks/network;
  `DataVolumes` creates **blank** DataVolumes; `PodEnvironment`
  (`builder.go:569-611`) feeds a virt-v2v conversion pod `V2V_diskPath`
  (SMB-mounted VHDX paths), `V2V_source=hyperv`, `V2V_staticIPs`
  (per-NIC static-IP configuration derived from KVP-exchange guest network
  data), and `V2V_firmware`. `TemplateLabels` (`builder.go:450-485`)
  prefers the OS virt-v2v itself detected during conversion, falling back
  to KVP-exchange `GuestOS`.
- **Validator** (`validator.go`): real logic for `StorageMapped`,
  `NetworksMapped`, `MaintenanceMode` (cluster-node-state-aware),
  `NICNetworkRefs`, `StaticIPs`, `MacConflicts`, `InvalidDiskSizes`,
  `PVCNameTemplate`, `HasSnapshot` (flags pre-existing checkpoints).
  No-ops: `DirectStorage`, `UdnStaticIPs`, `SharedDisks`, `ExcludedDisks`,
  `ChangeTrackingEnabled`, `PowerState`, `VMMigrationType`,
  `GuestToolsInstalled`, `ConsolidationNeeded`, and — the headline finding
  — `WarmMigration` (`false`).
- **Client** (`client.go`): real WinRM-backed `PowerState`/`PowerOff`,
  cluster-aware (`IsHyperVCluster()`, `RunOnNode` routes power operations
  to the VM's owning node). Warm-migration interface methods
  (`CreateSnapshot`, `RemoveSnapshot`, `CheckSnapshotReady`,
  `CheckSnapshotRemove`, `SetCheckpoints`, `GetSnapshotDeltas`) are all
  no-op stubs.
- **Scheduler** (`pkg/controller/plan/scheduler/hyperv/scheduler.go`, 117
  lines, no test file): per-host in-flight tracking keyed by `vm.Host`,
  correctly accounting for failover-cluster node placement — a capability
  Nutanix's scheduler entirely lacks. No disk-count-based cost or
  shared-disk ordering.
- **Host handler** (`pkg/controller/host/handler/hyperv/handler.go`, 14
  lines): explicit no-op — see Tier 2 for why this is inconsistent with
  the rest of the adapter.
- **Rego validation policies**
  (`validation/policies/io/konveyor/forklift/hyperv/`): 12 substantive
  policies covering checkpoints, cluster role, disk count/size/SMB path,
  integration services, IP state, name, unresolved networks, secure boot,
  TPM, and guest OS.
- **Docs**: `docs/hyperv-setup-guide.md` (full CLI walkthrough) and a
  column in every `docs/compatibility/*.md` matrix, plus documented
  `forkliftcontroller_hyperv_*` settings
  (`forkliftcontroller-settings.md:99-100,156`).
- **CLI (`kubectl-mtv`)**: dedicated `create/provider/hyperv` package
  (parity with vSphere, unlike Nutanix's generic-path routing); all four
  `create/plan/{network,storage}/{fetchers,mapper}/hyperv` files present.

### vSphere as the maturity baseline

See `nutanix-ahv-migration-maturity.md`'s Background section for the full
vSphere feature inventory (warm migration via CBT, conversion pod with
guest customization, real validator, host-capacity-aware scheduler with
shared-disk ordering, volume-populator storage offload, tag→label mapping,
full test suite). Hyper-V already matches vSphere on guest customization
and most validator logic — the gaps below are narrower than Nutanix's.

## Proposal

### Gap Tier 1: Warm migration / Resilient Change Tracking (RCT)

This is the standout finding of this review. `Disk.RCTEnabled bool`
(`model/hyperv/model.go:157`, comment: *"Resilient Change Tracking for
warm migration"*) is populated by real, tested PowerShell:
`GetDiskRCTEnabled` (`powershell/scripts.go:162-167`) runs `Get-VHD` and
checks `$vhd.RctId`; the batch collection scripts
(`BatchGetVMDetails`/`BatchGetVMHardware`/`BatchGetVHDCapacity`,
`scripts.go:215,232,235`) populate it inline for every disk on every VM in
one round-trip; `container/hyperv/client.go` has dedicated
enrichment/caching logic preserving the value across refresh cycles
(`client.go:1295`); `client_test.go` has roughly 15 assertions covering
it. **Someone built this specifically to support warm migration, and nothing
downstream ever reads it** — grep across
`pkg/controller/plan/adapter/hyperv/*.go` for RCT/replication/changed-block
terms returns zero hits. `Client.CreateSnapshot/RemoveSnapshot/
CheckSnapshotReady/CheckSnapshotRemove/SetCheckpoints/GetSnapshotDeltas`
are all literal no-ops, and `Validator.WarmMigration()` is hardcoded
`false`.

External research into Microsoft's actual RCT API surface
(`learn.microsoft.com`) found a real, well-documented CBT-equivalent, with
three consumption paths and materially different feasibility for each,
given Forklift's WinRM-only remote architecture:

- **WMI Export.** Hyper-V WMI creates "reference points," computes the
  delta, and — per Microsoft's own docs — *"compiles the changes into a
  virtual hard drive and copies the file to the requested location. This
  method is easy to use, works for all scenarios, and works remotely."*
  The tradeoff: *"the virtual hard drive generated often creates a large
  amount of data to transfer over the network"* — Hyper-V materializes a
  full delta VHD server-side rather than handing back a byte-range list.
  This is the only one of the three paths compatible with Forklift's
  existing remote-only (WinRM) architecture without new infrastructure.
- **Win32 VirtDisk APIs** (`QueryChangesVirtualDisk`,
  `GetVirtualDiskInformation`, `SetVirtualDiskInformation`) — the
  byte-range-efficient equivalent of vSphere's `QueryChangedDiskAreas`.
  Function signature confirmed directly against Microsoft's API reference:
  takes an open `VirtualDiskHandle` (from `OpenVirtualDisk`) plus a
  `ChangeTrackingId`, returns an array of changed byte ranges. Minimum
  supported server: Windows Server 2016. **Critical limitation, stated
  explicitly in Microsoft's docs: these Win32 APIs "can only be accessed
  locally" and "don't support reading data from shared virtual hard disk
  files."** Forklift has no host-side agent today — using this path would
  mean designing and shipping one, a materially larger effort than the WMI
  Export path.
- **Remote Shared Virtual Disk Protocol** — only relevant for Shared VHDX
  (guest-clustering) scenarios; niche, see Open Question #3.
- Whether RCT has a minimum VM configuration version gate at all is
  **unconfirmed** — see Open Question #2, which found that Microsoft's own
  authoritative configuration-version feature matrix doesn't list RCT.
  Regardless of the answer, `Disk.RCTEnabled`'s `Get-VHD`-based `RctId`
  check should correctly report RCT as unavailable on any VM where it
  isn't actually engaged.

**Sources:**
[Hyper-V Backup Approaches (Microsoft Learn)](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/backup-approaches),
[QueryChangesVirtualDisk function reference (Microsoft Learn)](https://learn.microsoft.com/en-us/windows/win32/api/virtdisk/nf-virtdisk-querychangesvirtualdisk).

On driver readiness: `pkg/lib/hyperv/driver/driver.go`'s `HyperVDriver`
interface (lines 31-59) already exposes `ExecuteCommand`/
`ExecuteCommandWithTimeout` (arbitrary PowerShell/WMI execution) and
`RunOnNode`/`RunOnNodeWithTimeout` (cluster-node-targeted execution,
already used by `PowerState`/`PowerOff`). **No new driver primitive is
needed for the WMI Export path** — a precopy loop could reuse
`ExecuteCommand` exactly as `getDiskRCTEnabled` already does.

**However, wiring the `Client` methods alone does not get Forklift to a
working precopy loop — this significantly changes the effort estimate.**
Traced the full consumption path for `GetSnapshotDeltas` in the shared
plan controller:

- The warm-migration itinerary (`pkg/controller/plan/migrator/base/migrator.go:325-360`)
  gates `PhaseStoreInitialSnapshotDeltas`, `PhaseRemovePreviousSnapshot`/
  `PhaseWaitForPreviousSnapshotRemoval`, `PhaseStoreSnapshotDeltas`,
  `PhaseRemovePenultimateSnapshot`/`PhaseWaitForPenultimateSnapshotRemoval`,
  and `PhaseRemoveFinalSnapshot` all with `All: VSphere` — these phases
  are skipped entirely for every other provider today, Hyper-V included.
  Any Tier 1 implementation needs to extend or remove these gates for
  Hyper-V, not just fill in `Client` method bodies.
- More fundamentally: `PhaseStoreSnapshotDeltas`'s handler
  (`pkg/controller/plan/migration.go:1505-1517`) calls
  `provider.GetSnapshotDeltas(...)` and stores the result as a
  `changeIdMapping map[string]string`. For vSphere, this is a CBT
  `changeId` per disk device, later fed to `SetCheckpoints`, which
  configures CDI's `DataVolume.Spec.Checkpoints` — **CDI's own VDDK
  importer natively understands incremental multi-checkpoint imports**;
  the actual incremental byte-copying is CDI's job, not Forklift's.
  Hyper-V has no equivalent DataVolume importer type. Its DataVolumes are
  `Blank` (`builder.go:381-385`), filled exactly once, in full, by the
  virt-v2v conversion pod reading the complete SMB-mounted VHDX
  (`PodEnvironment`, `builder.go:569-611`) — there is no existing
  mechanism that takes a WMI Export delta VHD (or any other
  changed-region representation) and applies it onto an already-populated
  target PVC.

**Revised scope:** Tier 1 needs, at minimum: (1) the reference-point
lifecycle and WMI-export changed-VHD retrieval scripts (as originally
scoped), (2) extending the itinerary's `VSphere`-only phase gates to cover
Hyper-V, and (3) **a genuinely new delta-ingestion mechanism** — something
that can merge a delta VHD (or equivalent) into an existing target PVC's
content across repeated precopy cycles, which has no current analogue
anywhere in this codebase (CDI's checkpoint-aware VDDK importer is
vSphere-specific; virt-v2v's conversion pod today does one full copy, not
incremental merges). Item (3) is the real unknown and should be treated as
its own design spike, not folded into "wire the client methods."

**Estimated effort:** large — not medium as an earlier draft of this
document estimated. Detection infrastructure and driver transport are
solved; the reference-point/WMI-export scripting and itinerary-gate
changes are a medium-sized, well-scoped piece, but the delta-ingestion
mechanism is a genuinely new component with no existing pattern to copy,
and should be scoped as a design spike of its own before any effort
estimate for the full feature is trustworthy. Large-or-larger if Open
Question #1 concludes the Win32 API path is worth pursuing instead, since
that additionally requires a new host-side agent architecture on top of
the same delta-ingestion problem.

### Gap Tier 2: Host handler correctness

`pkg/controller/host/handler/hyperv/handler.go`'s `Watch` is a 14-line
no-op with the comment *"HyperV is single-host, no host-level operations
needed."* This comment is inconsistent with the rest of the adapter:
`Validator.MaintenanceMode` reads real `Host.State` records
(`validator.go:92-120`), and the scheduler does per-host (`OwnerNode`)
concurrency tracking specifically for failover-cluster mode
(`scheduler.go`).

**The fix is smaller in scope than a straight vSphere port, and the
"practical consequence" needs correcting on two counts.** vSphere's
handler (`pkg/controller/host/handler/vsphere/handler.go`, 111 lines)
watches `vsphere.Host` and, on a `Path`/`InMaintenanceMode`/`Status`
change, lists and enqueues reconciliation for **`api.Host` CRs**
(`handler.go:79-108`) — not `Plan` CRs. Porting this pattern verbatim to
Hyper-V would make Hyper-V's own `Host` CR status refresh promptly on a
node state change; it would **not**, by itself, push-trigger revalidation
of any `Plan`. Separately, `MaintenanceMode` maps to the `HostNotReady`
condition, which is hardcoded `Category: api.CategoryWarn`
(`pkg/controller/plan/validation.go:789-795`) — a warning, not a blocking
condition — for every provider, vSphere included. So even a hypothetically
instant, fully push-driven revalidation wouldn't stop a plan from
executing against a paused/down node; it would only refresh a warning
message sooner.

**Revised practical consequence:** today, a Hyper-V cluster node entering
`Paused`/`Down` state is only reflected in plan status reactively (at the
plan's own next reconcile), rather than promptly on the state transition —
a UX/staleness gap, not a blocking-behavior gap, since vSphere's
equivalent condition doesn't block execution either. Closing this for real
(if paused/down nodes should actually gate migration execution, not just
warn) needs a design decision and new work beyond porting the host
handler: either wiring a Plan-level watch on `Host` state (host handler →
Host CR refresh → a separate watch from Plan to Host, which doesn't exist
today either), or a migration-time guard that checks node state
immediately before/during execution rather than only at validation time.
See Open Question #4 — reframed to ask both "is the host handler itself
worth porting for CR-freshness alone" and "should paused/down actually
block execution, which is a product decision independent of this gap."

**Estimated effort:** small for the host-handler port alone (CR-freshness
only, matching vSphere's own current behavior); medium-to-large if the
goal is to actually make paused/down nodes block execution, since that
needs new plan-watch or migration-time-guard work with no existing
pattern to copy.

### Gap Tier 3: Shared disk / passthrough disk collection

Unlike tag/category mapping (see Non-Goals), this is a real, scoped gap,
not an inapplicable one. Hyper-V genuinely supports Shared VHDX
(multi-VM shared virtual disks, used for guest clustering — SQL Server
Always On, Scale-Out File Server) and pass-through/iSCSI-attached disks as
documented product features. `model/hyperv/model.go`'s `Disk` struct has
no `Shared`, `LUN`, `Passthrough`, `iSCSI`, or `FibreChannel` field, and
the PowerShell collector (`Get-VMHardDiskDrive`, `Get-VHD`) never queries
for any of them — this is purely an uncollected-data gap, not a stub with
nothing to wire, matching the shape of Nutanix's analogous gap. See Open
Question #3 for whether this is worth pursuing given how niche guest
clustering is among likely Forklift customers.

**Estimated effort:** medium — new PowerShell queries, model fields, and
`Validator.SharedDisks`/`ExcludedDisks` wiring (both currently
hardcoded-pass no-ops), gated on the demand question above.

### Gap Tier 4: Small validator win — `GuestToolsInstalled`

`Validator.GuestToolsInstalled` is a hardcoded-pass no-op
(`validator.go:280-282`), but the data to do a real check already exists:
`VM.GuestOS` is populated from KVP-exchange data (the same field
`TemplateLabels`'s fallback already uses), and the existing
`integration_services.rego` policy already implements the equivalent
inference in Rego (*"if `powerState == 'On'` and `guestOS` is empty,
Integration Services likely isn't running"* —
`validation/policies/io/konveyor/forklift/hyperv/integration_services.rego`).
Today that inference only surfaces as a Rego-level `Warning` concern in
plan status, not as a Go-level `Validator` check.

**This is not a pure wiring task, though — the shared validator's
condition severity is a real blocker.** `Validator.GuestToolsInstalled`
returning `false` feeds into a single, provider-agnostic condition in the
plan controller (`pkg/controller/plan/validation.go:897-903,1193-1198`):
`guestToolsIssue` is hardcoded `Category: api.CategoryCritical` with the
message *"VMware Tools issues detected... Ensure VMware Tools are properly
installed and running before migration."* Naively porting a real check to
Hyper-V would flag Hyper-V VMs with a Critical (plan-blocking) condition
bearing a wrong, VMware-specific message. Closing this gap properly
requires either making that shared condition provider-aware (a
Hyper-V-specific message variant) or routing Hyper-V's check through a
different, less severe condition path — a small change to shared code in
`pkg/controller/plan/validation.go`, done once before (or alongside)
wiring `GuestToolsInstalled` itself.

**Estimated effort:** small-to-medium — the Hyper-V-side check itself is
trivial, but it's gated on a small shared-code change first.

### Gap Tier 5: Scheduler cost modeling and shared-disk ordering

vSphere's scheduler (`vsphere/scheduler.go`, 326 lines, plus
`scheduler_test.go`, 213 lines) adds disk-count-based per-VM cost and
shared-disk creator/consumer ordering on top of host-capacity awareness.
Hyper-V's scheduler (117 lines, **no test file**) already has the
host-capacity-awareness piece (a real capability gap Nutanix lacks
entirely), but not the cost modeling or shared-disk ordering — the latter
is blocked on Tier 3 landing first, since there's currently no shared-disk
concept in the Hyper-V model to order around.

**Estimated effort:** small for a `scheduler_test.go` (pure test-debt,
independent of everything else); medium for cost modeling; ordering work
is blocked on Tier 3.

### Gap Tier 6: Test coverage

Real logic exists in Hyper-V's `validator.go`/`builder.go`, but test depth
is 20–45% of vSphere's by line count for comparable surface area:

| File | Hyper-V | vSphere |
|---|---|---|
| `validator_test.go` | 311 lines | 692 lines |
| `builder_test.go` | 400 lines | 1986 lines |
| `client_test.go` | 58 lines | *(none — vSphere has no equivalent)* |
| `scheduler_test.go` | **absent** | present, 213 lines |

`destinationclient_test.go`'s absence is **not listed as a gap here** —
correcting an earlier draft of this document. vSphere's
`destinationclient.go` has real, substantial logic (`getPopulatorCrList`,
`findPVCByCR`, XCOPY-populator CR ownership handling), which is genuinely
worth testing. Hyper-V's `destinationclient.go` is two permanent no-ops
(`DeletePopulatorDataSource`, `SetPopulatorCrOwnership`) — permanent
because `SupportsVolumePopulators()` is deliberately `false` (see
Non-Goals/Non-gaps: architecturally inapplicable, not a future phase's
target), and no tier in this roadmap adds behavior here. This sits
alongside `csi_import_test.go`/`rdm_storage_test.go`/
`uuid_to_vmware_serial_test.go` as a vSphere-specific test file with no
Hyper-V analogue worth building, not a coverage gap.

**Estimated effort:** small, should track alongside each other tier's
implementation rather than as a standalone cleanup pass.

### Gap Tier 7: CLI/tooling parity

`kubectl-mtv`'s `describe/host/describe.go` and `describe/provider/
describe.go` both reference `vsphere`; grep found zero files under
`describe/` referencing `hyperv`. Since Hyper-V does have real Host
inventory records (cluster nodes, used by `MaintenanceMode`), `kubectl mtv
describe host` plausibly doesn't support Hyper-V hosts today — a real,
scoped CLI gap, unlike the `create/provider`/`create/plan` paths which
already have full Hyper-V parity with vSphere.

**Estimated effort:** small.

### Non-gaps — investigated and found inapplicable or already safe

- **Tag/category → label mapping.** `SourceVMLabelsAndAnnotations` is an
  empty stub, but unlike Nutanix's equivalent gap, this is architecturally
  correct: Hyper-V's `VM` model has no `Tags`/`Category`/`Notes` field,
  and no PowerShell script queries `$vm.Notes` or any WMI tagging class.
  Hyper-V has no native VM-tagging concept comparable to vSphere
  tags/Nutanix categories to map from.
- **Volume populators.** `SupportsVolumePopulators()` returns `false`,
  but there's no array-side offload primitive to populate from in the
  first place — disks already arrive via a directly SMB-mounted PVC read
  by virt-v2v, not a remote-array copy CDI streams. Architecturally
  inapplicable under the current transfer design, for a different reason
  than Nutanix's version of the same gap (Nutanix has no XCOPY-equivalent
  API; Hyper-V has no remote array at all).
- **`PreferenceName` returning empty.** Traced the full call chain in
  `pkg/controller/plan/kubevirt.go`: when `PreferenceName` returns empty,
  `vmPreference()` synthesizes an error, the caller logs an `Info`-level
  message and falls back to `vmTemplate`, then further to `emptyVm` if
  needed; since `usesInstanceType` is never set for Hyper-V, `mapCPU`/
  `mapMemory` (already real) set explicit resource requests. **Nothing
  breaks** — this is a deliberate, working fallback path, not a bug. A
  real `PreferenceName` implementation (à la vSphere's `osMap`-driven
  lookup) would still be a nice-to-have for consistency but isn't
  correctness-blocking the way it might first appear.

### Security, Risks, and Mitigations

- **Host-handler gap (Tier 2) is a staleness/UX risk, not a live blocking
  risk** — corrected from an earlier draft of this document, which
  overstated it. Since `MaintenanceMode` is a warning-level condition for
  every provider (not just Hyper-V), a migration proceeding against a
  node that entered maintenance mid-window is already possible for
  vSphere today too; Tier 2 doesn't change that unless the separate
  product decision in Open Question #4(b) is made to actually block on
  it.
- **RCT precopy loop (Tier 1), if built on the WMI Export path, inherits
  the same "materialize a full delta VHD" data-volume cost on every
  precopy cycle** — this should be sized against real network/storage
  budgets before committing to a checkpoint interval, the same way
  vSphere's CBT-based warm migration has to reason about precopy cadence
  vs. data volume.
- **A future host-side agent (if Open Question #1 resolves toward the
  Win32 API path) is new attack surface** — software running with
  elevated access directly on the Hyper-V host, a meaningfully different
  trust boundary than the current WinRM-remote-only model. This alone is
  a reason to treat that path as a separate, carefully-scoped follow-on
  rather than bundling it into Tier 1's initial implementation.

## Design Details

### Proposed phasing

| Phase | Scope | Depends on | Ships independently? |
|---|---|---|---|
| Phase 0 | Resolve Open Questions #1–#4 (RCT path decision, down-level VM version check, Shared VHDX demand sizing, host-handler fix scoping) | — | Yes — research/decision |
| Phase 1 | Tier 2 host-handler fix (CR-freshness scope) + Tier 4 `GuestToolsInstalled` (gated on its shared-`validation.go` prerequisite) + `scheduler_test.go` (Tier 6 partial) | — | Yes |
| Phase 2a | Tier 1 design spike: delta-ingestion mechanism (how a changed-region delta gets applied onto an already-populated target PVC) + itinerary-gate changes | Phase 0's RCT path decision | No — blocked on Phase 0 |
| Phase 2b | Tier 1 implementation: reference-point lifecycle, WMI-export scripting, `Client` method wiring | Phase 2a's design resolved | No — blocked on Phase 2a |
| Phase 3 | Tier 3 shared-disk/passthrough-disk collection + validator wiring | Phase 0's demand-sizing decision | No — blocked on Phase 0 |
| Phase 4 | Tier 5 scheduler shared-disk ordering | Phase 3 (needs a shared-disk concept to order around) | No |
| Ongoing | Tier 6 remaining test-debt, Tier 7 CLI `describe` parity | Tracks alongside other phases | N/A |

### Test Plan

- **Phase 1:** unit tests for the host-handler watch/diff/enqueue logic
  (mirroring vSphere's pattern); unit test for `GuestToolsInstalled`
  covering powered-on/empty-`GuestOS` and powered-off cases; a
  `scheduler_test.go` covering the existing 117-line scheduler's
  host-capacity logic (currently completely untested).
- **Phase 2a:** no automated test plan yet — this is a design spike; its
  deliverable is a concrete design for the delta-ingestion mechanism and
  itinerary changes, to be validated by Phase 2b's tests.
- **Phase 2b:** unit tests for the reference-point lifecycle and
  changed-VHD retrieval against fixture WinRM responses, plus unit tests
  for whatever delta-ingestion mechanism Phase 2a's spike lands on;
  integration test against a real Windows Server 2016+ host with
  RCT-capable VMs (VM configuration version TBD per Open Question #2) —
  this cannot be meaningfully tested against mocks alone, matching the
  same constraint noted for Nutanix's CRT-based approach.
- **Phase 3:** unit tests for the new Shared VHDX/passthrough-disk model
  fields and validator logic; integration test with an actual guest
  cluster if Open Question #3's demand sizing justifies building one.
- All phases: no existing Hyper-V or vSphere test should regress; run
  `go test ./pkg/controller/plan/adapter/hyperv/... ./pkg/controller/plan/scheduler/hyperv/... ./pkg/controller/host/handler/hyperv/...`.

### Upgrade / Downgrade Strategy

Phase 1 changes are internal reconciliation/validation logic with no CRD
schema changes. Phase 2 (warm migration, spanning 2a/2b) reuses the existing
`MigrationWarm` type and `Cutover`/`VMCutover` fields already defined in
`pkg/apis/forklift/v1beta1/{plan,migration}.go` — the same pattern
`Validator.WarmMigration()` already gates on for vSphere, no new API
surface anticipated. Phase 3 (shared disk) would add new optional fields
to the Hyper-V inventory model, additive and non-breaking.

### Key Code Locations

| Component | File |
|---|---|
| Hyper-V builder (VM spec, DataVolumes, conversion-pod env) | `pkg/controller/plan/adapter/hyperv/builder.go` |
| Hyper-V validator (target of Tier 4) | `pkg/controller/plan/adapter/hyperv/validator.go` |
| Hyper-V client (target of Tier 1's snapshot-method wiring) | `pkg/controller/plan/adapter/hyperv/client.go` |
| Hyper-V scheduler (target of Tier 5) | `pkg/controller/plan/scheduler/hyperv/scheduler.go` |
| Hyper-V host handler (target of Tier 2) | `pkg/controller/host/handler/hyperv/handler.go` |
| RCT detection (already real, target of Tier 1 consumption) | `pkg/controller/provider/model/hyperv/model.go` (`RCTEnabled`), `pkg/lib/hyperv/powershell/scripts.go` (`GetDiskRCTEnabled`, batch scripts), `pkg/controller/provider/container/hyperv/client.go` |
| WinRM/PowerShell driver (transport, no changes needed for Tier 1's WMI Export path) | `pkg/lib/hyperv/driver/driver.go`, `pkg/lib/hyperv/driver/winrm.go` |
| Hyper-V inventory model (target of Tier 3) | `pkg/controller/provider/model/hyperv/model.go` |
| Rego policies (reference for Tier 4's logic) | `validation/policies/io/konveyor/forklift/hyperv/integration_services.rego` |
| vSphere host handler (reference for Tier 2) | `pkg/controller/host/handler/vsphere/handler.go` |
| vSphere validator/scheduler (reference) | `pkg/controller/plan/adapter/vsphere/validator.go`, `pkg/controller/plan/scheduler/vsphere/scheduler.go` |
| Hyper-V setup guide (already exists — not a gap) | `docs/hyperv-setup-guide.md` |

## Implementation History

- 2026-09-02 — Initial gap analysis and phased roadmap drafted.
- 2026-09-02 — Applied review feedback: corrected an unverified VM
  configuration version 8.0 claim for RCT (Microsoft's own
  configuration-version feature matrix does not list it — now tracked as
  an open question, not a stated requirement); discovered and documented
  a real itinerary/DataVolume-architecture gap for warm migration (no
  existing delta-ingestion path for Hyper-V, unlike vSphere's
  checkpoint-aware CDI VDDK importer), which changed Tier 1's effort
  estimate from medium to large and split its phase into a design spike
  (2a) plus implementation (2b); corrected the host-handler fix's actual
  effect (vSphere's handler enqueues `Host` CRs, not `Plan` CRs, and
  `MaintenanceMode` is a warning condition for every provider, not a
  blocking one — softened from "live correctness risk" to "staleness/UX
  risk," with the real fix requiring new plan-watch or migration-time-guard
  work, not a straight port); flagged that wiring a real
  `GuestToolsInstalled` check requires a shared-code prerequisite (the
  condition it feeds is hardcoded to a VMware-specific Critical message);
  removed the premature `destinationclient_test.go` gap claim (no phase in
  this roadmap gives that file real behavior to test); fixed a broken
  `see-also` link to the not-yet-merged companion Nutanix document.

## Drawbacks

- Tier 1's WMI Export path carries a real, ongoing data-transfer cost per
  precopy cycle (full delta VHD materialization, not byte-range deltas) —
  warm migration built on it may not deliver the cutover-window
  improvement users expect from "warm migration" in the vSphere/CBT sense,
  and this should be set correctly in expectations before shipping.
- Tier 1's delta-ingestion mechanism (Phase 2a) is a genuinely new
  component with no existing pattern in this codebase to copy — the
  design spike could conclude it's substantially more complex than the
  reference-point/WMI-export scripting itself, which would push Tier 1's
  real cost well past even the revised "large" estimate.
- Tier 3 (shared disk) risks being built for a small fraction of actual
  deployments if guest clustering turns out to be rare among Forklift's
  Hyper-V customer base — Open Question #3 should be answered with real
  demand data, not assumed.
- Unlike the Nutanix roadmap, this one has no externally-imposed deadline
  (no equivalent of Nutanix's legacy-API EOL bulletin was found for
  Hyper-V/WinRM), so there's a real risk these items simply never get
  prioritized against features with clearer forcing functions.

## Alternatives

1. **Pursue the Win32 `QueryChangesVirtualDisk` path instead of WMI
   Export**, accepting the cost of a new host-side agent, to get
   byte-range-efficient precopy closer to vSphere's CBT experience. Higher
   investment, better end-state; should only be chosen if Tier 1's WMI
   Export path proves too data-heavy in practice.
2. **Track Tier 2 (host-handler fix) as an independent, low-priority
   cleanup item** rather than part of this roadmap — since it only
   improves `Host` CR staleness (not blocking behavior, which already
   matches vSphere's warning-only severity), it may not warrant scheduling
   pressure at all unless Open Question #4(b)'s product decision concludes
   paused/down nodes should actually block execution, in which case it
   becomes part of a larger plan-watch/migration-guard effort instead.
3. **Skip Tier 3 (shared disk) entirely** and document Shared VHDX/
   passthrough disks as an explicitly unsupported configuration, if Open
   Question #3's demand sizing comes back low — cheaper than building and
   maintaining collector/validator code for a rarely-hit path.

## Infrastructure Needed

- Access to a Windows Server 2016+ Hyper-V host (standalone and, ideally,
  a small failover cluster) with a mix of VM configuration versions —
  including down-level VMs — for Phase 0/2 RCT research (Open Question #2)
  and integration testing.
- If Alternative #1 is pursued: infrastructure and a security review for
  a new host-side agent component, a materially different trust model
  than today's WinRM-remote-only architecture.
