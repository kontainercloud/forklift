package nutanix

import (
	"bytes"
	"fmt"
	"testing"

	api "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1"
	planpkg "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/plan"
	"github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/ref"
	planbase "github.com/kubev2v/forklift/pkg/controller/plan/adapter/base"
	plancontext "github.com/kubev2v/forklift/pkg/controller/plan/context"
	model "github.com/kubev2v/forklift/pkg/controller/provider/web/nutanix"
	"github.com/kubev2v/forklift/pkg/lib/logging"
	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	cnv "kubevirt.io/api/core/v1"
)

func TestConfigMapSetsCDICertKeys(t *testing.T) {
	cacert := []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----")
	secret := &core.Secret{
		Data: map[string][]byte{
			"ca.crt": cacert,
		},
	}
	configMap := &core.ConfigMap{}
	builder := &Builder{}

	err := builder.ConfigMap(ref.Ref{}, secret, configMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(configMap.BinaryData["ca.pem"], cacert) {
		t.Fatalf("expected ca.pem to match provider CA")
	}
	if !bytes.Equal(configMap.BinaryData["tls.crt"], cacert) {
		t.Fatalf("expected tls.crt to match provider CA for CDI nbdkit cainfo")
	}
}

func TestBootDiskUUID(t *testing.T) {
	tests := []struct {
		name            string
		bootDeviceOrder string
		disks           []model.Disk
		want            string
	}{
		{
			name:            "first block disk in disk_list when indices collide",
			bootDeviceOrder: "DISK,CDROM,NETWORK",
			disks: []model.Disk{
				{UUID: "sata-0", DeviceType: "DISK", AdapterType: adapterSATA, DeviceIndex: 0},
				{UUID: "scsi-0", DeviceType: "DISK", AdapterType: adapterSCSI, DeviceIndex: 0},
			},
			want: "sata-0",
		},
		{
			name:            "skips cdrom before first disk in inventory order",
			bootDeviceOrder: "CDROM,DISK,NETWORK",
			disks: []model.Disk{
				{UUID: "cdrom-1", DeviceType: "CDROM", IsCdrom: true},
				{UUID: "sata-0", DeviceType: "DISK", AdapterType: adapterSATA, DeviceIndex: 0},
				{UUID: "scsi-0", DeviceType: "DISK", AdapterType: adapterSCSI, DeviceIndex: 0},
			},
			want: "sata-0",
		},
		{
			name: "defaults to disk cdrom network when boot order empty",
			disks: []model.Disk{
				{UUID: "cdrom-1", DeviceType: "CDROM", IsCdrom: true},
				{UUID: "disk-1", DeviceType: "DISK"},
			},
			want: "disk-1",
		},
		{
			name:            "falls back to first non-cdrom when boot order has no disk",
			bootDeviceOrder: "CDROM,NETWORK",
			disks: []model.Disk{
				{UUID: "cdrom-1", DeviceType: "CDROM", IsCdrom: true},
				{UUID: "disk-1", DeviceType: "DISK"},
			},
			want: "disk-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vm := &model.VM{VM1: model.VM1{
				BootDeviceOrder: tt.bootDeviceOrder,
				Disks:           tt.disks,
			}}
			if got := bootDiskUUID(vm); got != tt.want {
				t.Fatalf("bootDiskUUID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapDisks_BootOrder(t *testing.T) {
	tests := []struct {
		name            string
		bootDeviceOrder string
		disks           []model.Disk
		wantBootDisk    string
	}{
		{
			name:            "exactly one boot disk when adapter indices collide",
			bootDeviceOrder: "DISK,CDROM,NETWORK",
			disks: []model.Disk{
				{UUID: "sata-0", DeviceType: "DISK", AdapterType: adapterSATA, DeviceIndex: 0},
				{UUID: "scsi-0", DeviceType: "DISK", AdapterType: adapterSCSI, DeviceIndex: 0},
			},
			wantBootDisk: "sata-0",
		},
		{
			name:            "boot order follows inventory not adapter priority",
			bootDeviceOrder: "DISK,CDROM,NETWORK",
			disks: []model.Disk{
				{UUID: "scsi-0", DeviceType: "DISK", AdapterType: adapterSCSI, DeviceIndex: 0},
				{UUID: "sata-0", DeviceType: "DISK", AdapterType: adapterSATA, DeviceIndex: 0},
			},
			wantBootDisk: "scsi-0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vm := &model.VM{VM1: model.VM1{
				BootDeviceOrder: tt.bootDeviceOrder,
				Disks:           tt.disks,
			}}
			pvcs := make([]*core.PersistentVolumeClaim, 0, len(tt.disks))
			for _, disk := range tt.disks {
				if disk.IsCdrom {
					continue
				}
				pvcs = append(pvcs, &core.PersistentVolumeClaim{
					ObjectMeta: meta.ObjectMeta{
						Annotations: map[string]string{planbase.AnnDiskSource: disk.UUID},
					},
				})
			}

			object := &cnv.VirtualMachineSpec{Template: &cnv.VirtualMachineInstanceTemplateSpec{}}
			(&Builder{}).mapDisks(vm, pvcs, object)

			var bootCount int
			for _, disk := range object.Template.Spec.Domain.Devices.Disks {
				if disk.BootOrder == nil {
					continue
				}
				bootCount++
				if disk.Name != tt.wantBootDisk {
					t.Fatalf("expected boot order on %q, got %q", tt.wantBootDisk, disk.Name)
				}
			}
			if bootCount != 1 {
				t.Fatalf("expected exactly one boot disk, got %d", bootCount)
			}
		})
	}
}

func TestOsinfoID(t *testing.T) {
	tests := []struct {
		name           string
		guestOSID      string
		guestOSVersion string
		want           string
	}{
		{"empty", "", "", unknownOsinfoID},
		{"windows guest id", "Windows Server 2019", "", defaultWindowsOsinfoID},
		{"windows in version only", "", "windows:64:WindowsServer2008R2Enterprise", defaultWindowsOsinfoID},
		{"rhel", "rhel", "", defaultLinuxOsinfoID},
		{"red hat spelled out", "Red Hat Enterprise Linux", "", defaultLinuxOsinfoID},
		{"centos", "CentOS Linux 7", "", defaultLinuxOsinfoID},
		{"ubuntu", "Ubuntu 22.04", "", defaultLinuxOsinfoID},
		{"generic linux", "some linux distro", "", defaultLinuxOsinfoID},
		{"unrecognized", "FreeBSD", "", unknownOsinfoID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vm := &model.VM{}
			vm.GuestOSID = tt.guestOSID
			vm.GuestOSVersion = tt.guestOSVersion
			if got := osinfoID(vm); got != tt.want {
				t.Fatalf("osinfoID(%q, %q) = %q, want %q", tt.guestOSID, tt.guestOSVersion, got, tt.want)
			}
		})
	}
}

func TestTemplateLabels(t *testing.T) {
	vm := model.VM{}
	vm.ID = "vm-1"
	vm.GuestOSID = "rhel8"

	builder := &Builder{Context: &plancontext.Context{
		Source: plancontext.Source{
			Inventory: &fakeValidatorInventory{vm: vm},
		},
	}}

	labels, err := builder.TemplateLabels(ref.Ref{ID: "vm-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOSLabel := fmt.Sprintf(templateOSLabel, defaultLinuxOsinfoID)
	if labels[wantOSLabel] != "true" {
		t.Fatalf("expected label %q=true, got labels=%v", wantOSLabel, labels)
	}
	if labels[templateWorkloadLabel] != "true" || labels[templateFlavorLabel] != "true" {
		t.Fatalf("expected workload/flavor labels set, got %v", labels)
	}
}

func TestSourceVMLabelsAndAnnotations(t *testing.T) {
	vm := model.VM{}
	vm.ID = "vm-1"
	vm.Categories = map[string]string{
		"Environment": "Production",
		"Invalid Key": "bad value!",
	}

	newBuilder := func() *Builder {
		return &Builder{Context: &plancontext.Context{
			Source: plancontext.Source{
				Inventory: &fakeValidatorInventory{vm: vm},
			},
			Log: logging.WithName("test"),
		}}
	}

	t.Run("categories become labels", func(t *testing.T) {
		labels, annotations, _, err := newBuilder().SourceVMLabelsAndAnnotations(ref.Ref{ID: "vm-1"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if labels[nutanixLabelPrefix+"Environment"] != "Production" {
			t.Fatalf("expected Environment label, got %v", labels)
		}
		if len(annotations) != 0 {
			t.Fatalf("expected no annotations, got %v", annotations)
		}
	})

	t.Run("invalid key/value sanitized", func(t *testing.T) {
		labels, _, sanitizationReport, err := newBuilder().SourceVMLabelsAndAnnotations(ref.Ref{ID: "vm-1"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for k, v := range labels {
			if k != nutanixLabelPrefix+"Environment" && v == "bad_value" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a sanitized label for 'Invalid Key', got %v", labels)
		}
		if len(sanitizationReport) == 0 {
			t.Fatalf("expected a non-empty sanitization report")
		}
	})

	t.Run("disabled tag mapping produces no labels", func(t *testing.T) {
		labels, _, _, err := newBuilder().SourceVMLabelsAndAnnotations(ref.Ref{ID: "vm-1"}, &api.TagMapping{Disabled: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(labels) != 0 {
			t.Fatalf("expected no labels when tag mapping disabled, got %v", labels)
		}
	})

	t.Run("labelTags filters categories", func(t *testing.T) {
		labels, _, _, err := newBuilder().SourceVMLabelsAndAnnotations(ref.Ref{ID: "vm-1"}, &api.TagMapping{LabelTags: []string{"Environment"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := labels[nutanixLabelPrefix+"Environment"]; !ok {
			t.Fatalf("expected Environment label to survive filter, got %v", labels)
		}
		if len(labels) != 1 {
			t.Fatalf("expected only the filtered category, got %v", labels)
		}
	})
}

// TestPodEnvironment covers the minimal in-place virt-v2v environment this
// builder supplies when a plan opts into NutanixGuestConversion (Tier 3 --
// see docs/enhancements/nutanix-ahv-migration-maturity.md). Nutanix always
// runs in-place, so this deliberately does NOT set V2V_source/V2V_diskPath/
// V2V_libvirtURL -- AppConfig.validate() only requires those when the pod
// isn't running in-place.
func TestPodEnvironment(t *testing.T) {
	envValue := func(env []core.EnvVar, name string) (string, bool) {
		for _, e := range env {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	t.Run("sets vmName, nothing else for a plain Linux VM", func(t *testing.T) {
		vm := model.VM{}
		vm.ID = "vm-1"
		vm.Name = "linux-vm"
		vm.GuestOSID = "rhel8"

		builder := &Builder{Context: &plancontext.Context{
			Plan:   &api.Plan{},
			Source: plancontext.Source{Inventory: &fakeValidatorInventory{vm: vm}},
		}}

		env, err := builder.PodEnvironment(ref.Ref{ID: "vm-1"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name, ok := envValue(env, "V2V_vmName"); !ok || name != "linux-vm" {
			t.Fatalf("expected V2V_vmName=linux-vm, got env=%v", env)
		}
		if _, ok := envValue(env, "VIRTIO_WIN"); ok {
			t.Fatalf("did not expect VIRTIO_WIN for a Linux VM, got env=%v", env)
		}
		if _, ok := envValue(env, "V2V_NBDE_CLEVIS"); ok {
			t.Fatalf("did not expect V2V_NBDE_CLEVIS by default, got env=%v", env)
		}
		for _, unwanted := range []string{"V2V_source", "V2V_diskPath", "V2V_libvirtURL"} {
			if _, ok := envValue(env, unwanted); ok {
				t.Fatalf("did not expect %s to be set for Nutanix (in-place only), got env=%v", unwanted, env)
			}
		}
	})

	t.Run("sets VIRTIO_WIN for a Windows VM", func(t *testing.T) {
		vm := model.VM{}
		vm.ID = "vm-1"
		vm.Name = "win-vm"
		vm.GuestOSID = "windows"

		builder := &Builder{Context: &plancontext.Context{
			Plan:   &api.Plan{},
			Source: plancontext.Source{Inventory: &fakeValidatorInventory{vm: vm}},
		}}

		env, err := builder.PodEnvironment(ref.Ref{ID: "vm-1"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if path, ok := envValue(env, "VIRTIO_WIN"); !ok || path != "/usr/local/virtio-win-legacy.iso" {
			t.Fatalf("expected VIRTIO_WIN legacy ISO path, got env=%v", env)
		}
	})

	t.Run("InstallLegacyDrivers override forces VIRTIO_WIN on for a Linux VM", func(t *testing.T) {
		vm := model.VM{}
		vm.ID = "vm-1"
		vm.Name = "linux-vm"
		vm.GuestOSID = "rhel8"
		forceOn := true

		builder := &Builder{Context: &plancontext.Context{
			Plan: &api.Plan{
				Spec: api.PlanSpec{InstallLegacyDrivers: &forceOn},
			},
			Source: plancontext.Source{Inventory: &fakeValidatorInventory{vm: vm}},
		}}

		env, err := builder.PodEnvironment(ref.Ref{ID: "vm-1"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := envValue(env, "VIRTIO_WIN"); !ok {
			t.Fatalf("expected VIRTIO_WIN when InstallLegacyDrivers=true overrides a Linux VM, got env=%v", env)
		}
	})

	t.Run("InstallLegacyDrivers override forces VIRTIO_WIN off for a Windows VM", func(t *testing.T) {
		vm := model.VM{}
		vm.ID = "vm-1"
		vm.Name = "win-vm"
		vm.GuestOSID = "windows"
		forceOff := false

		builder := &Builder{Context: &plancontext.Context{
			Plan: &api.Plan{
				Spec: api.PlanSpec{InstallLegacyDrivers: &forceOff},
			},
			Source: plancontext.Source{Inventory: &fakeValidatorInventory{vm: vm}},
		}}

		env, err := builder.PodEnvironment(ref.Ref{ID: "vm-1"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := envValue(env, "VIRTIO_WIN"); ok {
			t.Fatalf("did not expect VIRTIO_WIN when InstallLegacyDrivers=false overrides a Windows VM, got env=%v", env)
		}
	})

	t.Run("sets V2V_NBDE_CLEVIS when the plan VM opts in", func(t *testing.T) {
		vm := model.VM{}
		vm.ID = "vm-1"
		vm.Name = "encrypted-vm"

		builder := &Builder{Context: &plancontext.Context{
			Plan: &api.Plan{
				Spec: api.PlanSpec{
					VMs: []planpkg.VM{{Ref: ref.Ref{ID: "vm-1"}, NbdeClevis: true}},
				},
			},
			Source: plancontext.Source{Inventory: &fakeValidatorInventory{vm: vm}},
		}}

		env, err := builder.PodEnvironment(ref.Ref{ID: "vm-1"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if value, ok := envValue(env, "V2V_NBDE_CLEVIS"); !ok || value != "true" {
			t.Fatalf("expected V2V_NBDE_CLEVIS=true, got env=%v", env)
		}
	})
}
