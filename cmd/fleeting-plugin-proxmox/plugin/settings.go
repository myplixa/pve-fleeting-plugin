package plugin

import (
	"errors"
	"fmt"
	"regexp"
)

// diskKeyPattern matches a Proxmox disk key: a bus name followed by its
// index, e.g. "virtio0", "scsi1", "sata0", "ide2".
var diskKeyPattern = regexp.MustCompile(`^(ide|sata|scsi|virtio)\d+$`)

// InstanceDisk configures one disk slot on the deployed instance. If Disk
// already exists on the clone (inherited from the template), it is resized
// to SizeGB; otherwise a new disk of that size is allocated on Storage (or
// Settings.Storage, if Storage is left empty).
type InstanceDisk struct {
	// Proxmox disk key, e.g. "virtio0", "scsi1", "sata0".
	Disk string `json:"disk"`

	// Target size in GB. Resizing an existing disk smaller than its current
	// size is rejected by the Proxmox API at deploy time.
	SizeGB int `json:"size_gb"`

	// Proxmox VE storage to allocate this disk on, if it doesn't already
	// exist on the template. Falls back to Settings.Storage when empty, so
	// this only needs to be set when a disk should land on a different
	// storage pool than the rest of the instance.
	Storage string `json:"storage,omitempty"`
}

// InstanceNetwork groups the instance_network_* settings that configure the
// deployed instance's net0 device and cloud-init networking, and where to
// read its resulting IP address back from.
type InstanceNetwork struct {
	// Network interface to read instance's IP address from.
	Interface string `json:"interface,omitempty"`

	// Network protocol to look for when discovering instance's IP address.
	Protocol NetworkProtocol `json:"protocol,omitempty"`

	// Bridge to attach the deployed instance's net0 device to. If empty, the
	// clone keeps whatever bridge is configured on the template.
	Bridge string `json:"bridge,omitempty"`

	// VLAN tag to set on the deployed instance's net0 device. nil leaves the
	// tag untouched; 0 removes any tag (plain, untagged bridge access).
	VlanTag *int `json:"vlan_tag,omitempty"`

	// If true, configure cloud-init (ipconfig0=ip=dhcp,ip6=dhcp) on the
	// deployed instance so it actually requests a DHCP lease on boot. Needed
	// for cloud-init templates that don't already bring up networking on
	// their own (e.g. a bare template with no netplan/network-config baked
	// in). No effect on templates that already configure networking
	// themselves.
	DHCP bool `json:"dhcp,omitempty"`
}

var (
	ErrRequiredSettingMissing  = errors.New("required setting is missing")
	ErrSettingInvalidParameter = errors.New("setting has invalid parameter")
)

// Available network protocols to look for when discovering instance's IP address.
type NetworkProtocol = string

const (
	// Tries to find one internal and one external IPv4 address.
	NetworkProtocolIPv4 NetworkProtocol = "ipv4"

	// Tries to find one internal (ULA) and one global (GUA) IPv6 address.
	NetworkProtocolIPv6 NetworkProtocol = "ipv6"

	// Will prioritize IPv6 but return IPv4 if there is no IPv6.
	NetworkProtocolAny NetworkProtocol = "any"
)

// Default values for plugin settings.
const (
	DefaultInstanceNetworkInterface = "ens18"
	DefaultInstanceNetworkProtocol  = NetworkProtocolIPv4

	// Used to derive instance_name_* defaults when Name is unset (only
	// happens calling FillWithDefaults directly, bypassing
	// CheckRequiredFields, since Name is otherwise required).
	defaultNamePrefix = "fleeting"
)

// Plguin settings.
type Settings struct {
	// Identifies this instance group - used as the prefix for generated VM
	// names (see instance_name_* below) and, combined with Pool, to tell
	// this instance group's own VMs apart from others, including other
	// instance groups sharing the same pool.
	Name string `json:"name"`

	// Proxmox VE URL.
	URL string `json:"url"`

	// If true then TLS certificate verification is disabled.
	InsecureSkipTLSVerify bool `json:"insecure_skip_tls_verify"`

	// Path to Proxmox VE credentials file.
	CredentialsFilePath string `json:"credentials_file_path"`

	// Name of the Proxmox VE pool to use.
	Pool string `json:"pool"`

	// Name of the Proxmox VE storage to use.
	Storage string `json:"storage"`

	// ID of the Proxmox VE VM to create instances from.
	TemplateID *int `json:"template_id,omitempty"`

	// Network settings for the deployed instance (interface/protocol to
	// read its IP address from, bridge, VLAN tag, DHCP). See InstanceNetwork.
	InstanceNetwork InstanceNetwork `json:"instance_network,omitempty"`

	// Base name for instances during creation. Each instance gets a unique
	// "<base>-<shortUUID>" name. Defaults to "<Name>-creating".
	InstanceNameCreating string `json:"instance_name_creating,omitempty"`

	// Base name for running instances. Defaults to "<Name>-running".
	InstanceNameRunning string `json:"instance_name_running,omitempty"`

	// Base name for instances during removal. Defaults to "<Name>-removing".
	InstanceNameRemoving string `json:"instance_name_removing,omitempty"`

	// Overrides the cloned VM's vCPU core count (template's socket count is
	// left untouched). If unset (or 0), inherits the template's value.
	InstanceNumCPUs *int `json:"instance_num_cpus,omitempty"`

	// Overrides the cloned VM's memory size, in GB. If unset (or 0),
	// inherits the template's value.
	InstanceMemoryGB *int `json:"instance_memory_gb,omitempty"`

	// Per-disk overrides for the deployed instance - resizes a disk
	// inherited from the template, or allocates a new one if the disk key
	// doesn't exist on the template. See InstanceDisk.
	InstanceDisks []InstanceDisk `json:"instance_disks,omitempty"`
}

func (s *Settings) FillWithDefaults() {
	if s.InstanceNetwork.Interface == "" {
		s.InstanceNetwork.Interface = DefaultInstanceNetworkInterface
	}

	if s.InstanceNetwork.Protocol == "" {
		s.InstanceNetwork.Protocol = DefaultInstanceNetworkProtocol
	}

	namePrefix := s.Name
	if namePrefix == "" {
		namePrefix = defaultNamePrefix
	}

	if s.InstanceNameCreating == "" {
		s.InstanceNameCreating = namePrefix + "-creating"
	}

	if s.InstanceNameRunning == "" {
		s.InstanceNameRunning = namePrefix + "-running"
	}

	if s.InstanceNameRemoving == "" {
		s.InstanceNameRemoving = namePrefix + "-removing"
	}
}

func (s *Settings) CheckRequiredFields() error {
	if s.Name == "" {
		return fmt.Errorf("%w: name", ErrRequiredSettingMissing)
	}

	if s.URL == "" {
		return fmt.Errorf("%w: url", ErrRequiredSettingMissing)
	}

	if s.CredentialsFilePath == "" {
		return fmt.Errorf("%w: credentials_file_path", ErrRequiredSettingMissing)
	}

	if s.Pool == "" {
		return fmt.Errorf("%w: pool", ErrRequiredSettingMissing)
	}

	if s.TemplateID == nil {
		return fmt.Errorf("%w: template_id", ErrRequiredSettingMissing)
	}

	if s.InstanceNetwork.Protocol != "" && s.InstanceNetwork.Protocol != NetworkProtocolIPv4 && s.InstanceNetwork.Protocol != NetworkProtocolIPv6 && s.InstanceNetwork.Protocol != NetworkProtocolAny {
		return fmt.Errorf("%w: instance_network.protocol: must be ipv4, ipv6 or any", ErrSettingInvalidParameter)
	}

	if s.InstanceNetwork.VlanTag != nil && (*s.InstanceNetwork.VlanTag < 0 || *s.InstanceNetwork.VlanTag > 4094) {
		return fmt.Errorf("%w: instance_network.vlan_tag: must be between 0 and 4094", ErrSettingInvalidParameter)
	}

	if s.InstanceNumCPUs != nil && *s.InstanceNumCPUs < 0 {
		return fmt.Errorf("%w: instance_num_cpus: must be >= 0", ErrSettingInvalidParameter)
	}

	if s.InstanceMemoryGB != nil && *s.InstanceMemoryGB < 0 {
		return fmt.Errorf("%w: instance_memory_gb: must be >= 0", ErrSettingInvalidParameter)
	}

	// Which instance_disks entries are a fresh allocation vs. a resize of an
	// existing disk isn't known until deploy time (it depends on the
	// template), so each entry conservatively needs *some* storage to fall
	// back on - either its own or Settings.Storage - to build a valid
	// "<storage>:<size_gb>" Proxmox value if it does turn out to be new.
	for _, disk := range s.InstanceDisks {
		if !diskKeyPattern.MatchString(disk.Disk) {
			return fmt.Errorf("%w: instance_disks: disk: must match %s", ErrSettingInvalidParameter, diskKeyPattern.String())
		}

		if disk.SizeGB <= 0 {
			return fmt.Errorf("%w: instance_disks: size_gb: must be > 0", ErrSettingInvalidParameter)
		}

		if disk.Storage == "" && s.Storage == "" {
			return fmt.Errorf("%w: instance_disks: storage (or top-level storage)", ErrRequiredSettingMissing)
		}
	}

	return nil
}
