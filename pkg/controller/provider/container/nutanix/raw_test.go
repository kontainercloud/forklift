package nutanix

import (
	"encoding/json"
	"testing"

	model "github.com/kubev2v/forklift/pkg/controller/provider/model/nutanix"
)

// TestSubnetV4Raw_JSONShape guards against the wire schema drifting out of
// sync with Nutanix's own published SDK (nutanix/ntnx-api-golang-clients,
// networking-go-client), since this endpoint has no live entities on any
// environment this was developed against to round-trip against directly.
func TestSubnetV4Raw_JSONShape(t *testing.T) {
	raw := `{
		"extId": "subnet-1",
		"name": "vlan-100",
		"clusterReference": "cluster-a",
		"networkId": 100,
		"subnetType": "VLAN",
		"ipConfig": [
			{
				"ipv4": {
					"ipSubnet": {"ip": {"value": "192.168.100.0"}, "prefixLength": 24},
					"defaultGatewayIp": {"value": "192.168.100.1"},
					"dhcpServerAddress": {"value": "192.168.100.2"},
					"poolList": [
						{"startIp": {"value": "192.168.100.100"}, "endIp": {"value": "192.168.100.200"}}
					]
				}
			}
		],
		"dhcpOptions": {"domainName": "example.local"}
	}`

	var r subnetV4Raw
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	entity := r.toEntity()

	if entity.Metadata.UUID != "subnet-1" {
		t.Errorf("Metadata.UUID = %q, want subnet-1", entity.Metadata.UUID)
	}
	if entity.Metadata.Name != "vlan-100" {
		t.Errorf("Metadata.Name = %q, want vlan-100", entity.Metadata.Name)
	}
	if entity.clusterUUID() != "cluster-a" {
		t.Errorf("clusterUUID() = %q, want cluster-a", entity.clusterUUID())
	}
	if entity.Status.Resources.VlanID != 100 {
		t.Errorf("VlanID = %d, want 100", entity.Status.Resources.VlanID)
	}
	if entity.Status.Resources.SubnetType != "VLAN" {
		t.Errorf("SubnetType = %q, want VLAN", entity.Status.Resources.SubnetType)
	}
	if entity.Status.Resources.IPConfig.SubnetIP != "192.168.100.0" {
		t.Errorf("SubnetIP = %q, want 192.168.100.0", entity.Status.Resources.IPConfig.SubnetIP)
	}
	if entity.Status.Resources.IPConfig.PrefixLength != 24 {
		t.Errorf("PrefixLength = %d, want 24", entity.Status.Resources.IPConfig.PrefixLength)
	}
	if entity.Status.Resources.IPConfig.DefaultGatewayIP != "192.168.100.1" {
		t.Errorf("DefaultGatewayIP = %q, want 192.168.100.1", entity.Status.Resources.IPConfig.DefaultGatewayIP)
	}
	if entity.Status.Resources.IPConfig.DHCPOptions.DHCPServerAddress != "192.168.100.2" {
		t.Errorf("DHCPServerAddress = %q, want 192.168.100.2", entity.Status.Resources.IPConfig.DHCPOptions.DHCPServerAddress)
	}
	if entity.Status.Resources.IPConfig.DHCPOptions.DomainName != "example.local" {
		t.Errorf("DomainName = %q, want example.local", entity.Status.Resources.IPConfig.DHCPOptions.DomainName)
	}
	if len(entity.Status.Resources.IPConfig.PoolList) != 1 || entity.Status.Resources.IPConfig.PoolList[0].Range != "192.168.100.100-192.168.100.200" {
		t.Errorf("PoolList = %+v, want one range 192.168.100.100-192.168.100.200", entity.Status.Resources.IPConfig.PoolList)
	}
}

// TestSubnetV4Raw_ApplyToModel confirms the full raw-JSON -> toEntity ->
// ApplyTo pipeline (the same path the collector uses) produces the
// expected model.Network fields, mirroring the v3 IPPoolRanges join format
// ("start-end", comma-separated across multiple pools).
func TestSubnetV4Raw_ApplyToModel(t *testing.T) {
	r := subnetV4Raw{
		ExtID:            "subnet-2",
		Name:             "overlay-1",
		ClusterReference: "cluster-b",
		NetworkID:        7654321,
		SubnetType:       "OVERLAY",
	}
	entity := r.toEntity()

	m := &model.Network{}
	entity.ApplyTo(m)

	if m.ID != "subnet-2" || m.NetworkUUID != "subnet-2" {
		t.Errorf("expected ID/NetworkUUID subnet-2, got ID=%q NetworkUUID=%q", m.ID, m.NetworkUUID)
	}
	if m.Cluster != "cluster-b" {
		t.Errorf("Cluster = %q, want cluster-b", m.Cluster)
	}
	if m.VlanID != 7654321 {
		t.Errorf("VlanID = %d, want 7654321", m.VlanID)
	}
	if m.SubnetType != "OVERLAY" {
		t.Errorf("SubnetType = %q, want OVERLAY", m.SubnetType)
	}
}

// TestSubnetV4Raw_MultiplePoolsJoined confirms multiple IPv4 pools produce
// a comma-joined range list, matching v3's IPPoolRanges convention.
func TestSubnetV4Raw_MultiplePoolsJoined(t *testing.T) {
	raw := `{
		"extId": "subnet-3",
		"ipConfig": [
			{
				"ipv4": {
					"poolList": [
						{"startIp": {"value": "10.0.0.10"}, "endIp": {"value": "10.0.0.20"}},
						{"startIp": {"value": "10.0.0.100"}, "endIp": {"value": "10.0.0.120"}}
					]
				}
			}
		]
	}`
	var r subnetV4Raw
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entity := r.toEntity()

	m := &model.Network{}
	entity.ApplyTo(m)

	want := "10.0.0.10-10.0.0.20,10.0.0.100-10.0.0.120"
	if m.IPPoolRanges != want {
		t.Errorf("IPPoolRanges = %q, want %q", m.IPPoolRanges, want)
	}
}

// TestClusterV4Raw_JSONShape guards against the wire schema drifting out
// of sync with Nutanix's own published SDK (clustermgmt-go-client), for
// the same reason subnetV4Raw's equivalent test exists.
func TestClusterV4Raw_JSONShape(t *testing.T) {
	raw := `{
		"extId": "cluster-1",
		"name": "prod-cluster",
		"vmCount": 25,
		"config": {
			"buildInfo": {"version": "6.8.2", "fullVersion": "el7.3-release-fraser-6.8.2-stable"},
			"clusterArch": "X86_64",
			"operationMode": "NORMAL",
			"timezone": "America/Los_Angeles",
			"clusterFunction": ["AOS"]
		},
		"network": {"externalAddress": {"ipv4": {"value": "10.10.1.50"}}},
		"nodes": {"numberOfNodes": 2}
	}`

	var r clusterV4Raw
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entity := r.toEntity()

	if entity.Metadata.UUID != "cluster-1" || entity.Metadata.Name != "prod-cluster" {
		t.Errorf("unexpected metadata: %+v", entity.Metadata)
	}
	if entity.isPrismCentralCluster() {
		t.Error("expected an AOS cluster to not be classified as the Prism Central pseudo-cluster")
	}

	m := &model.Cluster{}
	entity.ApplyTo(m)

	if m.ClusterUUID != "cluster-1" {
		t.Errorf("ClusterUUID = %q, want cluster-1", m.ClusterUUID)
	}
	if m.Version != "6.8.2" {
		t.Errorf("Version = %q, want 6.8.2", m.Version)
	}
	if m.BuildVersion != "el7.3-release-fraser-6.8.2-stable" {
		t.Errorf("BuildVersion = %q, want el7.3-release-fraser-6.8.2-stable", m.BuildVersion)
	}
	if m.ClusterArch != "X86_64" {
		t.Errorf("ClusterArch = %q, want X86_64", m.ClusterArch)
	}
	if m.OperationMode != "NORMAL" {
		t.Errorf("OperationMode = %q, want NORMAL", m.OperationMode)
	}
	if m.Timezone != "America/Los_Angeles" {
		t.Errorf("Timezone = %q, want America/Los_Angeles", m.Timezone)
	}
	if m.ExternalIP != "10.10.1.50" {
		t.Errorf("ExternalIP = %q, want 10.10.1.50", m.ExternalIP)
	}
	if m.NumNodes != 2 {
		t.Errorf("NumNodes = %d, want 2", m.NumNodes)
	}
	if m.VMCount != 25 {
		t.Errorf("VMCount = %d, want 25", m.VMCount)
	}
}

// TestClusterV4Raw_PrismCentralPseudoClusterExcluded confirms a cluster
// whose v4 clusterFunction includes PRISM_CENTRAL is still recognized by
// the shared isPrismCentralCluster() check (originally written against
// v3's ServiceList), so withoutPrismCentralClusters() keeps excluding it
// for the v4 path too.
func TestClusterV4Raw_PrismCentralPseudoClusterExcluded(t *testing.T) {
	r := clusterV4Raw{
		ExtID: "pc-pseudo",
		Name:  "pc-pseudo-cluster",
	}
	raw := `{"extId": "pc-pseudo", "name": "pc-pseudo-cluster", "config": {"clusterFunction": ["PRISM_CENTRAL"]}}`
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entity := r.toEntity()

	if !entity.isPrismCentralCluster() {
		t.Fatal("expected a cluster with clusterFunction=[PRISM_CENTRAL] to be classified as the Prism Central pseudo-cluster")
	}

	filtered := withoutPrismCentralClusters([]clusterEntity{entity})
	if len(filtered) != 0 {
		t.Fatalf("expected withoutPrismCentralClusters to drop it, got %+v", filtered)
	}
}

// TestHostV4Raw_JSONShape guards against the wire schema drifting out of
// sync with Nutanix's own published SDK (clustermgmt-go-client), for the
// same reason clusterV4Raw/subnetV4Raw's equivalent tests exist.
func TestHostV4Raw_JSONShape(t *testing.T) {
	raw := `{
		"extId": "host-1",
		"hostName": "ahv-node-01",
		"cluster": {"extId": "cluster-1"},
		"nodeSerial": "SN-1",
		"blockModel": "NX-3060-G7",
		"cpuModel": "Intel Xeon Gold 6238",
		"cpuCapacityHz": 88000000000,
		"numberOfCpuCores": 32,
		"numberOfCpuSockets": 2,
		"numberOfCpuThreads": 64,
		"memorySizeBytes": 274877906944,
		"hypervisor": {"fullName": "Nutanix 20240802.100", "numberOfVms": 15},
		"ipmi": {"ip": {"ipv4": {"value": "10.10.1.60"}}}
	}`

	var r hostV4Raw
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entity := r.toEntity()

	if entity.Metadata.UUID != "host-1" || entity.Metadata.Name != "ahv-node-01" {
		t.Errorf("unexpected metadata: %+v", entity.Metadata)
	}
	if entity.clusterUUID() != "cluster-1" {
		t.Errorf("clusterUUID() = %q, want cluster-1", entity.clusterUUID())
	}

	m := &model.Host{}
	entity.ApplyTo(m)

	if m.SerialNumber != "SN-1" {
		t.Errorf("SerialNumber = %q, want SN-1", m.SerialNumber)
	}
	if m.BlockModel != "NX-3060-G7" {
		t.Errorf("BlockModel = %q, want NX-3060-G7", m.BlockModel)
	}
	if m.CPUModel != "Intel Xeon Gold 6238" {
		t.Errorf("CPUModel = %q, want Intel Xeon Gold 6238", m.CPUModel)
	}
	if m.CPUCapacityHz != 88000000000 {
		t.Errorf("CPUCapacityHz = %d, want 88000000000", m.CPUCapacityHz)
	}
	if m.NumCpuCores != 32 || m.NumCpuSockets != 2 || m.NumCpuThreads != 64 {
		t.Errorf("unexpected CPU topology: cores=%d sockets=%d threads=%d", m.NumCpuCores, m.NumCpuSockets, m.NumCpuThreads)
	}
	if m.MemoryCapacityMiB != 262144 {
		t.Errorf("MemoryCapacityMiB = %d, want 262144 (256 GiB)", m.MemoryCapacityMiB)
	}
	if m.HypervisorType != "Nutanix 20240802.100" {
		t.Errorf("HypervisorType = %q, want Nutanix 20240802.100", m.HypervisorType)
	}
	if m.NumVMs != 15 {
		t.Errorf("NumVMs = %d, want 15", m.NumVMs)
	}
	if m.IPMIAddress != "10.10.1.60" {
		t.Errorf("IPMIAddress = %q, want 10.10.1.60", m.IPMIAddress)
	}
}

// TestVMV4Raw_JSONShape guards against the wire schema drifting out of
// sync with Nutanix's own published SDK (vmm-go-client), the same reason
// this file's other v4 raw-struct tests exist. This is the
// highest-consequence entity of the four (a wrong mapping here can
// misconfigure an actual migrated VM), so it exercises the full shape:
// legacy boot, a container-backed disk, a cd-rom, a NIC with a learned IP,
// a serial port, and guest tools.
func TestVMV4Raw_JSONShape(t *testing.T) {
	raw := `{
		"extId": "vm-1",
		"name": "web-01",
		"description": "web tier",
		"cluster": {"extId": "cluster-1"},
		"host": {"extId": "host-1"},
		"powerState": "ON",
		"numSockets": 2,
		"numCoresPerSocket": 2,
		"numThreadsPerCore": 1,
		"memorySizeBytes": 4294967296,
		"machineType": "PC",
		"hardwareClockTimezone": "UTC",
		"isVgaConsoleEnabled": true,
		"bootConfig": {
			"$objectType": "vmm.v4.ahv.config.LegacyBoot",
			"bootOrder": ["DISK", "CDROM"]
		},
		"disks": [
			{
				"extId": "disk-1",
				"diskAddress": {"busType": "SCSI", "index": 0},
				"backingInfo": {
					"$objectType": "vmm.v4.ahv.config.VmDisk",
					"diskSizeBytes": 42949672960,
					"storageContainer": {"extId": "sc-1"}
				}
			}
		],
		"cdRoms": [
			{
				"extId": "cdrom-1",
				"diskAddress": {"busType": "IDE", "index": 0},
				"backingInfo": {
					"$objectType": "vmm.v4.ahv.config.VmDisk",
					"dataSource": {"reference": {"imageExtId": "img-1"}}
				}
			}
		],
		"nics": [
			{
				"extId": "nic-1",
				"backingInfo": {"isConnected": true, "macAddress": "50:6b:8d:aa:bb:01", "model": "VIRTIO"},
				"networkInfo": {
					"nicType": "NORMAL_NIC",
					"vlanMode": "ACCESS",
					"subnet": {"extId": "subnet-1"},
					"ipv4Info": {"learnedIpAddresses": [{"value": "192.168.100.101"}]}
				}
			}
		],
		"serialPorts": [{"index": 0, "isConnected": true}],
		"guestTools": {
			"isEnabled": true,
			"isIsoInserted": false,
			"isReachable": true,
			"version": "4.1.1",
			"guestInfo": {"guestOsFullName": "CentOS Linux 7"}
		}
	}`

	var r vmV4Raw
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entity := r.toEntity()

	m := &model.VM{}
	entity.ApplyTo(m)

	if m.UUID != "vm-1" || m.Name != "web-01" {
		t.Errorf("unexpected identity: uuid=%q name=%q", m.UUID, m.Name)
	}
	if m.Description != "web tier" {
		t.Errorf("Description = %q, want %q", m.Description, "web tier")
	}
	if m.Cluster != "cluster-1" {
		t.Errorf("Cluster = %q, want cluster-1", m.Cluster)
	}
	if m.Host != "host-1" {
		t.Errorf("Host = %q, want host-1", m.Host)
	}
	if m.PowerState != "ON" {
		t.Errorf("PowerState = %q, want ON", m.PowerState)
	}
	if m.NumSockets != 2 || m.NumVcpusPerSocket != 2 || m.NumThreadsPerCore != 1 {
		t.Errorf("unexpected CPU topology: %+v", m)
	}
	if m.MemorySizeMiB != 4096 {
		t.Errorf("MemorySizeMiB = %d, want 4096", m.MemorySizeMiB)
	}
	if m.MachineType != "PC" {
		t.Errorf("MachineType = %q, want PC", m.MachineType)
	}
	if m.BootType != "LEGACY" {
		t.Errorf("BootType = %q, want LEGACY", m.BootType)
	}
	if m.BootDeviceOrder != "DISK,CDROM" {
		t.Errorf("BootDeviceOrder = %q, want DISK,CDROM", m.BootDeviceOrder)
	}
	if !m.VGAConsoleEnabled {
		t.Error("expected VGAConsoleEnabled")
	}

	if len(m.Disks) != 2 {
		t.Fatalf("expected 2 disks (1 disk + 1 cd-rom), got %d", len(m.Disks))
	}
	disk, cdrom := m.Disks[0], m.Disks[1]
	if disk.UUID != "disk-1" || disk.IsCdrom || disk.DiskSizeBytes != 42949672960 || disk.StorageContainerUUID != "sc-1" || disk.AdapterType != "SCSI" {
		t.Errorf("unexpected disk: %+v", disk)
	}
	if cdrom.UUID != "cdrom-1" || !cdrom.IsCdrom || cdrom.DeviceType != "CDROM" || cdrom.SourceImageUUID != "img-1" || cdrom.AdapterType != "IDE" {
		t.Errorf("unexpected cd-rom: %+v", cdrom)
	}

	if len(m.NICs) != 1 {
		t.Fatalf("expected 1 NIC, got %d", len(m.NICs))
	}
	nic := m.NICs[0]
	if nic.MACAddress != "50:6b:8d:aa:bb:01" || nic.Model != "VIRTIO" || !nic.IsConnected {
		t.Errorf("unexpected NIC: %+v", nic)
	}
	if nic.SubnetUUID != "subnet-1" {
		t.Errorf("SubnetUUID = %q, want subnet-1", nic.SubnetUUID)
	}
	if len(nic.IPAddresses) != 1 || nic.IPAddresses[0] != "192.168.100.101" {
		t.Errorf("IPAddresses = %v, want [192.168.100.101]", nic.IPAddresses)
	}

	if len(m.SerialPorts) != 1 || !m.SerialPorts[0].IsConnected {
		t.Errorf("unexpected serial ports: %+v", m.SerialPorts)
	}

	if !m.GuestToolsEnabled || !m.GuestToolsReachable || m.GuestToolsVersion != "4.1.1" {
		t.Errorf("unexpected guest tools: enabled=%v reachable=%v version=%q", m.GuestToolsEnabled, m.GuestToolsReachable, m.GuestToolsVersion)
	}
	if m.GuestOSVersion != "CentOS Linux 7" {
		t.Errorf("GuestOSVersion = %q, want CentOS Linux 7", m.GuestOSVersion)
	}
}

// TestVMV4Raw_BootTypeVariants confirms the LegacyBoot/UefiBoot
// $objectType discriminator (and UefiBoot's IsSecureBootEnabled flag) map
// to the same BootType strings v3's boot_type already produces
// (LEGACY/UEFI/SECURE_BOOT), since the validator and builder packages key
// off those exact values.
func TestVMV4Raw_BootTypeVariants(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "legacy",
			raw:  `{"$objectType": "vmm.v4.ahv.config.LegacyBoot", "bootOrder": ["DISK"]}`,
			want: "LEGACY",
		},
		{
			name: "uefi",
			raw:  `{"$objectType": "vmm.v4.ahv.config.UefiBoot", "bootOrder": ["DISK"], "isSecureBootEnabled": false}`,
			want: "UEFI",
		},
		{
			name: "secure boot",
			raw:  `{"$objectType": "vmm.v4.ahv.config.UefiBoot", "bootOrder": ["DISK"], "isSecureBootEnabled": true}`,
			want: "SECURE_BOOT",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := vmV4Raw{ExtID: "vm-1"}
			full := `{"extId": "vm-1", "bootConfig": ` + c.raw + `}`
			if err := json.Unmarshal([]byte(full), &r); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			entity := r.toEntity()
			if entity.Spec.Resources.BootConfig.BootType != c.want {
				t.Errorf("BootType = %q, want %q", entity.Spec.Resources.BootConfig.BootType, c.want)
			}
		})
	}
}
