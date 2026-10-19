package plugin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/luthermonson/go-proxmox"
)

var ErrNotFound = errors.New("not found")

func (ig *InstanceGroup) getProxmoxPool(ctx context.Context) (*proxmox.Pool, error) {
	pool, err := ig.proxmox.Pool(ctx, ig.Settings.Pool)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool id='%s': %w", ig.Settings.Pool, err)
	}

	return pool, nil
}

// Where possible, use getProxmoxVMOnNode instead as it makes less calls to API.
func (ig *InstanceGroup) getProxmoxVM(ctx context.Context, vmid int) (*proxmox.VirtualMachine, error) {
	pool, err := ig.getProxmoxPool(ctx)
	if err != nil {
		return nil, err
	}

	for _, member := range pool.Members {
		if member.Type != "qemu" {
			continue
		}

		if member.VMID == uint64(vmid) {
			return ig.getProxmoxVMOnNode(ctx, vmid, member.Node)
		}
	}

	return nil, ErrNotFound
}

// getProxmoxVMByName resolves one of this instance group's own instances
// (see isProxmoxResourceAnInstance) by its current pool-listing name - the
// public identifier handed to GitLab Runner via Update/ConnectInfo/Decrease,
// since it's what shows up in job logs instead of an opaque VMID.
func (ig *InstanceGroup) getProxmoxVMByName(ctx context.Context, name string) (*proxmox.VirtualMachine, error) {
	instances, err := ig.listInstances(ctx)
	if err != nil {
		return nil, err
	}

	for _, member := range instances {
		if member.Name == name {
			return ig.getProxmoxVMOnNode(ctx, int(member.VMID), member.Node)
		}
	}

	return nil, ErrNotFound
}

func (ig *InstanceGroup) getProxmoxVMOnNode(ctx context.Context, vmid int, nodeName string) (*proxmox.VirtualMachine, error) {
	node, err := ig.proxmox.Node(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get node='%s': %w", nodeName, err)
	}

	vm, err := node.VirtualMachine(ctx, vmid)
	if err != nil {
		return nil, fmt.Errorf("failed to get vm='%d' on node='%s': %w", vmid, nodeName, err)
	}

	return vm, nil
}

func (ig *InstanceGroup) getProxmoxClient() (*proxmox.Client, error) {
	url, err := url.Parse(ig.Settings.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL='%s': %w", ig.Settings.URL, err)
	}

	token, err := ig.getProxmoxAPIToken()
	if err != nil {
		return nil, err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		//nolint:gosec
		InsecureSkipVerify: ig.Settings.InsecureSkipTLSVerify,
	}

	httpClient := http.Client{
		Transport: transport,
	}

	return proxmox.NewClient(
		url.JoinPath("/api2/json").String(),
		proxmox.WithAPIToken(token.TokenID, token.TokenSecret),
		proxmox.WithHTTPClient(&httpClient),
	), nil
}

// apiToken mirrors the subset of Proxmox VE API token fields that
// credentials_file_path supplies. Token auth is stateless (no session
// ticket to refresh), unlike username/password login.
type apiToken struct {
	TokenID     string `json:"token_id"`
	TokenSecret string `json:"token_secret"`
}

func (ig *InstanceGroup) getProxmoxAPIToken() (*apiToken, error) {
	credentialsFile, err := os.Open(ig.Settings.CredentialsFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open credentials file from path='%s': %w", ig.Settings.CredentialsFilePath, err)
	}
	defer credentialsFile.Close()

	if info, statErr := credentialsFile.Stat(); statErr == nil {
		if info.Mode().Perm()&0o077 != 0 {
			ig.log.Warn("credentials file is readable by group/other, consider chmod 600", "path", ig.Settings.CredentialsFilePath)
		}
	}

	token := apiToken{}
	if err := json.NewDecoder(credentialsFile).Decode(&token); err != nil {
		return nil, fmt.Errorf("failed to decode credentials file from path='%s': %w", ig.Settings.CredentialsFilePath, err)
	}

	if token.TokenID == "" || token.TokenSecret == "" {
		return nil, fmt.Errorf("%w: credentials file must set token_id and token_secret", ErrSettingInvalidParameter)
	}

	return &token, nil
}
