package nutanix

import (
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
