package nutanix

import (
	libclient "github.com/kubev2v/forklift/pkg/lib/client/nutanix"
)

// volumeGroupEntity is a v3 volume_group wire entity.
type volumeGroupEntity libclient.VolumeGroup

func (e volumeGroupEntity) resources() libclient.VolumeGroupResources {
	if len(e.Status.Resources.DiskList) > 0 || len(e.Status.Resources.AttachmentList) > 0 {
		return e.Status.Resources
	}
	return e.Spec.Resources
}

// attachedVMCount returns how many distinct VMs this volume group is
// attached to.
func (e volumeGroupEntity) attachedVMCount() int {
	seen := map[string]bool{}
	for _, a := range e.resources().AttachmentList {
		if a.VMReference.UUID != "" {
			seen[a.VMReference.UUID] = true
		}
	}
	return len(seen)
}

// sharedVMDiskUUIDs returns the set of VM disk UUIDs (matching
// model.Disk.UUID / the v3 VM disk_list's own "uuid" field) that are
// backed by a volume group attached to more than one VM -- i.e. disks
// AHV actually shares across VMs, as opposed to a volume group merely
// used as a single VM's non-container-backed disk.
func sharedVMDiskUUIDs(volumeGroups []volumeGroupEntity) map[string]bool {
	shared := map[string]bool{}
	for _, vg := range volumeGroups {
		if vg.attachedVMCount() < 2 {
			continue
		}
		for _, disk := range vg.resources().DiskList {
			if disk.VmdiskUUID != "" {
				shared[disk.VmdiskUUID] = true
			}
		}
	}
	return shared
}
