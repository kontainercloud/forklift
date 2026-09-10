package nutanix

import (
	"errors"
	"testing"

	api "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1"
	planpkg "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/plan"
	"github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/ref"
	plancontext "github.com/kubev2v/forklift/pkg/controller/plan/context"
	webbase "github.com/kubev2v/forklift/pkg/controller/provider/web/base"
	model "github.com/kubev2v/forklift/pkg/controller/provider/web/nutanix"
	"github.com/kubev2v/forklift/pkg/controller/provider/web/ocp"
	"github.com/kubev2v/forklift/pkg/controller/validation"
	"github.com/kubev2v/forklift/pkg/lib/logging"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	cnv "kubevirt.io/api/core/v1"
)

// fakeValidatorInventory is a minimal web.Client stub for validator tests:
// Find returns a fixed VM, List returns a fixed set of destination VMs.
type fakeValidatorInventory struct {
	vm      model.VM
	destVMs []ocp.VM
	findErr error
}

func (f *fakeValidatorInventory) Finder() webbase.Finder { return nil }
func (f *fakeValidatorInventory) Get(_ interface{}, _ string) error {
	return nil
}
func (f *fakeValidatorInventory) List(list interface{}, _ ...webbase.Param) error {
	if l, ok := list.(*[]ocp.VM); ok {
		*l = f.destVMs
	}
	return nil
}
func (f *fakeValidatorInventory) Watch(_ interface{}, _ webbase.EventHandler) (*webbase.Watch, error) {
	return nil, errors.New("not implemented by fakeValidatorInventory")
}
func (f *fakeValidatorInventory) Find(resource interface{}, _ webbase.Ref) error {
	if f.findErr != nil {
		return f.findErr
	}
	if vm, ok := resource.(*model.VM); ok {
		*vm = f.vm
	}
	return nil
}
func (f *fakeValidatorInventory) VM(_ *webbase.Ref) (interface{}, error) { return &f.vm, nil }
func (f *fakeValidatorInventory) Workload(_ *webbase.Ref) (interface{}, error) {
	return nil, errors.New("not implemented by fakeValidatorInventory")
}
func (f *fakeValidatorInventory) Network(_ *webbase.Ref) (interface{}, error) {
	return nil, errors.New("not implemented by fakeValidatorInventory")
}
func (f *fakeValidatorInventory) Storage(_ *webbase.Ref) (interface{}, error) {
	return nil, errors.New("not implemented by fakeValidatorInventory")
}
func (f *fakeValidatorInventory) Host(_ *webbase.Ref) (interface{}, error) {
	return nil, errors.New("not implemented by fakeValidatorInventory")
}

// defaultTestVM returns a VM with two disks (one on a mapped storage
// container, backing a mapped subnet) and sensible defaults for testing.
func defaultTestVM() model.VM {
	vm := model.VM{}
	vm.ID = "vm-1"
	vm.Name = "test-vm"
	vm.PowerState = powerStateOn
	vm.GuestToolsEnabled = true
	vm.GuestToolsReachable = true
	vm.Disks = []model.Disk{
		{UUID: "disk-1", StorageContainerUUID: "sc-1", DiskSizeBytes: 1024},
		{UUID: "disk-2", StorageContainerUUID: "sc-1", DiskSizeBytes: 2048},
		{UUID: "cdrom-1", StorageContainerUUID: "sc-1", DiskSizeBytes: 0, IsCdrom: true},
	}
	vm.NICs = []model.NIC{
		{UUID: "nic-1", SubnetUUID: "subnet-1", MACAddress: "aa:bb:cc:dd:ee:01"},
	}
	return vm
}

func newTestValidator(vm model.VM, storageMap *api.StorageMap, networkMap *api.NetworkMap) *Validator {
	v := &Validator{
		Context: &plancontext.Context{
			Plan: &api.Plan{ObjectMeta: meta.ObjectMeta{Name: "test-plan"}},
			Source: plancontext.Source{
				Inventory: &fakeValidatorInventory{vm: vm},
			},
			Destination: plancontext.Destination{
				Inventory: &fakeValidatorInventory{},
			},
			Log: logging.WithName("test"),
		},
	}
	v.Map.Storage = storageMap
	v.Map.Network = networkMap
	return v
}

func mappedStorageMap(sourceIDs ...string) *api.StorageMap {
	refs := ref.Refs{}
	for _, id := range sourceIDs {
		refs.List = append(refs.List, ref.Ref{ID: id})
	}
	m := &api.StorageMap{}
	m.Status.Refs = refs
	return m
}

func mappedNetworkMap(sourceIDs ...string) *api.NetworkMap {
	refs := ref.Refs{}
	for _, id := range sourceIDs {
		refs.List = append(refs.List, ref.Ref{ID: id})
	}
	m := &api.NetworkMap{}
	m.Status.Refs = refs
	return m
}

func TestStorageMapped(t *testing.T) {
	vmRef := ref.Ref{ID: "vm-1"}

	t.Run("all disks mapped", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), mappedStorageMap("sc-1"), nil)
		ok, err := v.StorageMapped(vmRef)
		if err != nil || !ok {
			t.Fatalf("expected ok=true, err=nil; got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("unmapped storage container", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), mappedStorageMap("sc-other"), nil)
		ok, err := v.StorageMapped(vmRef)
		if err != nil || ok {
			t.Fatalf("expected ok=false, err=nil; got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("nil storage map", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), nil, nil)
		ok, err := v.StorageMapped(vmRef)
		if err != nil || ok {
			t.Fatalf("expected ok=false, err=nil; got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("cdrom disk ignored", func(t *testing.T) {
		vm := defaultTestVM()
		vm.Disks = []model.Disk{
			{UUID: "cdrom-1", StorageContainerUUID: "sc-unmapped", IsCdrom: true},
		}
		v := newTestValidator(vm, mappedStorageMap("sc-1"), nil)
		ok, err := v.StorageMapped(vmRef)
		if err != nil || !ok {
			t.Fatalf("expected ok=true (cdrom ignored), err=nil; got ok=%v, err=%v", ok, err)
		}
	})
}

func TestNetworksMapped(t *testing.T) {
	vmRef := ref.Ref{ID: "vm-1"}

	t.Run("all nics mapped", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), nil, mappedNetworkMap("subnet-1"))
		ok, err := v.NetworksMapped(vmRef)
		if err != nil || !ok {
			t.Fatalf("expected ok=true, err=nil; got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("unmapped subnet", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), nil, mappedNetworkMap("subnet-other"))
		ok, err := v.NetworksMapped(vmRef)
		if err != nil || ok {
			t.Fatalf("expected ok=false, err=nil; got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("nil network map", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), nil, nil)
		ok, err := v.NetworksMapped(vmRef)
		if err != nil || ok {
			t.Fatalf("expected ok=false, err=nil; got ok=%v, err=%v", ok, err)
		}
	})
}

func TestNICNetworkRefs(t *testing.T) {
	v := newTestValidator(defaultTestVM(), nil, nil)
	refs, err := v.NICNetworkRefs(ref.Ref{ID: "vm-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != "subnet-1" {
		t.Fatalf("expected one ref to subnet-1, got %+v", refs)
	}
}

func TestInvalidDiskSizes(t *testing.T) {
	vm := defaultTestVM()
	vm.Disks = append(vm.Disks, model.Disk{UUID: "disk-bad", StorageContainerUUID: "sc-1", DiskSizeBytes: 0})
	v := newTestValidator(vm, nil, nil)

	invalid, err := v.InvalidDiskSizes(ref.Ref{ID: "vm-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(invalid) != 1 || invalid[0] != "disk-bad" {
		t.Fatalf("expected [disk-bad], got %v", invalid)
	}
}

func TestMacConflicts(t *testing.T) {
	vm := defaultTestVM()

	t.Run("no conflict", func(t *testing.T) {
		v := newTestValidator(vm, nil, nil)
		v.Destination.Inventory = &fakeValidatorInventory{
			destVMs: []ocp.VM{
				{Object: cnv.VirtualMachine{}},
			},
		}
		conflicts, err := v.MacConflicts(ref.Ref{ID: "vm-1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(conflicts) != 0 {
			t.Fatalf("expected no conflicts, got %v", conflicts)
		}
	})

	t.Run("conflict detected", func(t *testing.T) {
		v := newTestValidator(vm, nil, nil)
		destVM := ocp.VM{}
		destVM.Namespace = "ns1"
		destVM.Name = "existing-vm"
		destVM.Object.Spec.Template = &cnv.VirtualMachineInstanceTemplateSpec{}
		destVM.Object.Spec.Template.Spec.Domain.Devices.Interfaces = []cnv.Interface{
			{MacAddress: "aa:bb:cc:dd:ee:01"},
		}
		v.Destination.Inventory = &fakeValidatorInventory{destVMs: []ocp.VM{destVM}}

		conflicts, err := v.MacConflicts(ref.Ref{ID: "vm-1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(conflicts) != 1 || conflicts[0].DestinationVM != "ns1/existing-vm" {
			t.Fatalf("expected one conflict against ns1/existing-vm, got %v", conflicts)
		}
	})
}

func TestPVCNameTemplate(t *testing.T) {
	v := newTestValidator(defaultTestVM(), nil, nil)

	t.Run("valid template", func(t *testing.T) {
		ok, err := v.PVCNameTemplate(ref.Ref{ID: "vm-1"}, "{{.PlanName}}-{{.TargetVmName}}-disk-{{.DiskIndex}}")
		if err != nil || !ok {
			t.Fatalf("expected ok=true, err=nil; got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("invalid template output", func(t *testing.T) {
		ok, err := v.PVCNameTemplate(ref.Ref{ID: "vm-1"}, "Invalid Name With Spaces")
		if err == nil || ok {
			t.Fatalf("expected an error for an invalid DNS1123 label, got ok=%v, err=%v", ok, err)
		}
	})
}

func TestGuestToolsInstalled(t *testing.T) {
	t.Run("powered on, tools reachable", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), nil, nil)
		ok, err := v.GuestToolsInstalled(ref.Ref{ID: "vm-1"})
		if err != nil || !ok {
			t.Fatalf("expected ok=true, err=nil; got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("powered on, tools not enabled", func(t *testing.T) {
		vm := defaultTestVM()
		vm.GuestToolsEnabled = false
		v := newTestValidator(vm, nil, nil)
		ok, err := v.GuestToolsInstalled(ref.Ref{ID: "vm-1"})
		if err != nil || ok {
			t.Fatalf("expected ok=false, err=nil; got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("powered off, tools not enabled is fine", func(t *testing.T) {
		vm := defaultTestVM()
		vm.PowerState = powerStateOff
		vm.GuestToolsEnabled = false
		v := newTestValidator(vm, nil, nil)
		ok, err := v.GuestToolsInstalled(ref.Ref{ID: "vm-1"})
		if err != nil || !ok {
			t.Fatalf("expected ok=true (VM powered off), err=nil; got ok=%v, err=%v", ok, err)
		}
	})
}

func withExcludeDisks(v *Validator, excludeDisks []string) *Validator {
	v.Plan.Spec.VMs = []planpkg.VM{
		{Ref: ref.Ref{ID: "vm-1"}, ExcludeDisks: excludeDisks},
	}
	return v
}

func TestSharedDisks(t *testing.T) {
	vmRef := ref.Ref{ID: "vm-1"}

	t.Run("no shared disks", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), nil, nil)
		ok, msg, category, err := v.SharedDisks(vmRef, nil)
		if err != nil || !ok || msg != "" || category != "" {
			t.Fatalf("expected ok=true with no message; got ok=%v, msg=%q, category=%q, err=%v", ok, msg, category, err)
		}
	})

	t.Run("shared disk flagged as a warning", func(t *testing.T) {
		vm := defaultTestVM()
		vm.Disks[0].Shared = true
		v := newTestValidator(vm, nil, nil)
		ok, msg, category, err := v.SharedDisks(vmRef, nil)
		if err != nil || ok {
			t.Fatalf("expected ok=false; got ok=%v, err=%v", ok, err)
		}
		if category != validation.Warn {
			t.Fatalf("expected category=Warn, got %q", category)
		}
		if msg == "" {
			t.Fatalf("expected a non-empty message")
		}
	})
}

func TestExcludedDisks(t *testing.T) {
	vmRef := ref.Ref{ID: "vm-1"}

	t.Run("no exclude list", func(t *testing.T) {
		v := newTestValidator(defaultTestVM(), nil, nil)
		ok, msg, category, err := v.ExcludedDisks(vmRef)
		if err != nil || !ok || msg != "" || category != "" {
			t.Fatalf("expected ok=true with no message; got ok=%v, msg=%q, category=%q, err=%v", ok, msg, category, err)
		}
	})

	t.Run("unknown disk UUID", func(t *testing.T) {
		v := withExcludeDisks(newTestValidator(defaultTestVM(), nil, nil), []string{"does-not-exist"})
		ok, _, category, err := v.ExcludedDisks(vmRef)
		if err != nil || ok || category != validation.Critical {
			t.Fatalf("expected ok=false/Critical for unknown UUID; got ok=%v, category=%q, err=%v", ok, category, err)
		}
	})

	t.Run("excludes every disk", func(t *testing.T) {
		v := withExcludeDisks(newTestValidator(defaultTestVM(), nil, nil), []string{"disk-1", "disk-2"})
		ok, _, category, err := v.ExcludedDisks(vmRef)
		if err != nil || ok || category != validation.Critical {
			t.Fatalf("expected ok=false/Critical when excluding every disk; got ok=%v, category=%q, err=%v", ok, category, err)
		}
	})

	t.Run("excludes the boot disk", func(t *testing.T) {
		v := withExcludeDisks(newTestValidator(defaultTestVM(), nil, nil), []string{"disk-1"})
		ok, _, category, err := v.ExcludedDisks(vmRef)
		if err != nil || ok || category != validation.Warn {
			t.Fatalf("expected ok=false/Warn when excluding the boot disk; got ok=%v, category=%q, err=%v", ok, category, err)
		}
	})

	t.Run("excludes a non-boot disk", func(t *testing.T) {
		v := withExcludeDisks(newTestValidator(defaultTestVM(), nil, nil), []string{"disk-2"})
		ok, msg, _, err := v.ExcludedDisks(vmRef)
		if err != nil || !ok {
			t.Fatalf("expected ok=true excluding a non-boot disk; got ok=%v, msg=%q, err=%v", ok, msg, err)
		}
	})

	t.Run("cdrom-only VM excluding cdrom does not trip every-disk check", func(t *testing.T) {
		vm := defaultTestVM()
		vm.Disks = []model.Disk{{UUID: "cdrom-1", IsCdrom: true}}
		v := withExcludeDisks(newTestValidator(vm, nil, nil), []string{"cdrom-1"})
		ok, msg, _, err := v.ExcludedDisks(vmRef)
		if err != nil || !ok {
			t.Fatalf("expected ok=true, got ok=%v, msg=%q, err=%v", ok, msg, err)
		}
	})
}
