---
title: nutanix-ahv-migration-maturity
authors:
  - "@tamalsaha"
reviewers:
  - TBD
approvers:
  - TBD
creation-date: 2026-09-02
last-updated: 2026-09-10
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
2. ~~Can virt-v2v's `-i disk` input mode run against a network-attached
   block device (nbdkit `curl`/`ssh` plugin) rather than a fully-downloaded
   local file?~~ **Resolved by a real spike, 2026-09-10 — yes, via a
   different (and better) mechanism than originally proposed.** This
   originally asked whether `-i disk` could be pointed at an `nbd://`
   socket manually fronted by nbdkit. That framing turns out to be based
   on an unreliable secondary source: an earlier research pass (web search
   + AI-summarized fetch) claimed `-i disk`'s man page documents an
   `nbd://` URI form, but the actual installed man page for virt-v2v 2.4.0
   (Ubuntu Noble) says no such thing about `-i disk` — that claim appears
   to have been a summarizer artifact, not a real documented feature (the
   same class of problem flagged for a different claim under Open
   Question #4). Rereading the real man page instead surfaced the correct,
   already-documented mechanism: its bandwidth-limiting section explicitly
   calls out **"`-i libvirtxml` when using HTTP or HTTPS disks"** as a
   first-class supported case.

   **This was tested end-to-end, locally, not just read about.** `nbdkit`
   and `virt-v2v` were installed in this session's sandbox (no Nutanix or
   OpenShift involved) and driven directly:
   1. Served a test disk image over both plain HTTP (nginx) and HTTPS with
      Basic Auth and a self-signed cert (nginx again, matching how a real
      HTTP(S) source is typically fronted), confirming byte-range request
      support first (`nbdkit`'s curl plugin requires HTTP Range support
      from the backing server — a real, concrete precondition worth
      checking against Nutanix's actual image-download endpoint before
      relying on this path, since it wasn't previously called out
      anywhere in this document).
   2. Wrote a minimal libvirt domain XML with `<disk type='network'
      device='disk'><source protocol='http'` (then `'https'`)
      `name='...'><host name='...' port='...'/></source>...`, per the man
      page's "Minimal XML for -i libvirtxml option" template.
   3. Ran `virt-v2v -i libvirtxml <that XML> -o local -os <dir>`.

   **Result: it worked completely, for both HTTP and HTTPS.** virt-v2v
   internally spawns its own `nbdkit` process with the `curl` plugin
   layered under `retry`/`cacheextents`/`cow` filters
   (`nbdkit --filter cow --filter cacheextents --filter retry curl
   url=https://...`), connects qemu to it over a Unix-socket NBD
   connection it manages itself, and libguestfs successfully ran
   `inspect_os` against the resulting network-backed disk (confirmed via
   `-x` trace output showing `launch = 0` and the OS-inspection pass
   actually running). The HTTPS case required the standard "trust this
   CA" step any HTTPS client needs (self-signed cert had to be added to
   the system trust store, and needed a proper `subjectAltName=IP:...`
   extension, not just a CN, for modern TLS libraries to accept it) — no
   different from what CDI's HTTP importer already requires today via
   this adapter's `ConfigMap`/CA-cert handling. (The only "failure" in
   the whole exercise was libguestfs correctly reporting "No root device
   found" on the synthetic test blob, which has no real filesystem on it
   by design — that's the expected, correct outcome for a blank disk, not
   a mechanism failure.)

   **Practical consequence for Phase 4, mechanism confirmed but a real
   blocker found, 2026-09-10.** The general `-i libvirtxml` + network-disk
   mechanism works, as above. But testing the two remaining unknowns
   directly against real endpoints (not just this session's synthetic
   test server) found a genuine blocker in one of them:

   - **Range-request support: tested against this document's actual lab,
     and Nutanix's real image-download endpoint does NOT support it.** A
     real catalog image was created via the same v3 Image Service flow
     `elementHTTPSource`/`builder.go` uses (`POST .../images` →
     `PUT .../images/{uuid}/file` → poll for `COMPLETE`), then its
     download endpoint (`GET /api/nutanix/v3/images/{uuid}/file`, the
     exact URL `elementHTTPSource` builds) was probed with a `Range:
     bytes=0-99` header. The response was `HTTP/2 200` with the *full*
     `content-length` (2097152, not the requested 100 bytes) and **no
     `Accept-Ranges` or `Content-Range` header at all** — confirmed on two
     separate range requests, not a one-off. Nutanix's real endpoint
     simply ignores the `Range` header and always returns the whole file.
     Since nbdkit's `curl` plugin (which `-i libvirtxml` relies on
     internally) *requires* Range support to work at all (confirmed
     separately this session: it errors immediately against a
     non-Range-capable server rather than degrading to a full
     download) — **the streaming, no-double-copy path this open question
     hoped for does not work against Nutanix's real image endpoint as it
     exists today.** (Test image created and deleted as part of this
     probe; the lab has zero images before and after.) This was only
     tested against Prism Element's v3 image endpoint (this lab has no
     Prism Central); whether Prism Central's v4 image download behaves
     differently is untested and a reasonable next thing to check if a
     PC environment becomes available.
   - **Credential passing: still untested**, for an environment reason
     rather than a design one this time. `apt-get install libvirt-clients
     libvirt-daemon-system` succeeded, but `libvirtd` exits immediately
     when started in this sandbox (`Failed to connect socket to
     '/var/run/libvirt/libvirt-sock'`) — consistent with a container
     sandbox lacking whatever kernel/cgroup features a full libvirtd
     needs, unlike `virt-v2v`'s own direct-backend QEMU invocation (which
     doesn't need a running daemon and worked fine). Genuinely blocked in
     this environment, not just undone.

   **Revised bottom line:** Open Question #2's core mechanism question
   (can virt-v2v consume an HTTP(S) disk source without a full download)
   is answered — yes, mechanically — but the practical answer for Nutanix
   specifically is now **no, not without either a full local-storage
   download first (regressing the "no double-copy" property this question
   was trying to preserve, but not a new problem — this document's Design
   Details section already names `PlanSpec.ConversionTempStorageClass`/
   `ConversionTempStorageSize` as the fallback for exactly this case) or a
   workaround this document hasn't identified** (e.g. nbdkit's `cache`/
   `cow` filters combined with a different plugin, or confirming whether
   v4's image download differs from v3's). This is a real, concrete
   narrowing of Phase 4's remaining uncertainty — not fully resolved, but
   now grounded in a direct test against the actual target system rather
   than assumption in either direction.
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

   **Caution flagged, 2026-09-10:** this question's "PC 7.3 / AOS 7.3"
   GA-floor citation, credited above to "the current v4 API reference
   matrix," should itself be treated with suspicion rather than as settled.
   Separate research for Tier 0 (this session) found that a summarized
   fetch of the same `nutanix.dev/api-reference-v4` page produced an
   equivalent "PC/AOS 7.3" GA claim for the *clustermgmt*/*vmm*/
   *networking* namespaces that doesn't square with Nutanix's real
   `pc.202x.x` version-numbering scheme, and was discarded as a likely
   fetch-summarizer artifact rather than cited as fact (see Tier 0's
   "Further research" note). The `dataprotection` "7.3" figure here may be
   the same kind of artifact from an earlier research pass, not
   independently confirmed against a primary changelog either. Re-verify
   this figure from a rendered (not AI-summarized) copy of the reference
   page, or directly with Nutanix, before relying on it.

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
architecture in detail); implements most `Validator` methods against real
inventory state (a handful — `DirectStorage`, `PowerState`,
`VMMigrationType` — are deliberate no-ops for concepts that don't apply to
vSphere either, not gaps); has a 326-line scheduler with host-capacity awareness and
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
  image-file endpoint (`/api/nutanix/v3/images/%s/file`).
  `pkg/controller/plan/adapter/nutanix/image_v4.go` — `clusterExternalIP`
  (used to rewrite Prism Central download redirects to the cluster VIP)
  calls `libclient.ListV3[libclient.Cluster]`.
- `pkg/controller/plan/adapter/nutanix/client.go:212-252` — VM lifecycle
  operations during migration are also on `v3`: `getVM` (`GET
  /api/nutanix/v3/vms/{uuid}`), `setPowerState` (`PUT` of the full VM spec
  with an updated power-state field — Nutanix's v3 update pattern requires
  resubmitting the whole spec, not a partial patch), and
  `transitionPowerState` (`POST .../vms/{uuid}/set_power_state`, used for
  ACPI shutdown). These are load-bearing for cold migration itself — power
  state reads and shutdown are legacy-API-dependent alongside inventory
  collection and disk transfer, not a separate concern.

Only two paths have already been ported to `v4`: Prism Central image
listing/creation (`vmm v4.0/content/images`, `image_v4.go`) and Prism
Central storage-container listing (`clustermgmt v4.0/config/
storage-containers`, `storage_api.go`). Everything else — the entire VM/
host/cluster/subnet inventory collector, the VM lifecycle calls above, and
the whole Prism Element disk-transfer path — is on APIs Nutanix has now
dated for removal.

**Why this matters for sequencing:** unlike Tiers 1–5, this has an external
deadline that isn't under this project's control, and it touches the same
client/collector code that several later tiers (Tier 1's `StorageMapped`/
`NetworksMapped`/`MaintenanceMode`, Tier 2's category collection) will also
need to modify. Doing the legacy-API migration first avoids
rebasing that other work on soon-to-be-replaced client code.

**Estimated effort (updated 2026-09-10):** VM lifecycle actions are done
(small, low-risk — see below). The remaining inventory-collector port
(cluster/host/VM/subnet listing) is medium-to-large: the "GA or not"
precondition that gated this estimate is now settled (all four listing
endpoints confirmed GA per Nutanix's own current SDK — see the "Major
correction" note below), but the actual field-by-field reshape work for
four large, deeply-nested v4 models remains, and should be done against a
live v4-capable Prism Central rather than blind. The runway (last-GA
release ~Q2 CY2027, phased removal starting ~Q4 CY2027, both roughly a
year or more out from this document's writing) is real but not immediate,
so this should be scheduled deliberately rather than treated as a fire
drill — see Open Question #5.

**Lab-confirmed, 2026-09-10:** a local Nutanix CE cluster (AOS 6.8.1,
Prism Element only, not registered to any Prism Central) exposes **zero**
v4 endpoints — `GET`/direct probes against
`/api/clustermgmt/v4.0/config/storage-containers`,
`/api/vmm/v4.0/content/images`, `/api/vmm/v4.0/ahv/config/vms`, and
`/api/clustermgmt/v4.0/config/clusters` all return `404`, while the
existing `v3`/`v2.0` calls this adapter already makes return `200`. AOS
6.8.1 is below every GA floor collected in Open Question #4 (earliest
cited: `pc.2024.3+`/AOS 7.3), so this doesn't resolve that question, but
it is a concrete, directly-observed data point that a bare Prism-Element
deployment on pre-7.x AOS has no v4 gateway at all — consistent with v4
being a Prism Central-fronted API surface rather than something Prism
Element serves standalone. **Practical consequence:** Phase 1 cannot be
implemented-and-verified end-to-end against this lab as configured; doing
so needs Prism Central added to the lab and/or an AOS upgrade toward
7.x. Phase 2 (Tier 1) has no such dependency — it is pure logic over
already-collected `v3` inventory data — so implementation is starting
there first, with Phase 1 to follow once a v4-capable environment is
available. See Implementation History.

**Further research, 2026-09-10 — per-endpoint v4 GA status is not uniform,
and the filter mechanism doesn't port 1:1.** Beyond the lab having no v4
surface at all, deeper research into the specific endpoints this tier would
need to port found real additional uncertainty, not just "needs a
PC-having lab to verify":

- **The `v3` generic list helper's filtering mechanism has no v4
  equivalent.** `listAllV3[T](r, kind, filter, pageSize)` passes a FIQL body
  filter; v4's `ListAllV4` (already used by the two ported paths, per
  `pkg/lib/client/nutanix/client.go`) pages via `$page`/`$limit` query
  params and has no such filter parameter — v4 uses OData query
  conventions instead. Porting `listClusters`/`listHosts`/`listVMs`/
  `listSubnets` isn't a drop-in swap of the generic helper; each call's
  filtering/exclusion logic (e.g. `excludeHostsByCluster`,
  `filterByMatch`) needs re-deriving against OData semantics.
- **Cluster listing's exact current path is unsettled across sources** —
  community/blog citations show `/api/clustermgmt/v4.0.b1/config/clusters`
  and `v4.0.b2/...` (both RC-suffixed) as well as a later unsuffixed
  `v4.2/config/clusters`, which is inconsistent with a single stable GA
  path. Confidence: inferred from scattered snippets, not confirmed
  against a primary reference we could render.
- **Host listing is confirmed RC (`v4.0.b1`) and restructured**, not just
  unverified: `GET /clustermgmt/v4.0.b1/config/clusters/{extId}/hosts`
  (developers.nutanix.com's own SDK docs) is per-cluster-nested, replacing
  v3's single global `POST /api/nutanix/v3/hosts/list`. Nutanix's own
  versioning policy states RC APIs are "not recommended for production
  use" (already quoted in Open Question #4) — this isn't a hypothetical
  concern for host listing, it's the confirmed status of the specific
  endpoint needed.
- **VM listing (`vmm`/`ahv/config/vms`) has the strongest evidence of the
  four**: nutanix.dev states directly *"the 'vmm' namespace is currently
  available as GA in Prism Central pc.2024.3 and AOS 7.0."* But namespace-level
  GA does not guarantee every operation in it is GA — a nutanix.dev post on
  vmm batch operations shows `POST .../vms/{extId}/$actions/
  associate-categories` explicitly tagged RC (`v4.0.b1`) even though it's
  in the same "GA" vmm namespace. Whether the base list-VMs call and the
  power-state/shutdown actions Tier 0 actually needs (see below) share the
  namespace's GA status specifically, or are RC like the categories action,
  is unconfirmed.
- **Subnet listing (`networking` namespace)** shows the same
  version-graduation pattern via Nutanix's own published SDK release
  history (`ntnx-networking-py-client` on PyPI: `4.0.1` → `4.0.1a1` →
  `4.0.1b1` → `4.0.2b1` → `4.1.1`+, i.e. alpha → beta → GA over time) —
  confirming namespaces do genuinely graduate, but not pinning which stage
  the specific subnet-listing endpoint is at on any AOS/PC version this
  document has evidence for.
- **VM lifecycle actions** (the v3 `getVM`/`setPowerState`/
  `transitionPowerState` equivalents Tier 0 also needs) follow a
  `.../vms/{extId}/$actions/<name>` pattern in v4, confirmed by the same
  batch-operations post above — but that post's own example action is RC,
  and this document found no primary source confirming the specific
  power-on/power-off/ACPI-shutdown action names or their GA/RC status.
- No single authoritative GA/EA/RC matrix covering clustermgmt+vmm+networking
  together could be confirmed: Nutanix's own `developers.nutanix.com`
  API reference is a JS-rendered SPA that didn't yield content to
  automated fetching in this research pass, and one summarized fetch of
  `nutanix.dev/api-reference-v4` returned a version-numbering claim
  ("PC/AOS 7.3") inconsistent with Nutanix's actual `pc.202x.x` versioning
  scheme — treated as a fetch-summarizer artifact and discarded, not cited
  as evidence anywhere in this document.

**Practical consequence (as of the above):** this tier's client-layer port
should not be implemented speculatively against unverified/RC endpoint
paths and an untested OData filter redesign. The path to `implementable`
status runs through either (a) a v4-capable Prism Central environment to
test against directly, or (b) a direct Nutanix engineering contact who can
confirm the current GA matrix authoritatively.

**Major correction, 2026-09-10 — the above was based on stale/secondary
sources; Nutanix's own current official SDK settles most of it.** The
scattered blog/community citations above (`.b1`/`.b2`-suffixed paths,
"unconfirmed" GA status) turned out to describe an *older* stage of these
APIs. Checking Nutanix's own actively-maintained Go client repository
(`github.com/nutanix/ntnx-api-golang-clients`, fetched directly via `gh
api`/raw.githubusercontent.com — the same technique that correctly sourced
the Volume Group schema in Tier 2, not a web search) shows all of the
following at **`v4.3`/`v4.4`, with no `.aN`/`.bN` EA/RC suffix** in the
currently-published client:

- Cluster listing: `GET /api/clustermgmt/v4.3/config/clusters`
- Host listing: **`GET /api/clustermgmt/v4.3/config/hosts`** — a global
  listing, not the per-cluster-nested-only endpoint the earlier
  `.b1`-suffixed blog snippet described. (A per-cluster variant,
  `.../clusters/{clusterExtId}/hosts`, also exists alongside it.) Host
  maintenance mode is also a real, addressable v4 concept here:
  `.../hosts/{extId}/$actions/enter-host-maintenance` and
  `exit-host-maintenance` — directly relevant to closing this document's
  `MaintenanceMode` gap (see Tier 1) once a way to read current
  maintenance status (not just toggle it) is confirmed.
- VM listing: `GET /api/vmm/v4.3/ahv/config/vms`, `GET
  /api/vmm/v4.3/ahv/config/vms/{extId}`.
- VM lifecycle: `POST .../vms/{extId}/$actions/power-on`, `power-off`
  ("Forceably shuts down a virtual machine which is equivalent to removing
  the power cable"), and `shutdown` ("Collaborative shutdown of a Virtual
  Machine through the ACPI support in the operating system" — the direct
  v4 equivalent of v3's `ACPI_SHUTDOWN` transition).
- Subnet listing: `GET /api/networking/v4.4/config/subnets`.

**This doesn't resolve everything** — it settles "is this endpoint
fundamentally GA in Nutanix's current API surface" (yes, for all of the
above), but not "what AOS/PC version floor does a given customer cluster
need to actually have it" (still Open Question #4's territory: SDK-level
GA and a specific deployed cluster's available API surface are different
questions), and it doesn't validate the exact JSON response field layout
end-to-end against a live server (the model structs are large,
auto-generated, and were read from source, not exercised).

**Status: VM lifecycle implemented, 2026-09-10.** On the strength of this
evidence, `getVM`/`setPowerState`/`transitionPowerState` in
`pkg/controller/plan/adapter/nutanix/client.go` are now dual-path: v3
against Prism Element (unchanged, since Element has no v4 surface at all
— see the lab-probe note above), v4 (`vmV4Path` +
`power-on`/`power-off`/`shutdown` actions) against Prism Central. This was
scoped narrowly and deliberately: it's a small, self-contained action
surface (no body, just an extId) with low mapping risk, unlike the
inventory-collector listing endpoints below.

**Status: cluster and subnet listing implemented, 2026-09-10.**
`listClusters`/`listSubnets` in `pkg/controller/provider/container/nutanix/
client.go` are now dual-path too, following the same evidence-sourced
approach as VM lifecycle: v4 wire schemas taken directly from Nutanix's
own official Go client (`clustermgmt-go-client`, `networking-go-client`),
with fixture-based tests (including one confirming the Prism Central
pseudo-cluster exclusion still works against v4's differently-shaped
`clusterFunction` field). Two smaller, well-understood entities — the
initial concern about "large, deeply-nested, auto-generated structs" held
for these too, but proved tractable once actually attempted: Subnet and
Cluster's v4 shapes are moderately nested (a few levels for IP config;
`Config`/`Network`/`Nodes` sub-objects for cluster) but not qualitatively
different in kind from what `listAllV4`/`toEntity` already handles for
images and storage containers. One accepted, documented gap: Cluster's v4
entity carries no storage-capacity fields at the list level (that data is
behind a separate `/stats/clusters/{extId}` call this collector doesn't
make, to avoid an N+1 call per cluster) — `TotalCapacity`/`UsedCapacity`
are left at zero for Prism Central-sourced clusters.

**Still open: VM and Host listing.** These remain on v3 for both Prism
modes. Unlike Cluster/Subnet, the v4 `Vm` model is substantially larger
(dozens of fields spanning disks, NICs, GPUs, boot config, guest tools) and
a mapping error there is more consequential (VM is the actual migration
subject — wrong disk/NIC data could silently misconfigure a migrated VM,
not just misreport a dashboard stat the way a wrong cluster capacity
number would). Host wasn't reattempted this pass either, simply for time —
its v4 shape wasn't re-researched after finding the global (not
per-cluster-only) `config/hosts` endpoint earlier. Both should follow the
same official-SDK-as-schema-source approach that worked for
Cluster/Subnet, ideally validated against a live v4-capable Prism Central
once one is available, with VM taking priority as the higher-value target.

### Gap Tier 1: Validator correctness

**Status: implemented, 2026-09-10** (Phase 2 — see Implementation History).
`StorageMapped`, `NetworksMapped`/`NICNetworkRefs`, `InvalidDiskSizes`,
`MacConflicts`, `PVCNameTemplate`, and `GuestToolsInstalled` all now check
real inventory state, with unit test coverage in `validator_test.go`.
`MaintenanceMode` remains a hardcoded pass, per the partial-exception note
below — closing it needs the AHV host-state model work described there,
which hasn't been done.

The table below reflects the pre-implementation state, kept for the
file:line evidence trail. These `Validator` methods returned a hardcoded
pass even though the inventory fields they'd need to check already existed
in the Nutanix model *and* were already consumed by the builder for other
purposes — closing them required no new inventory collection, only porting
logic and, where possible, reusing shared helpers from
`pkg/controller/plan/adapter/base` that vSphere already uses.

| Method | Nutanix before Phase 2 | Data available | vSphere reference |
|---|---|---|---|
| `StorageMapped` | `validator.go:28-31`, always `true` | `Disk.StorageContainerUUID` (used in `builder.go:436`) | iterates `vm.Disks`, checks `disk.Datastore.ID` against `Map.Storage.Status.Refs` |
| `NetworksMapped` / `NICNetworkRefs` | `validator.go:37-40,46-49`, stub | `NIC.SubnetUUID` (used in `builder.go:327`) | iterates `vm.Networks` against `Map.Network.Status.Refs` |
| `InvalidDiskSizes` | `validator.go:83-86`, stub | `Disk.DiskSizeBytes` (used in `builder.go:749`) | flags disk files with `capacity <= 0` |
| `MacConflicts` | `validator.go:88-91`, stub | `NIC.MACAddress` (used in `builder.go:342`) + shared `planbase.CheckMacConflicts` helper | checks source MACs against destination inventory |
| `GuestToolsInstalled` | `validator.go:98-100`, hardcoded `true` | `VM.GuestToolsEnabled/Mounted/Reachable/Version` — collected and already exposed via the inventory REST API (`web/nutanix/vm.go:250-270`), just not consumed by validation or building logic | checks VMware Tools status when VM is powered on |
| `PVCNameTemplate` | `validator.go:93-96`, stub | `Disk.UUID` covers the base case; `WinDriveLetter` (`plan.go:609`, vSphere-only today, sourced from VMware Tools guest-info) would need an equivalent Nutanix Guest Tools-derived field — a separate, smaller inventory gap, not part of Tier 3's conversion-pod work | builds `PVCNameTemplateData` per disk, validates via shared `planbase.ValidatePVCNameTemplate` |

`MaintenanceMode` is a partial exception: vSphere checks a real
`Host.InMaintenanceMode` boolean, but the Nutanix `Host` model only has a
bare `State string` — closing this one requires mapping AHV host state
values to a boolean first (a small model/collector change, not just wiring),
and possibly confirming the Prism API even surfaces this per-host.

**Investigated directly against the lab, 2026-09-10 — this needs new API
research, not a mapping table.** A live `POST /api/nutanix/v3/hosts/list`
query against this document's Nutanix CE lab returned `status.state:
"COMPLETE"` for its single host, with no maintenance-related field
anywhere in the full entity (checked by grepping the complete response for
"maintenance"). `COMPLETE`/`PENDING`/`ERROR` is the generic v3 entity
provisioning-lifecycle state every Nutanix v3 resource carries (the same
field shape appears on clusters, VMs, subnets, etc.) — it has nothing to
do with operational host maintenance mode, confirming the "possibly
confirming the Prism API even surfaces this per-host" caveat above was the
right thing to worry about. Community documentation for AHV host
maintenance describes it as an `ncli host edit`/CVM-level operation;
research for this document found no confirmed v2/v3 REST field or
endpoint exposing it as a simple per-host boolean the way vSphere's
`InMaintenanceMode` is. (This lab is also single-node CE, where
host-level maintenance mode may not be a fully exercisable concept in the
first place, so this negative result should be treated as suggestive, not
exhaustive, for a real multi-node cluster.) `MaintenanceMode` stays a
hardcoded pass; closing it needs either a multi-node Nutanix cluster to
probe the real API surface directly, or confirmation from Nutanix of
which endpoint (if any) exposes this.

**Estimated effort:** small — a few days per method, mostly following the
vSphere pattern and reusing `planbase` helpers. This tier has no dependency
on the open questions, but does touch the same collector client Tier 0
migrates — see Proposed Phasing below for sequencing.

### Gap Tier 2: Feature parity

Gaps that are real and scoped, but smaller than Tiers 4–5:

- **Excluded-disk detection — status: implemented, 2026-09-10.**
  `ExcludedDisks` now validates against real state, and the builder skips
  excluded disks in `VirtualMachine`/`DataVolumes`/`Tasks`
  (`VM.RemoveExcludedDisks` in `web/nutanix/vm.go`). This landed with a
  different identifier scheme than originally proposed here: rather than
  synthesizing a vSphere-style composite bus address (e.g. `scsi:0`) from
  `AdapterType`+`DeviceIndex` — which risks collisions across adapter types
  that both index from 0 — `excludeDisks` entries for Nutanix are disk
  `UUID`s, which are already globally unique and stable. The
  `plan.VM.ExcludeDisks` field's Go doc comment (`plan/vm.go:216`, "vSphere
  bus addresses") is now inaccurate for a second provider; it should read
  as provider-specific disk identifiers. See `docs/compatibility/vm-fields.md`
  for the updated per-provider format documentation.
- **Shared-disk detection — status: implemented, 2026-09-10, after being
  temporarily reclassified as blocked.** An earlier pass through this
  session correctly found that AHV has no shared-disk concept at the
  VM-disk level (multi-VM attachment is done via **Volume Groups**, a
  separate top-level entity, confirmed against the v3 API's `VMDisk`
  struct which has no shared/attachment field) — but then over-generalized
  that finding into "needs new API access this session doesn't have,"
  which was wrong: Volume Groups are a **v3** entity
  (`POST /api/nutanix/v3/volume_groups/list`), confirmed reachable and
  responding (200, zero entities) against this document's own
  Prism-Element-only lab, no Prism Central needed. The actual blocker was
  conflating "needs substantial new code" with "needs external access" —
  they're different problems. Implemented properly once that was
  corrected: a new `volume_groups` v3 collector
  (`container/nutanix/resource_volume_group.go`), with the exact wire
  schema (`attachment_list[].vm_reference`, `disk_list[].vmdisk_uuid`,
  etc.) sourced from Nutanix's own published Go SDK
  (`github.com/nutanix/terraform-provider-nutanix`,
  `nutanix/sdks/v3/prism/prism_structs.go`) rather than guessed, since
  this lab has no populated volume groups to verify against directly. A
  disk is marked `model.Disk.Shared = true` when it's backed by a volume
  group attached to more than one VM (cross-referenced by matching the
  VM's own disk UUID against the volume group's `disk_list[].vmdisk_uuid`
  — collector enrichment in `resource_vm.go`'s `enrichVM`, wired from
  `collector.go`'s `vms()`, with a best-effort fallback to "no shared
  disks" if the volume-group list call itself fails, so an RBAC
  restriction on this one endpoint can't break VM collection entirely).
  `Validator.SharedDisks` now flags shared disks as a **Warning**, not
  Critical: Nutanix's builder has no shared-PVC creation/dedup machinery
  the way vSphere's `findSharedPVCs` does, so a shared Volume Group is
  migrated today as independent, diverging per-VM copies rather than a
  single shared destination volume — the warning tells the user that
  before they're surprised by it, without blocking the migration outright
  (mirroring vSphere's own "Missing shared disks PVC" Warn-not-Critical
  precedent). Actually deduplicating/sharing the destination volume across
  VMs remains unimplemented and would still be new-feature-scale work;
  what's landed here is detection, matching what this tier actually asked
  for.
- **OS/Template mapping — status: partially implemented, 2026-09-10.**
  `TemplateLabels` now classifies the VM's guest OS
  (`VM.GuestOSID`/`GuestOSVersion`) into an osinfo template ID and sets the
  `os.template.kubevirt.io/*`/`workload.template.kubevirt.io/server`/
  `flavor.template.kubevirt.io/medium` labels accordingly. This is a
  coarser classifier than originally proposed — not a vSphere-style
  ~40-entry exact-match `osMap`, but a substring classifier (win/linux
  family) — because AHV has no fixed enumerated guest-OS-ID list to
  exact-match against: `guest_os_id` is reported by Nutanix Guest Tools
  introspection rather than user-selected from a known set, and Nutanix's
  own community forums report it commonly comes back null/empty in
  practice (unverified against this document's own lab, which has no VMs
  provisioned to check). A vSphere/oVirt-style exact-match table keyed on
  unconfirmed value strings would silently never match, so this uses the
  same substring fallback tier vSphere/oVirt already fall back to for
  values outside their own tables.

  **`PreferenceName`'s plumbing is now implemented, but the item is still
  incomplete for a reason this tier's original "self-contained, small"
  framing didn't anticipate.** Unlike `TemplateLabels`, which is
  adapter-local, `PreferenceName` looks up a `*core.ConfigMap` that
  `KubeVirt.getOsMapConfig` (`pkg/controller/plan/kubevirt.go:3190`)
  resolves via a per-provider `Settings.<Provider>OsConfigMap` value —
  previously wired only for `api.VSphere`/`api.OVirt`, with Nutanix falling
  into the `default` case and always getting an empty ConfigMap. This
  session added `Settings.NutanixOsConfigMap`, a `getOsMapConfig` switch
  case, and a real `configMap.Data[vm.GuestOSID]` lookup in
  `Builder.PreferenceName` — but deliberately made the setting **optional**
  rather than required at startup (unlike `VsphereOsConfigMap`/
  `OvirtOsConfigMap`), because there's still no verified Nutanix
  guest-OS-ID enum to populate a real mapping ConfigMap against (the same
  "no verified `guest_os_id` enum" problem as `TemplateLabels`, above).
  So the code path is ready, but there's no ConfigMap to point it at until
  that data is confirmed — an operator/CSV change to ship one is a
  separate, still-open follow-on once it is. This is functionally harmless
  to leave that way: `KubeVirt.vmPreference`
  (`pkg/controller/plan/kubevirt.go:2957-2969`) falls back to `vmTemplate`
  (which now works, per `TemplateLabels` above) whenever `PreferenceName`
  returns `""`, exactly as before this change, so there's no regression —
  just a still-open data gap rather than a wiring gap.
- **Tag/category → label/annotation mapping — status: implemented,
  2026-09-10.** `SourceVMLabelsAndAnnotations` now maps `VM.Categories`
  (key:value pairs, already collected — see Open Question #3's resolution)
  to `nutanix.forklift.konveyor.io/<key>: <value>` labels, reusing the same
  sanitization logic vSphere's tag→label mapping uses (extracted to
  `planbase.SanitizeForK8sMetadata`/`IsInLabelTags` as a shared helper
  during this work, and vSphere's builder now calls the shared version too
  — see `pkg/controller/plan/adapter/base/sanitize.go`). Unlike vSphere,
  which also maps custom attributes to annotations from a second data
  source, Nutanix only has categories, so annotations is always empty.
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

**Estimated effort (updated 2026-09-10):** excluded-disk detection,
`TemplateLabels`, category mapping, and shared-disk detection are all
done. `PreferenceName`'s wiring is done too, but its usefulness is gated
on a still-open data problem (no verified guest-OS-ID enum to populate a
mapping ConfigMap with) rather than remaining code work. Volume populators
and scheduler host-awareness remain new-feature-scale design efforts, not
ports, and should be scoped as their own follow-on enhancements.

### Gap Tier 3: Guest customization (conversion pod)

vSphere's disk copy for cold migrations can route through a virt-v2v
conversion pod (`PodEnvironment`, `builder.go:202-312` in the vSphere
adapter) that performs in-guest customization: virtio driver injection,
`/etc/fstab`/network config rewriting for static-IP preservation, and
LUKS/NBDE disk decryption. Nutanix has none of this — `PodEnvironment`
(`builder.go:822-824`) is a stub returning `nil, nil`, `ConversionPodConfig`
returns an empty result, and `builder.go:772` carries an explicit TODO:
*"remove this when Nutanix has a conversion step."*

**Practical consequence — a risk, not a guaranteed failure.** The builder's
`diskBus` mapping (`builder.go:256-268`) already preserves AHV's SCSI and
SATA/IDE adapter types onto their equivalent KubeVirt buses, so a guest
whose disks were attached via one of those buses in AHV can potentially
still boot without virtio drivers. Only disks that require virtio
specifically — including AHV's PCI-attached adapters, which `diskBus`
defaults to virtio for — are guaranteed to depend on injection that
doesn't happen today. The actual failure rate in practice depends on which
adapter types and guest OS/driver combinations Nutanix customers commonly
use; this is nonetheless the single biggest known risk standing between
"works in a lab with virtio-ready images" and general production
readiness, but it should be characterized and sized rather than assumed
universal.

virt-v2v itself is not inherently vSphere-specific. `docs/use-of-virt-v2v-
in-forklift.md` (an older architecture doc; note it predates the project's
current `-o local`-based conversion-pod design and doesn't mention
`-i libvirtxml`'s HTTP/HTTPS network-disk support) only describes a local
`-i disk` mode as "mainly useful for testing." **Open Question #2 is now
resolved** (see above) with a better answer than either framing assumed:
`-i libvirtxml` with a `<disk type='network' protocol='http'`/`'https'>`
source is a first-class, documented virt-v2v input mode, verified this
session to work end-to-end against a local streaming HTTP(S) source
(including proper TLS/CA handling), with virt-v2v transparently
orchestrating its own internal `nbdkit`+`cow`/`cacheextents`/`retry`
pipeline. **However, Nutanix's real per-disk catalog-image download
endpoint (`elementHTTPSource`/`centralHTTPSource` in `builder.go`) was
directly tested against this document's lab and does not support HTTP
Range requests** — see Open Question #2's "mechanism confirmed but a real
blocker found" update for the full test. Since nbdkit's `curl` plugin
requires Range support to stream at all, the streaming/no-double-copy path
this section originally proposed does not work against Nutanix's real
image endpoint as it exists today (at least on Prism Element v3; Prism
Central v4 is untested). The fallback is a full local-storage download
before invoking virt-v2v, which regresses the "no double-copy" property
but isn't a new problem to solve — the provider-neutral
`PlanSpec.ConversionTempStorageClass`/`ConversionTempStorageSize` fields
(already consumed generically by `pkg/controller/conversion/builder.go`)
exist for exactly this case.

**Estimated effort:** large, and the specific uncertainty this section
used to carry (could virt-v2v consume Nutanix's HTTP disk source at all)
is now answered concretely: mechanically yes, but not without a full
download first, given the real Range-support finding above. What's left
before this tier can be scoped as `implementable`: deciding whether the
full-download fallback is acceptable for a first version or whether a
Range-support workaround should be pursued (e.g. checking Prism Central's
v4 image download behavior, or an alternate nbdkit filter/plugin
combination), confirming how Prism's Basic Auth/cookie credentials get
passed through the libvirt XML `<source>`/`<auth>` mechanism (still
untested — blocked in this session's sandbox by `libvirtd` not starting,
not by the design itself), and the actual guest-customization logic
itself (driver injection, static-IP config, LUKS/NBDE) once the input
mechanism is settled.

**The conversion-pod orchestration logic lives in this repo, not an
opaque external image, 2026-09-10.** `pkg/virt-v2v/{config,conversion,
customize,server}` and `pkg/controller/conversion/builder.go` implement
the actual virt-v2v invocation, pod-spec construction, and domain-XML
handling that interprets the `V2V_*` env vars `PodEnvironment` sets — this
document previously treated that as an external contract to guess at;
it's directly inspectable Go code. It confirms an existing, already-used
local-file input pattern: `EnvDiskPathName`/`V2V_diskPath`, used today by
OVA and Hyper-V (`-i disk` against a file already present in the pod's
filesystem, e.g. Hyper-V's SMB-mounted share). This is architecturally
the natural fit for Nutanix's Range-support-forced full-download fallback
above: download the catalog image into the pod's filesystem first, then
set `V2V_diskPath` to it, exactly like OVA/Hyper-V already do.

**But the actual extension point providers get, `ConversionPodConfigResult`
(`pkg/controller/plan/adapter/base/doc.go`), only carries `NodeSelector`/
`Labels`/`Annotations` — no way to specify an extra init container or
volume.** OVA's `PodEnvironment` can point `V2V_diskPath` at an
already-mounted file because its source volume is mounted through some
other, more generic mechanism outside per-provider `ConversionPodConfig`
(not further investigated this session). Nutanix has no equivalent
pre-mounted volume — its disk only exists as a remote HTTP(S) byte stream
— so making `V2V_diskPath` work for Nutanix needs something new to
actually perform the download into the pod's filesystem before virt-v2v
runs. That "something" doesn't exist yet for any provider, and adding it
means extending `ConversionPodConfigResult` (and
`pkg/controller/conversion/builder.go`'s `BuildVirtV2vPod`, which every
provider's conversion pod goes through) with an init-container/volume
capability — a change to shared infrastructure every other provider's
conversion pod also relies on, not a Nutanix-local addition. That's a
meaningfully bigger and more consequential change than anything else in
this tier, and this document has no way to validate it without deploying
an actual conversion pod against a real cluster — which this session's
sandbox cannot do (no OpenShift cluster, no conversion pod runtime, no
Nutanix VM to actually migrate and check boots correctly). This is the
concrete reason this tier stops at architecture/research rather than
code, not effort avoidance: the risk profile (an incorrect implementation
here can render a migrated VM unbootable, and/or destabilize the shared
pod-building path other providers already depend on) is categorically
different from this session's other Nutanix-local changes.

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
  calls `generic.CreateProvider(...)` after `validateNutanixOptions`) —
  this is the one demonstrated CLI parity gap. Inventory listing itself is
  **not** a gap: the shared `get/inventory/` commands already have
  explicit Nutanix support — `storage.go` calls
  `GetResourceCollection(ctx, "storagecontainers", ...)` for
  `case "nutanix"`, `clusters.go` and `hosts.go` both list `"nutanix"`
  alongside `"ovirt"`/`"vsphere"`/`"hyperv"`, `vms.go` includes
  `"nutanix"` in its provider-type switches, and `networks.go`'s default
  branch (used by every non-OpenShift provider) already covers it.
  `get/inventory/{folders,datacenters}.go` genuinely have no Nutanix case,
  but that's architecturally correct — AHV has no vCenter-style
  folder/datacenter hierarchy to list.
- **Inventory web layer.** vSphere models folders/datacenters/tree/
  custom-fields as first-class inventory resources
  (`pkg/controller/provider/web/vsphere/{datacenter,datastore,folder,
  customfielddef,tree}.go`); none have a Nutanix equivalent. Some of this
  is architecturally inapplicable (AHV has no folder hierarchy); categories
  are already collected into the inventory model (see Tier 2) but not yet
  exposed as a first-class inventory web resource the way vSphere exposes
  custom fields.
- **Docs — status: implemented, 2026-09-10.** All eight
  `docs/compatibility/*.md` feature matrices now include Nutanix (verified
  against source, per Phase 3's Implementation History entry).
  `docs/nutanix-setup-guide.md` now exists, modeled on
  `docs/hyperv-setup-guide.md`, with its CLI examples validated against
  this document's own lab (`prism_central` probe, authenticated VM list,
  storage-container list all confirmed to return the responses the guide
  describes) rather than written from assumption alone. It states this
  document's current maturity/limitations up front (cold-only, no guest
  customization, no shared-disk migration) so it doesn't overclaim support
  the adapter doesn't have yet.

**Estimated effort:** small per item, but numerous; treat as ongoing work
tracked alongside each other tier rather than a single deliverable (see
Proposed Phasing below).

### Security, Risks, and Mitigations

- **Silent misconfiguration today, with two different failure shapes.**
  Because Tier 1 validators unconditionally pass, a plan with an unmapped
  storage container currently reaches `Ready` and fails during execution
  (`DataVolumes` returns an error when it can't find a storage mapping for
  a disk). An unmapped **network** fails differently and more subtly:
  `mapNetworks` (`builder.go:331-333`) silently `continue`s past any NIC
  with no allocated mapping — the migration doesn't fail at all, it
  completes with that NIC simply absent from the resulting VM spec. This
  is arguably a worse operational outcome than an execution failure, since
  a plan can appear to succeed while the migrated VM is missing expected
  connectivity. This is a correctness/UX risk, not a security risk, and is
  the primary justification for prioritizing Tier 1 early.
- **Guest customization gap and encrypted disks — the risk is remediation,
  not decryption.** CDI's HTTP importer is an opaque byte copier: it
  doesn't need to decrypt a LUKS volume, and doesn't corrupt one either —
  the encrypted bytes transfer unchanged, and a guest with its own
  passphrase prompt or a reachable NBDE/Tang server could still unlock at
  boot without any Forklift involvement. What Tier 3 actually provides
  (mirroring vSphere's `LUKS`/`NbdeClevis` plan fields) is *automated*
  remediation: passphrase injection, or NBDE network reconfiguration when
  the migrated VM's new network topology can't reach the original Tang
  server. Without Tier 3, encrypted-disk VMs migrate as opaque data and
  depend entirely on the guest's own unlock path continuing to work
  post-migration — a real limitation, but "cannot be migrated at all" is
  overstated and should not be repeated in user-facing docs.
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
| Phase 0 | Resolve the remaining open questions — #1 (Nutanix partner conversation), ~~#2 (virt-v2v spike)~~ **done 2026-09-10**, #4 (`compute-changed-regions` GA-status confirmation, now broadened to Tier 0's endpoints too), #5 (Tier 0 sequencing decision). Open Question #3 (categories) was already resolved. | — | Yes — pure research |
| Phase 1 | Tier 0 legacy API migration (v3/v2.0 → v4 for cluster/host/VM/subnet inventory, Prism Element image/storage-container handling, and the v3-based VM lifecycle calls in `client.go` — `getVM`, `setPowerState`, `transitionPowerState`) — **VM lifecycle, cluster listing, and subnet listing done 2026-09-10**; host and VM listing still open (VM is the higher-value remaining target) | — | Yes |
| Phase 2 | Tier 1 validator correctness + `validator_test.go` + compatibility-matrix docs update — **done 2026-09-10** | Benefits from Phase 1 landing first (shares the same client code) but not strictly blocked on it | Yes |
| Phase 3 | Tier 2 items with no external dependency: OS/Preference mapping, shared/excluded-disk model extension + validation, category→label mapping — **done 2026-09-10** | Not strictly blocked on Phase 2, but the shared/excluded-disk validator work benefits from landing after it (same test-fixture patterns) | Yes |
| Phase 4 | Tier 3 guest customization (conversion pod) | Open Question #2 is resolved (see Tier 3): the mechanism works, but Nutanix's real image endpoint doesn't support the Range requests it needs, so a full-download fallback is required instead of true streaming. Credential passing through libvirt XML `<auth>` is still untested (sandbox-blocked, not design-blocked) | Unblocked enough to scope a first version around the download-fallback path; still needs a credential-passing decision and the guest-customization logic itself |
| Phase 5 | Tier 4 warm migration | Phase 0's Nutanix partner-access and version-confirmation results, and reuses Phase 4's disk-access patterns if any | No — blocked on Phase 0, likely also on Phase 4 |
| Ongoing | Tier 5 tooling/CLI/tests/docs — compatibility docs and `nutanix-setup-guide.md` **done 2026-09-10** | Tracks alongside each phase above | N/A |

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
and oVirt already model these fields. Phase 4 (conversion pod) needs no new
`PlanSpec` fields at all — the existing provider-neutral
`ConversionTempStorageClass`/`ConversionTempStorageSize` fields already
configure scratch storage generically via
`pkg/controller/conversion/builder.go` and can be reused as-is if Nutanix's
conversion pod needs temp storage. Phase 5 (warm migration) would reuse the
existing `MigrationWarm` type and `Cutover`/`VMCutover` fields already
defined in `pkg/apis/forklift/v1beta1/{plan,migration}.go` — no new API
surface anticipated there either.

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
- 2026-09-10 — Verification pass against current upstream `main`
  (`26b55d051`, 2026-09-09): confirmed this branch is based on exactly
  that commit for every cited file, so no citations had drifted. Spot-
  checked every Tier 0/Tier 1 file:line citation, all 20 `Validator`
  methods, the `model.go` field list, and the `planbase` helper names
  directly against source; all confirmed accurate except one — corrected
  `clusterExternalIP`'s location from `builder.go` to `image_v4.go` (Tier
  0's bullet list). Beginning Phase 1 (Tier 0 legacy API migration)
  implementation next, validated against a local Nutanix CE lab (Prism
  Element, cold migration).
- 2026-09-10 — Probed the lab (AOS 6.8.1, Prism Element only) directly:
  confirmed it has no v4 API surface at all (all v4 endpoint probes
  404; see the new note under Gap Tier 0). This makes Phase 1
  unverifiable against the lab as currently configured, so implementation
  is starting with Phase 2 (Tier 1 validator correctness) instead, which
  needs no new API access. Phase 1 will follow once Prism Central and/or
  a newer AOS build is available.
- 2026-09-10 — Implemented Phase 2 (Tier 1 validator correctness) in full:
  `StorageMapped`, `NetworksMapped`/`NICNetworkRefs`, `InvalidDiskSizes`,
  `MacConflicts`, `PVCNameTemplate`, and `GuestToolsInstalled` now check
  real inventory state, with unit tests in `validator_test.go`.
  `MaintenanceMode` is unchanged (still needs the AHV host-state model
  work noted in its partial-exception paragraph).
- 2026-09-10 — Implemented most of Phase 3 (Tier 2): excluded-disk
  detection (using disk UUID as the identifier, not a synthesized bus
  address — see the updated Tier 2 entry and `docs/compatibility/vm-fields.md`),
  `TemplateLabels` OS classification, and category→label mapping, all with
  unit tests. In the course of this work, found two Tier 2 items were
  under-scoped in earlier drafts and reclassified them: shared-disk
  detection needs new Volume Group inventory collection (AHV multi-attach
  isn't modeled at the VM-disk level at all), and `PreferenceName` needs a
  Settings/operator change outside the adapter package, not just builder
  wiring — see the updated Tier 2 entries for both. Also extracted
  vSphere's tag→label sanitization helpers into a shared
  `pkg/controller/plan/adapter/base/sanitize.go` so Nutanix's category
  mapping could reuse them instead of duplicating ~35 lines of logic.
  Updated the `docs/compatibility/*.md` matrices to add Nutanix throughout
  (previously absent everywhere, per Tier 5), and corrected the
  `plan.VM.ExcludeDisks` API doc comment (and regenerated CRD manifests)
  which had said "vSphere only."
- 2026-09-10 — Followed up on `PreferenceName`: added the
  `Settings.NutanixOsConfigMap`/`getOsMapConfig` plumbing after all (see
  `pkg/settings/migration.go`, `pkg/controller/plan/kubevirt.go`), so this
  item isn't purely blocked. Unlike `VsphereOsConfigMap`/`OvirtOsConfigMap`,
  it's optional rather than required at controller startup, since there's
  still no verified Nutanix guest-OS-ID enum to force every deployment to
  ship a populated ConfigMap against (see `TemplateLabels`' `osinfoID`
  finding above). `PreferenceName` itself now does the real
  `configMap.Data[vm.GuestOSID]` lookup; it just has no data source until
  an operator sets `NUTANIX_OS_MAP` to a real ConfigMap, which still isn't
  populatable with confidence today.
- 2026-09-10 — Deepened Tier 0 and Open Question #2 research (see their
  updated entries): found host listing's v4 endpoint is confirmed RC
  (`v4.0.b1`) and per-cluster-nested rather than global, cluster/subnet
  listing's current path is unsettled across sources, the v3 filter
  mechanism has no v4 equivalent to port 1:1, and flagged that Open
  Question #4's existing "PC 7.3/AOS 7.3" dataprotection GA-floor citation
  may share the same kind of sourcing problem (an AI-summarized fetch of
  `nutanix.dev/api-reference-v4` producing a version number inconsistent
  with Nutanix's real `pc.202x.x` scheme) as a claim independently found
  and discarded this session for the clustermgmt/vmm/networking
  namespaces — worth re-verifying rather than continuing to treat as
  settled. For Open Question #2, confirmed virt-v2v's `-i disk` mode
  requires an `nbd://` URI (not a raw HTTP URL) and that nbdkit's `curl`
  plugin is already how virt-v2v itself fronts HTTP(S) sources elsewhere,
  narrowing the open question to whether that specific combination has
  been tested for an external (non-vCenter) HTTP source. Did not implement
  Tier 0's client-layer port on the strength of this evidence — it would
  mean writing and shipping code against endpoints this research could not
  confirm are GA, stable, or even at a settled path across Nutanix
  releases, with no live Prism Central to test against.
- 2026-09-10 — Investigated `MaintenanceMode` directly against the lab
  rather than leaving it as an assumption: a live `POST
  /api/nutanix/v3/hosts/list` query confirmed the collected `Host.State`
  field ("COMPLETE") is the generic v3 entity provisioning-lifecycle
  state every Nutanix resource carries, not operational maintenance mode,
  and no maintenance-related field appears anywhere in the full host
  entity response. Closing this validator needs new API research (a
  multi-node cluster to probe, or confirmation from Nutanix), not the
  "small model/collector change" originally estimated — see the updated
  Tier 1 entry. Completed `PreferenceName`'s settings/wiring gap
  identified in the previous entry (`Settings.NutanixOsConfigMap`,
  optional unlike vSphere/oVirt's required equivalent) — see the updated
  Tier 2 entry. Added `docs/nutanix-setup-guide.md`, with its CLI examples
  validated against the lab rather than written from assumption. Left
  Tier 0's client-layer port and Tier 2's shared-disk detection
  unimplemented, per the reasoning in their respective entries above —
  both would require either fabricating unverified API/schema details or
  new inventory-collection work this session's evidence doesn't support
  doing speculatively.
- 2026-09-10 — **Resolved Open Question #2 with a real spike**, not
  documentation-reading: installed `nbdkit` and `virt-v2v` locally
  (no Nutanix or OpenShift involved) and drove `virt-v2v -i libvirtxml`
  against a minimal libvirt domain XML with a `<disk type='network'
  protocol='http'>` (then `'https'`) source pointed at a local streaming
  HTTP(S) server. Confirmed end-to-end: virt-v2v transparently spawns its
  own `nbdkit` (`curl` plugin + `retry`/`cacheextents`/`cow` filters),
  connects qemu to it over NBD, and libguestfs successfully runs
  `inspect_os` against the network-backed disk — for both HTTP and HTTPS
  (the latter needed a properly-SAN'd trusted CA, exactly like CDI's
  importer already requires). This corrects an earlier research pass in
  this document's history (the "Refined, 2026-09-10" note previously
  under Open Question #2, now replaced) that had relied on an
  AI-summarized web fetch claiming `-i disk` accepts `nbd://` URIs
  directly — the actual installed man page says no such thing; the real
  documented mechanism is `-i libvirtxml` with a network-disk source, and
  it works. Also empirically confirmed nbdkit's curl plugin requires HTTP
  Range support from the backing server (a plain Python `http.server`
  failed with "server does not support 'range' requests"; nginx with
  Range support worked) — a concrete precondition to check against
  Nutanix's real image-download endpoints, not previously identified
  anywhere in this document. Updated Tier 3 and the Proposed Phasing
  table to reflect that Phase 4's core feasibility question is answered;
  what remains is narrower (Range support on Nutanix's actual endpoints,
  and credential passing through the libvirt XML `<auth>` mechanism or an
  embedded-credentials URL — neither tested this session, since testing
  them needs either a real Nutanix image URL or a running `libvirtd` with
  `virsh secret-*`, neither available in this sandbox).
- 2026-09-10 — **Implemented shared-disk detection**, correcting an
  over-generalization from earlier the same day: Volume Groups (AHV's
  multi-VM disk-attachment mechanism) were assumed blocked on "new API
  access this session doesn't have," but they're a **v3** endpoint,
  already confirmed reachable against this document's own lab. New
  `volume_groups` v3 collector (schema sourced from Nutanix's own
  published Go SDK on GitHub, not guessed, since the lab has no populated
  volume groups to verify against), `model.Disk.Shared` field, and
  `Validator.SharedDisks` now flags shared disks as a Warning (Nutanix has
  no shared-PVC dedup machinery, so a shared Volume Group migrates as
  independent per-VM copies today — the warning surfaces that rather than
  leaving it a silent surprise). See the updated Tier 2 entry. This
  closes out Phase 3 (Tier 2) completely.
- 2026-09-10 — **Corrected Tier 0's GA-status findings and ported VM
  lifecycle to v4.** The earlier "hosts confirmed RC, others unsettled"
  research (same day, above) turned out to be based on stale
  blog/community snippets describing an older API stage. Checking
  Nutanix's own currently-maintained official Go client
  (`github.com/nutanix/ntnx-api-golang-clients`, fetched directly via `gh
  api`, the same technique that correctly sourced Tier 2's Volume Group
  schema) shows cluster/host/VM/subnet listing all GA at `v4.3`/`v4.4`
  with no EA/RC suffix, including a global (not per-cluster-only) host
  listing endpoint and host-maintenance-mode enter/exit actions. Ported
  `getVM`/`setPowerState`/`transitionPowerState` in
  `pkg/controller/plan/adapter/nutanix/client.go` to dual-path v3/v4 on
  the strength of this evidence, with unit tests against a mock Prism
  Central server. Left the larger inventory-collector listing port
  (cluster/host/VM/subnet) for a live v4-capable environment, given the
  size and mapping risk of reshaping four large auto-generated v4 models
  blind. See the updated Tier 0 section.
- 2026-09-10 — **Tested Open Question #2's two remaining items directly
  against real systems, rather than leaving them as assumptions.**
  Created a real catalog image on this document's lab via the same v3
  Image Service flow `elementHTTPSource` uses, then probed its download
  endpoint with an HTTP `Range` header: the response was `200` with the
  full content length and no `Accept-Ranges`/`Content-Range` headers —
  Nutanix's real image endpoint does not support Range requests (test
  image deleted immediately after; the lab has zero images before and
  after). Since nbdkit's `curl` plugin requires Range support to stream,
  this means the no-double-copy streaming path Open Question #2 hoped
  for does not work against Nutanix's real endpoint as it exists today —
  a genuine, concrete blocker found by testing, not assumed in either
  direction. Separately attempted the credential-passing test
  (`libvirt-clients`/`libvirt-daemon-system` installed successfully, but
  `libvirtd` fails to start in this sandbox — environment-blocked, not a
  design question). Updated Open Question #2, Tier 3, and the Proposed
  Phasing table to reflect that Phase 4's fallback is a full-download
  approach (using the already-existing
  `ConversionTempStorageClass`/`Size` fields) rather than true streaming.
- 2026-09-10 — **Inspected the actual conversion-pod orchestration code**
  (`pkg/virt-v2v/*`, `pkg/controller/conversion/builder.go`) rather than
  treating it as an opaque external contract. Confirmed OVA/Hyper-V's
  existing `V2V_diskPath` local-file pattern is the natural fit for
  Nutanix's download-fallback path above, but found the actual extension
  point providers get (`ConversionPodConfigResult`) has no way to specify
  an init container or extra volume — so making the download step work
  needs a change to shared conversion-pod-building infrastructure every
  provider depends on, not a Nutanix-local addition. Did not implement
  this: an incorrect change here risks both an unbootable migrated VM and
  destabilizing other providers' already-working conversion pods, with no
  way in this sandbox to deploy and validate an actual pod. See the
  updated Tier 3 section.
- 2026-09-10 — **Reconsidered and closed part of the inventory-collector
  gap.** The earlier "large and risky, needs a live server" framing for
  the full collector port conflated two different risk profiles: wrong
  VM/disk data (consequential — could silently misconfigure a migrated
  VM) versus wrong cluster/subnet metadata (low-consequence — a
  misreported dashboard stat, not something that affects migration
  correctness). Ported `listClusters`/`listSubnets` to v4 for Prism
  Central on that reconsideration, using the same official-SDK-as-schema
  approach as the VM lifecycle port, with fixture-based tests. `listVMs`/
  `listHosts` remain on v3 — VM's v4 model is both larger and
  higher-consequence to get wrong, and Host wasn't reattempted this pass.
  See the updated Tier 0 section.

## Drawbacks

- This is a large, multi-release effort; splitting it across six tiers
  and six phases risks the project shipping Phase 1 or 2 and then
  deprioritizing the rest indefinitely, leaving Nutanix permanently at
  "cold migration, no guest customization" maturity. Phase sequencing
  should be revisited at each phase boundary rather than treated as a
  fire-and-forget backlog.
- Phase 5 (and part of Phase 4) depend on external factors (Nutanix
  partner access, unconfirmed API GA status) that engineering cannot
  unilaterally resolve — Phase 4's core virt-v2v integration question was
  resolved by direct testing this session, but two narrower items
  (Range-request support on Nutanix's real endpoints, credential passing)
  still need verification before it's a hard release candidate.
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
