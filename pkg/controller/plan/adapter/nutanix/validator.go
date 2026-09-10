package nutanix

import (
	"fmt"
	"strings"

	api "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1"
	"github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/ref"
	planbase "github.com/kubev2v/forklift/pkg/controller/plan/adapter/base"
	plancontext "github.com/kubev2v/forklift/pkg/controller/plan/context"
	webbase "github.com/kubev2v/forklift/pkg/controller/provider/web/base"
	model "github.com/kubev2v/forklift/pkg/controller/provider/web/nutanix"
	"github.com/kubev2v/forklift/pkg/controller/validation"
	liberr "github.com/kubev2v/forklift/pkg/lib/error"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Validator struct {
	*plancontext.Context
}

func (r *Validator) WarmMigration() bool {
	return false
}

func (r *Validator) MigrationType() bool {
	switch r.Plan.Spec.Type {
	case api.MigrationCold, "":
		return true
	default:
		return false
	}
}

// StorageMapped reports whether every non-CDROM disk's storage container has
// a destination mapping.
func (r *Validator) StorageMapped(vmRef ref.Ref) (ok bool, err error) {
	if r.Map.Storage == nil {
		return
	}
	vm := &model.VM{}
	err = r.Source.Inventory.Find(vm, vmRef)
	if err != nil {
		err = liberr.Wrap(err, "vm", vmRef.String())
		return
	}
	for _, disk := range vm.Disks {
		if disk.IsCdrom {
			continue
		}
		if !r.Map.Storage.Status.Find(ref.Ref{ID: disk.StorageContainerUUID}) {
			return
		}
	}
	ok = true
	return
}

func (r *Validator) DirectStorage(_ ref.Ref) (bool, error) {
	return true, nil
}

// NetworksMapped reports whether every VM NIC's subnet has a destination
// mapping. A NIC with no mapping is silently dropped by the builder rather
// than failing the migration, so this is what catches that case before the
// plan reaches Ready.
func (r *Validator) NetworksMapped(vmRef ref.Ref) (ok bool, err error) {
	if r.Map.Network == nil {
		return
	}
	vm := &model.VM{}
	err = r.Source.Inventory.Find(vm, vmRef)
	if err != nil {
		err = liberr.Wrap(err, "vm", vmRef.String())
		return
	}
	for _, nic := range vm.NICs {
		if !r.Map.Network.Status.Find(ref.Ref{ID: nic.SubnetUUID}) {
			return
		}
	}
	ok = true
	return
}

func (r *Validator) MaintenanceMode(_ ref.Ref) (bool, error) {
	return true, nil
}

// NICNetworkRefs returns one source-subnet ref per VM NIC.
func (r *Validator) NICNetworkRefs(vmRef ref.Ref) (refs []ref.Ref, err error) {
	vm := &model.VM{}
	err = r.Source.Inventory.Find(vm, vmRef)
	if err != nil {
		err = liberr.Wrap(err, "vm", vmRef.String())
		return
	}
	refs = make([]ref.Ref, 0, len(vm.NICs))
	for _, nic := range vm.NICs {
		refs = append(refs, ref.Ref{ID: nic.SubnetUUID})
	}
	return
}

func (r *Validator) StaticIPs(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) UdnStaticIPs(_ ref.Ref, _ client.Client) (bool, error) {
	return true, nil
}

// SharedDisks flags disks backed by a Nutanix Volume Group attached to
// more than one VM (AHV's multi-attach mechanism -- see
// container/nutanix/resource_volume_group.go for how this is detected).
// Unlike vSphere, Nutanix's builder has no shared-PVC creation/dedup
// machinery: each VM's DataVolumes() independently creates its own
// catalog image and DataVolume per disk, so a shared Volume Group would
// be migrated as independent, diverging copies rather than a single
// shared destination volume. This is a Warning, not a blocking Critical:
// the migration can still proceed, but the user should know the result
// won't actually be shared storage.
func (r *Validator) SharedDisks(vmRef ref.Ref, _ client.Client) (ok bool, msg string, category string, err error) {
	vm := &model.VM{}
	err = r.Source.Inventory.Find(vm, vmRef)
	if err != nil {
		return false, "", "", liberr.Wrap(err, "vm", vmRef.String())
	}

	var sharedDisks []string
	for _, disk := range vm.Disks {
		if disk.Shared {
			sharedDisks = append(sharedDisks, disk.UUID)
		}
	}
	if len(sharedDisks) == 0 {
		return true, "", "", nil
	}

	return false, fmt.Sprintf(
		"disk(s) %s are backed by a Nutanix Volume Group shared with other VMs; "+
			"this migration creates an independent copy for this VM rather than a shared destination volume",
		strings.Join(sharedDisks, ", "),
	), validation.Warn, nil
}

// ExcludedDisks reports whether excludeDisks is valid for the VM. Unlike
// vSphere (bus addresses), Nutanix disks are identified by UUID -- there's
// no adapter-type/device-index string format to synthesize or validate
// against.
func (r *Validator) ExcludedDisks(vmRef ref.Ref) (ok bool, msg string, category string, err error) {
	planVM, found := r.Plan.Spec.FindVM(vmRef)
	if !found || len(planVM.ExcludeDisks) == 0 {
		return true, "", "", nil
	}
	vm := &model.VM{}
	err = r.Source.Inventory.Find(vm, vmRef)
	if err != nil {
		return false, "", "", liberr.Wrap(err, "vm", vmRef.String())
	}

	known := make(map[string]struct{}, len(vm.Disks))
	nonCdromCount := 0
	for _, disk := range vm.Disks {
		known[disk.UUID] = struct{}{}
		if !disk.IsCdrom {
			nonCdromCount++
		}
	}

	exclude := make(map[string]struct{}, len(planVM.ExcludeDisks))
	var unknown []string
	for _, id := range planVM.ExcludeDisks {
		if _, ok := known[id]; !ok {
			unknown = append(unknown, id)
			continue
		}
		exclude[id] = struct{}{}
	}
	if len(unknown) > 0 {
		return false, fmt.Sprintf("excludeDisks disk UUIDs not found on VM: %s", strings.Join(unknown, ", ")), validation.Critical, nil
	}

	excludedNonCdromCount := 0
	for _, disk := range vm.Disks {
		if disk.IsCdrom {
			continue
		}
		if _, skip := exclude[disk.UUID]; skip {
			excludedNonCdromCount++
		}
	}
	if nonCdromCount > 0 && excludedNonCdromCount == nonCdromCount {
		return false, "excludeDisks removes every disk from the VM", validation.Critical, nil
	}

	if bootDisk := bootDiskUUID(vm); bootDisk != "" {
		if _, skip := exclude[bootDisk]; skip {
			return false, fmt.Sprintf("excludeDisks includes the root disk %s; the target VM may not boot", bootDisk), validation.Warn, nil
		}
	}

	return true, "", "", nil
}

func (r *Validator) ChangeTrackingEnabled(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) HasSnapshot(_ ref.Ref) (bool, string, string, error) {
	return true, "", "", nil
}

func (r *Validator) PowerState(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) VMMigrationType(_ ref.Ref) (bool, error) {
	return true, nil
}

// InvalidDiskSizes returns the UUIDs of non-CDROM disks with a non-positive
// size.
func (r *Validator) InvalidDiskSizes(vmRef ref.Ref) ([]string, error) {
	vm := &model.VM{}
	err := r.Source.Inventory.Find(vm, vmRef)
	if err != nil {
		return nil, liberr.Wrap(err, "vm", vmRef.String())
	}
	var invalid []string
	for _, disk := range vm.Disks {
		if disk.IsCdrom {
			continue
		}
		if disk.DiskSizeBytes <= 0 {
			invalid = append(invalid, disk.UUID)
		}
	}
	return invalid, nil
}

// MacConflicts checks the VM's source NIC MAC addresses against
// already-migrated destination VMs.
func (r *Validator) MacConflicts(vmRef ref.Ref) ([]planbase.MacConflict, error) {
	vm, err := planbase.FindSourceVM[model.VM](r.Source.Inventory, vmRef)
	if err != nil {
		return nil, err
	}

	destinationVMs, err := planbase.GetDestinationVMsFromInventory(r.Destination.Inventory, webbase.Param{
		Key:   webbase.DetailParam,
		Value: "all",
	})
	if err != nil {
		return nil, liberr.Wrap(err)
	}

	var sourceMacs []string
	for _, nic := range vm.NICs {
		sourceMacs = append(sourceMacs, nic.MACAddress)
	}

	return planbase.CheckMacConflicts(sourceMacs, destinationVMs), nil
}

// PVCNameTemplate validates the PVC name template against every non-CDROM
// disk, using the same disk enumeration order DataVolumes() uses so the
// validated DiskIndex values match what will actually be created.
func (r *Validator) PVCNameTemplate(vmRef ref.Ref, pvcNameTemplate string) (ok bool, err error) {
	vm := &model.VM{}
	err = r.Source.Inventory.Find(vm, vmRef)
	if err != nil {
		err = liberr.Wrap(err, "vm", vmRef.String())
		return
	}

	targetVmName := planbase.ResolveTargetVmName(r.Plan, vm.ID, vm.Name)

	diskIndex := 0
	for _, disk := range vm.Disks {
		if disk.IsCdrom {
			continue
		}
		testData := api.PVCNameTemplateData{
			VmName:       vm.Name,
			TargetVmName: targetVmName,
			PlanName:     r.Plan.Name,
			DiskIndex:    diskIndex,
			VmId:         vm.ID,
		}
		if _, templateErr := planbase.ValidatePVCNameTemplate(pvcNameTemplate, testData); templateErr != nil {
			return false, templateErr
		}
		diskIndex++
	}

	return true, nil
}

// GuestToolsInstalled checks Nutanix Guest Tools status for the VM. Only
// checked when the VM is powered on, mirroring vSphere's VMware Tools check.
func (r *Validator) GuestToolsInstalled(vmRef ref.Ref) (ok bool, err error) {
	vm := &model.VM{}
	err = r.Source.Inventory.Find(vm, vmRef)
	if err != nil {
		err = liberr.Wrap(err, "vm", vmRef.String())
		return
	}
	if vm.PowerState != powerStateOn {
		ok = true
		return
	}
	ok = vm.GuestToolsEnabled && vm.GuestToolsReachable
	return
}

func (r *Validator) ConsolidationNeeded(_ ref.Ref) (bool, error) {
	return false, nil
}
