package nutanix

import (
	"strings"

	libclient "github.com/kubev2v/forklift/pkg/lib/client/nutanix"
)

// Version-specific API shapes. Normalizers convert these into the canonical
// v3-style entities in resource.go so ApplyTo stays shared across Prism modes.

type storageContainerV2Raw struct {
	ClusterUUID          string         `json:"cluster_uuid"`
	CompressionEnabled   bool           `json:"compression_enabled"`
	ErasureCode          string         `json:"erasure_code"`
	MaxCapacity          int64          `json:"max_capacity"`
	MaxCapacityBytes     int64          `json:"max_capacity_bytes"`
	Name                 string         `json:"name"`
	OnDiskDedup          string         `json:"on_disk_dedup"`
	ReplicationFactor    int            `json:"replication_factor"`
	StorageContainerUUID string         `json:"storage_container_uuid"`
	TotalCapacity        int64          `json:"total_capacity"`
	UsageStats           map[string]any `json:"usage_stats"`
	UUID                 string         `json:"uuid"`
}

func (r storageContainerV2Raw) toEntity() storageContainerEntity {
	uuid := libclient.Coalesce(r.StorageContainerUUID, r.UUID)
	maxCapacity := r.MaxCapacityBytes
	if maxCapacity == 0 {
		maxCapacity = coalesceInt64(r.MaxCapacity, r.TotalCapacity)
	}

	usageBytes := int64(0)
	if r.UsageStats != nil {
		if value, ok := libclient.ParseNumericString(r.UsageStats["storage.user_usage_bytes"]); ok {
			usageBytes = value
		} else if value, ok := libclient.ParseNumericString(r.UsageStats["storage.reserved_usage_bytes"]); ok {
			usageBytes = value
		}
	}

	return storageContainerEntity{
		Metadata: libclient.Metadata{
			Name: r.Name,
			UUID: uuid,
		},
		Status: storageContainerStatus{
			Resources: storageContainerResources{
				ClusterReference:   libclient.Ref{UUID: r.ClusterUUID},
				CompressionEnabled: r.CompressionEnabled,
				ErasureCode:        r.ErasureCode,
				MaxCapacityBytes:   maxCapacity,
				OnDiskDedup:        r.OnDiskDedup,
				ReplicationFactor:  r.ReplicationFactor,
				UsageBytes:         usageBytes,
			},
		},
	}
}

type storageContainerV4Raw struct {
	ClusterExtID         string `json:"clusterExtId"`
	ClusterExtIDAlt      string `json:"cluster_ext_id"`
	CompressionEnabled   bool   `json:"compression_enabled"`
	ContainerExtID       string `json:"container_ext_id"`
	ContainerExtIDAlt    string `json:"containerExtId"`
	ErasureCode          string `json:"erasure_code"`
	ErasureCodeAlt       string `json:"erasureCode"`
	ExtID                string `json:"extId"`
	IsCompressionEnabled bool   `json:"isCompressionEnabled"`
	MaxCapacityBytes     int64  `json:"max_capacity_bytes"`
	MaxCapacityBytesAlt  int64  `json:"maxCapacityBytes"`
	Name                 string `json:"name"`
	OnDiskDedup          string `json:"on_disk_dedup"`
	OnDiskDedupAlt       string `json:"onDiskDedup"`
	ReplicationFactor    int    `json:"replication_factor"`
	ReplicationFactorAlt int    `json:"replicationFactor"`
	UsageBytes           int64  `json:"usage_bytes"`
	UsageBytesAlt        int64  `json:"usageBytes"`
}

func (r storageContainerV4Raw) toEntity() storageContainerEntity {
	uuid := libclient.Coalesce(r.ExtID, r.ContainerExtIDAlt, r.ContainerExtID)
	clusterUUID := libclient.Coalesce(r.ClusterExtIDAlt, r.ClusterExtID)

	return storageContainerEntity{
		Metadata: libclient.Metadata{
			Name: r.Name,
			UUID: uuid,
		},
		Status: storageContainerStatus{
			Resources: storageContainerResources{
				ClusterReference: libclient.Ref{UUID: clusterUUID},
				CompressionEnabled: coalesceBool(
					r.IsCompressionEnabled,
					r.CompressionEnabled,
				),
				ErasureCode: libclient.Coalesce(r.ErasureCodeAlt, r.ErasureCode),
				MaxCapacityBytes: coalesceInt64(
					r.MaxCapacityBytesAlt,
					r.MaxCapacityBytes,
				),
				OnDiskDedup: libclient.Coalesce(r.OnDiskDedupAlt, r.OnDiskDedup),
				ReplicationFactor: coalesceInt(
					r.ReplicationFactorAlt,
					r.ReplicationFactor,
				),
				UsageBytes: coalesceInt64(r.UsageBytesAlt, r.UsageBytes),
			},
		},
	}
}

type imageV4Raw struct {
	ExtID     string `json:"extId"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	Source    struct {
		URL string `json:"url"`
	} `json:"source"`
	Type string `json:"type"`
}

func (r imageV4Raw) toEntity() imageEntity {
	entity := imageEntity(libclient.V3Image{
		Metadata: libclient.Metadata{UUID: r.ExtID},
	})
	entity.Status.Name = r.Name
	entity.Status.Resources = libclient.V3ImageResources{
		ImageType: r.Type,
		SizeBytes: r.SizeBytes,
		SourceURI: r.Source.URL,
	}
	return entity
}

// subnetV4Raw is the v4 networking API's Subnet entity shape (Prism
// Central only). Field names/nesting verified against Nutanix's own
// currently-published Go client
// (github.com/nutanix/ntnx-api-golang-clients, networking-go-client/
// models/networking/v4/config/config_model.go), not guessed -- this
// endpoint has no populated entities on any lab used during development
// to round-trip against directly. Top-level fields carry a defensive
// snake_case alternate (matching this file's existing v2/v4 raw structs'
// convention for exactly this kind of uncertainty); deeply-nested fields
// don't, since the SDK source is the strongest evidence available for
// those and doubling every leaf would just be noise.
type subnetV4Raw struct {
	ExtID            string `json:"extId"`
	ExtIDAlt         string `json:"ext_id"`
	Name             string `json:"name"`
	ClusterReference string `json:"clusterReference"`
	ClusterRefAlt    string `json:"cluster_reference"`
	NetworkID        int    `json:"networkId"`
	NetworkIDAlt     int    `json:"network_id"`
	SubnetType       string `json:"subnetType"`
	SubnetTypeAlt    string `json:"subnet_type"`
	IPConfig         []struct {
		IPv4 *struct {
			IPSubnet *struct {
				IP struct {
					Value string `json:"value"`
				} `json:"ip"`
				PrefixLength int `json:"prefixLength"`
			} `json:"ipSubnet"`
			DefaultGatewayIP *struct {
				Value string `json:"value"`
			} `json:"defaultGatewayIp"`
			DhcpServerAddress *struct {
				Value string `json:"value"`
			} `json:"dhcpServerAddress"`
			PoolList []struct {
				StartIP struct {
					Value string `json:"value"`
				} `json:"startIp"`
				EndIP struct {
					Value string `json:"value"`
				} `json:"endIp"`
			} `json:"poolList"`
		} `json:"ipv4"`
	} `json:"ipConfig"`
	DhcpOptions *struct {
		DomainName string `json:"domainName"`
	} `json:"dhcpOptions"`
}

func (r subnetV4Raw) toEntity() networkEntity {
	entity := networkEntity{}
	entity.Metadata.UUID = libclient.Coalesce(r.ExtID, r.ExtIDAlt)
	entity.Metadata.Name = r.Name
	clusterUUID := libclient.Coalesce(r.ClusterReference, r.ClusterRefAlt)
	entity.Spec.ClusterReference = libclient.Ref{UUID: clusterUUID}
	entity.Status.ClusterReference = libclient.Ref{UUID: clusterUUID}
	entity.Status.Name = r.Name
	entity.Status.Resources.SubnetType = libclient.Coalesce(r.SubnetTypeAlt, r.SubnetType)
	entity.Status.Resources.VlanID = coalesceInt(r.NetworkIDAlt, r.NetworkID)

	if len(r.IPConfig) > 0 && r.IPConfig[0].IPv4 != nil {
		ipv4 := r.IPConfig[0].IPv4
		if ipv4.IPSubnet != nil {
			entity.Status.Resources.IPConfig.SubnetIP = ipv4.IPSubnet.IP.Value
			entity.Status.Resources.IPConfig.PrefixLength = ipv4.IPSubnet.PrefixLength
		}
		if ipv4.DefaultGatewayIP != nil {
			entity.Status.Resources.IPConfig.DefaultGatewayIP = ipv4.DefaultGatewayIP.Value
		}
		if ipv4.DhcpServerAddress != nil {
			entity.Status.Resources.IPConfig.DHCPOptions.DHCPServerAddress = ipv4.DhcpServerAddress.Value
		}
		for _, pool := range ipv4.PoolList {
			rangeStr := pool.StartIP.Value
			if pool.EndIP.Value != "" {
				rangeStr += "-" + pool.EndIP.Value
			}
			entity.Status.Resources.IPConfig.PoolList = append(
				entity.Status.Resources.IPConfig.PoolList,
				struct {
					Range string `json:"range"`
				}{Range: rangeStr},
			)
		}
	}
	if r.DhcpOptions != nil {
		entity.Status.Resources.IPConfig.DHCPOptions.DomainName = r.DhcpOptions.DomainName
	}

	return entity
}

// clusterV4Raw is the v4 clustermgmt API's Cluster entity shape (Prism
// Central only). Field names/nesting verified against Nutanix's own
// currently-published Go client (clustermgmt-go-client/models/
// clustermgmt/v4/config/config_model.go), not guessed -- like subnetV4Raw,
// no lab used during development had entities to round-trip against
// directly.
//
// Known gap: v4's Cluster entity (from this list endpoint) carries no
// storage-capacity fields equivalent to v3's status.resources.analysis.
// storage -- that data lives behind a separate /stats/clusters/{extId}
// call this collector doesn't make (avoiding an N+1 call per cluster).
// TotalCapacity/UsedCapacity are left at zero for Prism Central-sourced
// clusters as a result; every other field maps directly.
type clusterV4Raw struct {
	ExtID    string `json:"extId"`
	ExtIDAlt string `json:"ext_id"`
	Name     string `json:"name"`
	VmCount  int64  `json:"vmCount"`
	Config   *struct {
		BuildInfo *struct {
			Version     string `json:"version"`
			FullVersion string `json:"fullVersion"`
		} `json:"buildInfo"`
		ClusterArch     string   `json:"clusterArch"`
		OperationMode   string   `json:"operationMode"`
		Timezone        string   `json:"timezone"`
		ClusterFunction []string `json:"clusterFunction"`
	} `json:"config"`
	Network *struct {
		ExternalAddress *struct {
			IPv4 *struct {
				Value string `json:"value"`
			} `json:"ipv4"`
		} `json:"externalAddress"`
	} `json:"network"`
	Nodes *struct {
		NumberOfNodes int `json:"numberOfNodes"`
	} `json:"nodes"`
}

func (r clusterV4Raw) toEntity() clusterEntity {
	entity := clusterEntity{}
	entity.Metadata.UUID = libclient.Coalesce(r.ExtID, r.ExtIDAlt)
	entity.Metadata.Name = r.Name
	entity.Spec.Name = r.Name
	entity.Status.Name = r.Name
	entity.Status.Resources.Analysis.VMCount = r.VmCount

	if r.Config != nil {
		if r.Config.BuildInfo != nil {
			entity.Status.Resources.Config.Build.Version = r.Config.BuildInfo.Version
			entity.Status.Resources.Config.Build.FullVersion = r.Config.BuildInfo.FullVersion
		}
		entity.Status.Resources.Config.ClusterArch = r.Config.ClusterArch
		entity.Status.Resources.Config.OperationMode = r.Config.OperationMode
		entity.Status.Resources.Config.Timezone = r.Config.Timezone
		// v4's ClusterFunctionRef enum names the PRISM_CENTRAL case
		// identically to v3's ServiceList entry, so
		// isPrismCentralCluster()'s existing ServiceList check works
		// unchanged once mapped through here.
		entity.Status.Resources.Config.ServiceList = r.Config.ClusterFunction
	}
	if r.Network != nil && r.Network.ExternalAddress != nil && r.Network.ExternalAddress.IPv4 != nil {
		entity.Status.Resources.Network.ExternalIP = r.Network.ExternalAddress.IPv4.Value
	}
	if r.Nodes != nil {
		// v3's NumNodes is populated from len(HypervisorServerList) in
		// ApplyTo; pad a slice of that length so the shared ApplyTo logic
		// (which counts entries, not a direct field) works unchanged for
		// both Prism modes.
		entity.Status.Resources.Nodes.HypervisorServerList = make([]struct {
			IP string `json:"ip"`
		}, r.Nodes.NumberOfNodes)
	}

	return entity
}

// hostV4Raw is the v4 clustermgmt API's Host entity shape (Prism Central
// only). Field names/nesting verified against Nutanix's own currently-
// published Go client (clustermgmt-go-client/models/clustermgmt/v4/
// config/config_model.go), not guessed -- same caveat as
// clusterV4Raw/subnetV4Raw: no lab used during development has entities
// on this endpoint to round-trip against directly.
//
// model.Host.State is deliberately left unmapped (empty) here rather than
// guessed: v4's Host has both a `maintenanceState` string field (no
// typed enum/value list in the SDK to confirm what it actually returns)
// and a separate `nodeStatus` enum (NORMAL/TO_BE_REMOVED/...) that is a
// generic node-lifecycle concept, not obviously equivalent to v3's
// status.state ("COMPLETE" etc, confirmed via a live probe -- see Tier 1
// in the design doc). `maintenanceState` is a promising lead for closing
// the MaintenanceMode validator gap, but wiring it needs its actual value
// strings confirmed first, which this pass didn't do.
type hostV4Raw struct {
	ExtID           string `json:"extId"`
	ExtIDAlt        string `json:"ext_id"`
	HostName        string `json:"hostName"`
	ClusterExtID    string `json:"clusterExtId"`
	NodeSerial      string `json:"nodeSerial"`
	BlockModel      string `json:"blockModel"`
	HostType        string `json:"hostType"`
	CpuModel        string `json:"cpuModel"`
	CpuCapacityHz   int64  `json:"cpuCapacityHz"`
	NumberOfCores   int    `json:"numberOfCpuCores"`
	NumberOfSockets int    `json:"numberOfCpuSockets"`
	NumberOfThreads int    `json:"numberOfCpuThreads"`
	MemorySizeBytes int64  `json:"memorySizeBytes"`
	Cluster         *struct {
		ExtID string `json:"extId"`
	} `json:"cluster"`
	Hypervisor *struct {
		FullName    string `json:"fullName"`
		NumberOfVms int    `json:"numberOfVms"`
	} `json:"hypervisor"`
	Ipmi *struct {
		IP *struct {
			IPv4 *struct {
				Value string `json:"value"`
			} `json:"ipv4"`
		} `json:"ip"`
	} `json:"ipmi"`
}

func (r hostV4Raw) toEntity() hostEntity {
	entity := hostEntity{}
	uuid := libclient.Coalesce(r.ExtID, r.ExtIDAlt)
	entity.Metadata.UUID = uuid
	entity.Metadata.Name = r.HostName
	entity.Status.Name = r.HostName

	clusterUUID := r.ClusterExtID
	if r.Cluster != nil && r.Cluster.ExtID != "" {
		clusterUUID = r.Cluster.ExtID
	}
	entity.Spec.ClusterReference = libclient.Ref{UUID: clusterUUID}
	entity.Status.ClusterReference = libclient.Ref{UUID: clusterUUID}

	res := &entity.Status.Resources
	res.SerialNumber = r.NodeSerial
	res.Block.BlockModel = r.BlockModel
	res.HostType = r.HostType
	res.CPUModel = r.CpuModel
	res.CPUCapacityHz = r.CpuCapacityHz
	res.NumCpuCores = r.NumberOfCores
	res.NumCpuSockets = r.NumberOfSockets
	res.NumCpuThreads = r.NumberOfThreads
	if r.MemorySizeBytes > 0 {
		res.MemoryCapacityMiB = r.MemorySizeBytes / 1024 / 1024
	}
	if r.Hypervisor != nil {
		res.Hypervisor.HypervisorFullName = r.Hypervisor.FullName
		res.Hypervisor.NumVMs = r.Hypervisor.NumberOfVms
	}
	if r.Ipmi != nil && r.Ipmi.IP != nil && r.Ipmi.IP.IPv4 != nil {
		res.IPMI.IP = r.Ipmi.IP.IPv4.Value
	}

	return entity
}

func coalesceInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	if len(values) > 0 {
		return values[0]
	}
	return 0
}

func coalesceInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	if len(values) > 0 {
		return values[0]
	}
	return 0
}

func coalesceBool(values ...bool) bool {
	for _, value := range values {
		if value {
			return true
		}
	}
	return false
}

// vmV4Raw is the v4 vmm API's Vm entity shape (Prism Central only). Field
// names/nesting verified against Nutanix's own currently-published Go
// client (vmm-go-client/models/vmm/v4/ahv/config/config_model.go) and its
// separately-published VM API definition (vmm-go-client/api/vm_api.go,
// confirming the list path GET /api/vmm/v4.3/ahv/config/vms used by
// vmsV4Path), same unverified-against-a-live-server caveat as this file's
// other v4 raw structs.
//
// Known gaps, both deliberate rather than guessed:
//   - model.VM.Categories is left empty for Prism Central-sourced VMs. v4's
//     Vm.Categories only carries a bare extId per entry
//     (CategoryReference{ExtId}), unlike v3's metadata.categories map of
//     key:value strings -- resolving the key:value pair needs a separate
//     category-by-extId lookup this collector doesn't make (the same
//     N+1-avoidance tradeoff as clusterV4Raw's capacity-stats gap).
//   - model.VM.GuestOSID is left empty: v4 has no guestOsId-equivalent
//     field anywhere on Vm or GuestTools, only the free-text
//     GuestInfo.GuestOsFullName (mapped to GuestOSVersion below, which is
//     what TemplateLabels' substring classifier already keys off -- see
//     Tier 2 in the design doc).
//   - Volume-group-backed disks (Disk.BackingInfo's
//     ADSFVolumeGroupReference variant, as opposed to the normal
//     container-backed VmDisk variant) map to a disk entry with a UUID and
//     bus address but no size/container, since that data lives on the
//     volume group's own disk entities, not here.
type vmDiskAddressV4Raw struct {
	BusType string `json:"busType"`
	Index   int    `json:"index"`
}

// vmDiskBackingV4Raw covers both of Disk.BackingInfo's polymorphic
// variants (VmDisk and ADSFVolumeGroupReference) in a single struct, since
// their field sets don't collide -- avoids needing a second $objectType
// switch on top of vmV4Raw's BootConfig one.
type vmDiskBackingV4Raw struct {
	DiskSizeBytes int64 `json:"diskSizeBytes"`
	DataSource    *struct {
		Reference *struct {
			ImageExtID string `json:"imageExtId"`
		} `json:"reference"`
	} `json:"dataSource"`
	StorageContainer *struct {
		ExtID string `json:"extId"`
	} `json:"storageContainer"`
	VolumeGroupExtID string `json:"volumeGroupExtId"`
}

type vmDiskItemV4Raw struct {
	ExtID       string              `json:"extId"`
	DiskAddress *vmDiskAddressV4Raw `json:"diskAddress"`
	BackingInfo *vmDiskBackingV4Raw `json:"backingInfo"`
}

type vmNicBackingV4Raw struct {
	IsConnected bool   `json:"isConnected"`
	MacAddress  string `json:"macAddress"`
	Model       string `json:"model"`
}

type vmNicNetworkInfoV4Raw struct {
	NicType  string `json:"nicType"`
	VlanMode string `json:"vlanMode"`
	Subnet   *struct {
		ExtID string `json:"extId"`
	} `json:"subnet"`
	Ipv4Info *struct {
		LearnedIPAddresses []struct {
			Value string `json:"value"`
		} `json:"learnedIpAddresses"`
	} `json:"ipv4Info"`
}

type vmNicV4Raw struct {
	ExtID       string                 `json:"extId"`
	BackingInfo *vmNicBackingV4Raw     `json:"backingInfo"`
	NetworkInfo *vmNicNetworkInfoV4Raw `json:"networkInfo"`
}

type vmV4Raw struct {
	ExtID       string `json:"extId"`
	ExtIDAlt    string `json:"ext_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Cluster     *struct {
		ExtID string `json:"extId"`
	} `json:"cluster"`
	Host *struct {
		ExtID string `json:"extId"`
	} `json:"host"`
	PowerState            string `json:"powerState"`
	NumSockets            int    `json:"numSockets"`
	NumCoresPerSocket     int    `json:"numCoresPerSocket"`
	NumThreadsPerCore     int    `json:"numThreadsPerCore"`
	MemorySizeBytes       int64  `json:"memorySizeBytes"`
	MachineType           string `json:"machineType"`
	HardwareClockTimezone string `json:"hardwareClockTimezone"`
	IsVgaConsoleEnabled   bool   `json:"isVgaConsoleEnabled"`
	BootConfig            *struct {
		ObjectType          string   `json:"$objectType"`
		BootOrder           []string `json:"bootOrder"`
		IsSecureBootEnabled *bool    `json:"isSecureBootEnabled"`
	} `json:"bootConfig"`
	Disks       []vmDiskItemV4Raw `json:"disks"`
	CdRoms      []vmDiskItemV4Raw `json:"cdRoms"`
	Nics        []vmNicV4Raw      `json:"nics"`
	SerialPorts []struct {
		Index       int  `json:"index"`
		IsConnected bool `json:"isConnected"`
	} `json:"serialPorts"`
	GuestTools *struct {
		IsEnabled     bool   `json:"isEnabled"`
		IsIsoInserted bool   `json:"isIsoInserted"`
		IsReachable   bool   `json:"isReachable"`
		Version       string `json:"version"`
		GuestInfo     *struct {
			GuestOsFullName string `json:"guestOsFullName"`
		} `json:"guestInfo"`
	} `json:"guestTools"`
}

// vmDiskFromV4 converts one disk or cd-rom entry into the shared
// libclient.VMDisk shape applyDisk() already knows how to render. CD-ROMs
// are folded into the same model.Disk list as regular disks (with
// DeviceType "CDROM"/IsCdrom true) because every downstream consumer
// (validator, builder) already expects that v3-era mixed-list shape.
func vmDiskFromV4(item vmDiskItemV4Raw, isCdrom bool) libclient.VMDisk {
	disk := libclient.VMDisk{UUID: item.ExtID}
	disk.DeviceProperties.DeviceType = "DISK"
	if isCdrom {
		disk.DeviceProperties.DeviceType = "CDROM"
	}
	if item.DiskAddress != nil {
		disk.DeviceProperties.DiskAddress.AdapterType = item.DiskAddress.BusType
		disk.DeviceProperties.DiskAddress.DeviceIndex = item.DiskAddress.Index
	}
	if item.BackingInfo != nil {
		disk.DiskSizeBytes = item.BackingInfo.DiskSizeBytes
		if item.BackingInfo.DataSource != nil && item.BackingInfo.DataSource.Reference != nil {
			disk.DataSourceReference = libclient.Ref{UUID: item.BackingInfo.DataSource.Reference.ImageExtID}
		}
		if item.BackingInfo.StorageContainer != nil {
			disk.StorageContainerReference = libclient.Ref{UUID: item.BackingInfo.StorageContainer.ExtID}
		}
	}
	return disk
}

func (r vmV4Raw) toEntity() vmEntity {
	entity := vmEntity{}
	entity.Metadata.UUID = libclient.Coalesce(r.ExtID, r.ExtIDAlt)
	entity.Metadata.Name = r.Name
	entity.Spec.Name = r.Name
	entity.Spec.Description = r.Description

	if r.Cluster != nil {
		entity.Spec.ClusterReference = libclient.Ref{UUID: r.Cluster.ExtID}
	}
	if r.Host != nil {
		entity.Status.Resources.HostReference = libclient.Ref{UUID: r.Host.ExtID}
	}

	res := &entity.Spec.Resources
	res.PowerState = r.PowerState
	res.NumSockets = r.NumSockets
	res.NumVcpusPerSocket = r.NumCoresPerSocket
	res.NumThreadsPerCore = r.NumThreadsPerCore
	res.MemorySizeMiB = r.MemorySizeBytes / 1024 / 1024
	res.MachineType = r.MachineType
	res.HardwareClockTZ = r.HardwareClockTimezone
	res.VGAConsoleEnabled = r.IsVgaConsoleEnabled

	if r.BootConfig != nil {
		res.BootConfig.BootDeviceOrderList = r.BootConfig.BootOrder
		switch {
		case strings.HasSuffix(r.BootConfig.ObjectType, "UefiBoot"):
			if r.BootConfig.IsSecureBootEnabled != nil && *r.BootConfig.IsSecureBootEnabled {
				res.BootConfig.BootType = "SECURE_BOOT"
			} else {
				res.BootConfig.BootType = "UEFI"
			}
		case strings.HasSuffix(r.BootConfig.ObjectType, "LegacyBoot"):
			res.BootConfig.BootType = "LEGACY"
		}
	}

	for _, d := range r.Disks {
		res.DiskList = append(res.DiskList, vmDiskFromV4(d, false))
	}
	for _, c := range r.CdRoms {
		res.DiskList = append(res.DiskList, vmDiskFromV4(c, true))
	}

	for _, n := range r.Nics {
		nic := libclient.VMNIC{UUID: n.ExtID}
		if n.BackingInfo != nil {
			nic.IsConnected = n.BackingInfo.IsConnected
			nic.MACAddress = n.BackingInfo.MacAddress
			nic.Model = n.BackingInfo.Model
		}
		if n.NetworkInfo != nil {
			nic.NicType = n.NetworkInfo.NicType
			nic.VlanMode = n.NetworkInfo.VlanMode
			if n.NetworkInfo.Subnet != nil {
				nic.SubnetReference = libclient.Ref{UUID: n.NetworkInfo.Subnet.ExtID}
			}
			if n.NetworkInfo.Ipv4Info != nil {
				for _, ip := range n.NetworkInfo.Ipv4Info.LearnedIPAddresses {
					nic.IPEndpointList = append(nic.IPEndpointList, struct {
						IP string `json:"ip"`
					}{IP: ip.Value})
				}
			}
		}
		res.NICList = append(res.NICList, nic)
	}

	for _, p := range r.SerialPorts {
		res.SerialPortList = append(res.SerialPortList, libclient.VMSerialPort{
			Index:       p.Index,
			IsConnected: p.IsConnected,
		})
	}

	if r.GuestTools != nil {
		ngt := &res.GuestTools.NutanixGuestTools
		ngt.Enabled = r.GuestTools.IsEnabled
		ngt.IsReachable = r.GuestTools.IsReachable
		ngt.Version = r.GuestTools.Version
		if r.GuestTools.IsIsoInserted {
			ngt.ISOMountState = "MOUNTED"
		}
		if r.GuestTools.GuestInfo != nil {
			ngt.GuestOSVersion = r.GuestTools.GuestInfo.GuestOsFullName
		}
	}

	return entity
}
