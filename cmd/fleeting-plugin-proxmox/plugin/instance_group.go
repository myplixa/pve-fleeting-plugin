package plugin

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sync"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/luthermonson/go-proxmox"
	"gitlab.com/gitlab-org/fleeting/fleeting/provider"
	"golang.org/x/sync/errgroup"
)

var _ provider.InstanceGroup = (*InstanceGroup)(nil)

const triggerChannelCapacity = 100

// MaxInstances is the hard ceiling this plugin reports to GitLab Runner via
// ProviderInfo.MaxSize. The actual operational cap is [runners.autoscaler]'s
// own max_instances, which must stay at or below this value - see VM Naming
// in the README. Not user-configurable, same as the vSphere/ESXi plugin's
// own constant.
const MaxInstances = 50

type InstanceGroup struct {
	Settings         `json:",inline"`
	FleetingSettings provider.Settings `json:"-"`

	log     hclog.Logger    `json:"-"`
	proxmox *proxmox.Client `json:"-"`

	// This mutex is used when cloning template for new instances. It is required for blocking other
	// operations like collection or update, because when new instance is created with recycled ID then for
	// a brief period it will be reported from Proxmox with old name (e.g. InstanceNameRemoving).
	instanceCloningMu sync.Mutex `json:"-"`

	// Serializes the Proxmox clone task end-to-end (API call through task
	// completion), across every Increase call for this instance group - not
	// just within one. Proxmox's pool-add step at the end of a clone takes a
	// cluster-wide pmxcfs lock (cfs-lock 'file-user_cfg') that isn't scoped
	// per pool; running clones concurrently reliably trips "got lock request
	// timeout" under load, which also starves other pmxcfs-backed operations
	// (the web UI/API, since pmxcfs backs all of /etc/pve). A per-Increase-call
	// mutex isn't enough since GitLab Runner can call Increase multiple times
	// concurrently as it scales up - this needs to live on the instance group
	// itself to serialize across all of them.
	cloneMu sync.Mutex `json:"-"`

	// Trigger for collector to start removed instances collection.
	instanceCollectionTrigger chan struct{} `json:"-"`

	// Trigger to shutdown collector.
	collectorShutdownTrigger chan struct{} `json:"-"`

	// Wait group for the collector.
	collectorWaitGroup sync.WaitGroup `json:"-"`

	// Lifetime context for the collector, canceled by collectorCancel in
	// Shutdown. collectRemovedInstances derives its per-cycle timeout
	// context from this one, so canceling it on shutdown also cancels any
	// in-flight per-cycle work (an in-progress VM stop/delete task wait)
	// instead of letting Shutdown block for up to collectionTimeout.
	collectorCtx    context.Context    `json:"-"`
	collectorCancel context.CancelFunc `json:"-"`

	// SSH public key injected into deployed instances via cloud-init
	// (ciuser/sshkeys), generated in Init when not using static credentials.
	sshPubKey []byte `json:"-"`
}

// Init implements provider.InstanceGroup.
func (ig *InstanceGroup) Init(ctx context.Context, logger hclog.Logger, settings provider.Settings) (provider.ProviderInfo, error) {
	var err error

	ig.log = logger
	ig.FleetingSettings = settings
	ig.instanceCollectionTrigger = make(chan struct{}, triggerChannelCapacity)
	ig.collectorShutdownTrigger = make(chan struct{}, 1)
	ig.collectorCtx, ig.collectorCancel = context.WithCancel(context.Background())

	if err := ig.Settings.CheckRequiredFields(); err != nil {
		return provider.ProviderInfo{}, err
	}

	ig.Settings.FillWithDefaults()

	if ig.Settings.InsecureSkipTLSVerify {
		ig.log.Warn("TLS verification for Proxmox client is disabled, connections will be insecure")
	}

	ig.proxmox, err = ig.getProxmoxClient()
	if err != nil {
		return provider.ProviderInfo{}, err
	}

	// Tracks whether this process generated a fresh SSH key (as opposed to
	// the operator supplying a fixed one via connector_config.key). A
	// self-generated key is different on every restart, so any instance
	// already in -running state from before this restart has an
	// authorized_keys entry for the *old* key and will reject the new one -
	// see markStaleInstancesForRemoval below.
	//
	// TODO(2.1 Option B): persist the generated key to disk (e.g. next to
	// credentials_file_path) and load it back here instead of generating a
	// fresh one every restart, so running instances survive a plugin
	// restart without being torn down. Needs careful handling of file
	// permissions (match the credentials-file 0600 convention) and a
	// decision about key rotation.
	selfGeneratedKey := false

	if !ig.FleetingSettings.UseStaticCredentials {
		if ig.FleetingSettings.Protocol == "" {
			ig.FleetingSettings.Protocol = provider.ProtocolSSH
		}

		if ig.FleetingSettings.Username == "" {
			ig.FleetingSettings.Username = "fleeting"
		}

		if ig.FleetingSettings.Key == nil {
			key, err := generateSshKey()
			if err != nil {
				return provider.ProviderInfo{}, fmt.Errorf("generating ssh key: %w", err)
			}

			ig.FleetingSettings.Key = key
			selfGeneratedKey = true
		}

		pubKey, err := getSshPubKey(ig.FleetingSettings.Key)
		if err != nil {
			return provider.ProviderInfo{}, fmt.Errorf("deriving ssh public key: %w", err)
		}

		ig.sshPubKey = pubKey
	}

	if err := ig.markStaleInstancesForRemoval(ctx, selfGeneratedKey); err != nil {
		return provider.ProviderInfo{}, err
	}

	// Sleep for a bit to give Proxmox a chance to propagate renames for stale instances
	// Without this sleep these instances would be reported as creating during first Update
	<-time.After(collectionWaitAfterTrigger)

	//nolint:contextcheck
	ig.startRemovedInstanceCollector()

	return provider.ProviderInfo{
		ID:        path.Join("proxmox", ig.Settings.Name, ig.Settings.Pool),
		MaxSize:   MaxInstances,
		Version:   Version.String(),
		BuildInfo: Version.BuildInfo(),
	}, nil
}

// Heartbeat implements provider.InstanceGroup.
func (ig *InstanceGroup) Heartbeat(_ context.Context, _ string) error {
	return nil
}

// Shutdown implements provider.InstanceGroup.
func (ig *InstanceGroup) Shutdown(_ context.Context) error {
	ig.collectorShutdownTrigger <- struct{}{}
	ig.collectorCancel()

	ig.collectorWaitGroup.Wait()

	return nil
}

// Increase implements provider.InstanceGroup.
func (ig *InstanceGroup) Increase(ctx context.Context, count int) (int, error) {
	template, err := ig.getProxmoxVM(ctx, *ig.Settings.TemplateID)
	if err != nil {
		return 0, fmt.Errorf("failed to find template with id='%d': %w", *ig.Settings.TemplateID, err)
	}

	var (
		errorGroup = new(errgroup.Group)

		succeeded   = 0
		succeededMu = new(sync.Mutex)
	)

	for n := 0; n < count; n++ {
		errorGroup.Go(func() error {
			vmid, err := ig.deployInstance(ctx, template)
			if err != nil {
				ig.log.Error("failed to deploy an instance", "vmid", vmid, "err", err)
				return err
			}

			ig.log.Info("successfully deployed instance", "vmid", vmid)
			succeededMu.Lock()
			succeeded++
			succeededMu.Unlock()

			return nil
		})
	}

	if err := errorGroup.Wait(); err != nil {
		return succeeded, fmt.Errorf("failed to create one or more instances: %w", err)
	}

	return succeeded, nil
}

// Update implements provider.InstanceGroup.
func (ig *InstanceGroup) Update(ctx context.Context, update func(instance string, state provider.State)) error {
	ig.instanceCloningMu.Lock()
	defer ig.instanceCloningMu.Unlock()

	instances, err := ig.listInstances(ctx)
	if err != nil {
		return err
	}

	for _, member := range instances {
		state, ok := ig.instanceState(member.Name)
		if !ok {
			continue // Unknown name, skipping...
		}

		update(member.Name, state)
	}

	return nil
}

// ConnectInfo implements provider.InstanceGroup.
func (ig *InstanceGroup) ConnectInfo(ctx context.Context, instance string) (provider.ConnectInfo, error) {
	vm, err := ig.getProxmoxVMByName(ctx, instance)
	if err != nil {
		return provider.ConnectInfo{}, fmt.Errorf("failed to retrieve instance name='%s': %w", instance, err)
	}

	internalAddress, externalAddress, err := ig.getInstanceAddresses(ctx, vm)
	if err != nil {
		return provider.ConnectInfo{}, fmt.Errorf("failed to retrieve instance name='%s' network address: %w", instance, err)
	}

	info := provider.ConnectInfo{
		ID:              instance,
		InternalAddr:    internalAddress,
		ExternalAddr:    externalAddress,
		ConnectorConfig: ig.FleetingSettings.ConnectorConfig,
	}

	if info.UseStaticCredentials {
		return info, nil
	}

	if info.Protocol == "" {
		info.Protocol = provider.ProtocolSSH
	}

	if info.Username == "" {
		info.Username = ig.FleetingSettings.Username
	}

	if info.Protocol == provider.ProtocolSSH && info.Key == nil {
		info.Key = ig.FleetingSettings.Key
	}

	return info, nil
}

// Decrease implements provider.InstanceGroup.
func (ig *InstanceGroup) Decrease(ctx context.Context, instancesToRemove []string) ([]string, error) {
	ig.instanceCloningMu.Lock()
	defer ig.instanceCloningMu.Unlock()

	instances, err := ig.listInstances(ctx)
	if err != nil {
		return []string{}, err
	}

	var (
		errorGroup = new(errgroup.Group)

		succeeded   = []string{}
		succeededMu = new(sync.Mutex)
	)

	for _, member := range instances {
		if !slices.Contains(instancesToRemove, member.Name) {
			continue
		}

		if hasInstanceNamePrefix(member.Name, ig.Settings.InstanceNameCreating) {
			// It must be running to start the deletion
			continue
		}

		if hasInstanceNamePrefix(member.Name, ig.Settings.InstanceNameRemoving) {
			// Already deleting...
			succeededMu.Lock()
			succeeded = append(succeeded, member.Name)
			succeededMu.Unlock()

			continue
		}

		ig.log.Info("removing instance", "vmid", member.VMID, "name", member.Name)

		errorGroup.Go(func() error {
			if err := ig.markInstancesForRemoval(ctx, &member); err != nil {
				return err
			}

			succeededMu.Lock()
			defer succeededMu.Unlock()
			succeeded = append(succeeded, member.Name)

			return nil
		})
	}

	//nolint:wrapcheck
	return succeeded, errorGroup.Wait()
}
