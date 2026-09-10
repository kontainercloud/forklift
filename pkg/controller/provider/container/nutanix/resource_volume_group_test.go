package nutanix

import (
	"encoding/json"
	"testing"

	libclient "github.com/kubev2v/forklift/pkg/lib/client/nutanix"
)

func vgWithAttachments(diskUUIDs []string, vmUUIDs ...string) volumeGroupEntity {
	var attachments []libclient.VMAttachment
	for _, vm := range vmUUIDs {
		attachments = append(attachments, libclient.VMAttachment{VMReference: libclient.Ref{UUID: vm}})
	}
	var disks []libclient.VGDisk
	for _, d := range diskUUIDs {
		disks = append(disks, libclient.VGDisk{VmdiskUUID: d})
	}
	return volumeGroupEntity{
		Status: libclient.VolumeGroupDefStatus{
			Resources: libclient.VolumeGroupResources{
				AttachmentList: attachments,
				DiskList:       disks,
			},
		},
	}
}

func TestAttachedVMCount(t *testing.T) {
	tests := []struct {
		name string
		vg   volumeGroupEntity
		want int
	}{
		{"no attachments", vgWithAttachments([]string{"d1"}), 0},
		{"single VM", vgWithAttachments([]string{"d1"}, "vm-1"), 1},
		{"two distinct VMs", vgWithAttachments([]string{"d1"}, "vm-1", "vm-2"), 2},
		{"duplicate VM reference counted once", vgWithAttachments([]string{"d1"}, "vm-1", "vm-1"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.vg.attachedVMCount(); got != tt.want {
				t.Errorf("attachedVMCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSharedVMDiskUUIDs(t *testing.T) {
	vgs := []volumeGroupEntity{
		vgWithAttachments([]string{"disk-shared-1", "disk-shared-2"}, "vm-1", "vm-2"),
		vgWithAttachments([]string{"disk-solo"}, "vm-3"),
		vgWithAttachments([]string{"disk-orphan"}),
	}

	shared := sharedVMDiskUUIDs(vgs)

	for _, want := range []string{"disk-shared-1", "disk-shared-2"} {
		if !shared[want] {
			t.Errorf("expected %q to be marked shared, got %+v", want, shared)
		}
	}
	for _, notWant := range []string{"disk-solo", "disk-orphan"} {
		if shared[notWant] {
			t.Errorf("expected %q NOT to be marked shared, got %+v", notWant, shared)
		}
	}
}

// TestVolumeGroupEntity_JSONShape guards against the wire schema drifting
// out of sync with Nutanix's own published SDK
// (nutanix/terraform-provider-nutanix, prism_structs.go), since this
// endpoint has no live entities on the environment this was developed
// against to round-trip against directly.
func TestVolumeGroupEntity_JSONShape(t *testing.T) {
	raw := `{
		"metadata": {"uuid": "vg-1", "name": "shared-data-vg"},
		"status": {
			"name": "shared-data-vg",
			"state": "COMPLETE",
			"resources": {
				"sharing_status": "SHARED",
				"iscsi_target_prefix": "shared-data-vg",
				"attachment_list": [
					{"vm_reference": {"uuid": "vm-1", "name": "app-node-1"}},
					{"vm_reference": {"uuid": "vm-2", "name": "app-node-2"}}
				],
				"disk_list": [
					{"vmdisk_uuid": "disk-shared-1"}
				]
			}
		}
	}`

	var e volumeGroupEntity
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if e.Metadata.UUID != "vg-1" {
		t.Errorf("Metadata.UUID = %q, want vg-1", e.Metadata.UUID)
	}
	if e.attachedVMCount() != 2 {
		t.Errorf("attachedVMCount() = %d, want 2", e.attachedVMCount())
	}
	shared := sharedVMDiskUUIDs([]volumeGroupEntity{e})
	if !shared["disk-shared-1"] {
		t.Errorf("expected disk-shared-1 to be marked shared, got %+v", shared)
	}
}
