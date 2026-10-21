package plugin

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/luthermonson/go-proxmox"
	"golang.org/x/crypto/ssh"
)

type PrivPub interface {
	crypto.PrivateKey
	Public() crypto.PublicKey
}

func getSshPubKey(privKey []byte) ([]byte, error) {
	priv, err := ssh.ParseRawPrivateKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("reading private key: %w", err)
	}

	key, ok := priv.(PrivPub)
	if !ok {
		return nil, errors.New("key doesn't export PublicKey()")
	}

	pubkey, err := ssh.NewPublicKey(key.Public())
	if err != nil {
		return nil, fmt.Errorf("generating ssh public key: %w", err)
	}

	return ssh.MarshalAuthorizedKey(pubkey), nil
}

func generateSshKey() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, fmt.Errorf("generating private key: %w", err)
	}

	eKey := pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		})

	if eKey == nil {
		return nil, errors.New("encoding private key")
	}

	return eKey, nil
}

// configureCloudInitUser injects the generated SSH key into a freshly
// cloned instance via cloud-init (ciuser/sshkeys), before it is started. A
// no-op when using static credentials (the template is then expected to
// already have working SSH access baked in).
func (ig *InstanceGroup) configureCloudInitUser(ctx context.Context, vm *proxmox.VirtualMachine) error {
	if ig.FleetingSettings.UseStaticCredentials || len(ig.sshPubKey) == 0 {
		return nil
	}

	options := []proxmox.VirtualMachineOption{
		{Name: "ciuser", Value: ig.FleetingSettings.Username},
		{Name: "sshkeys", Value: proxmoxURLEncode(ig.sshPubKey)},
		// Proxmox defaults ciupgrade to 1, injecting package_upgrade: true
		// into the generated cloud-init user-data. That runs apt dist-upgrade
		// on every single first boot, slowing down (and sometimes failing,
		// e.g. on transient mirror/network issues) an otherwise short-lived
		// CI runner instance for no benefit - the template is expected to
		// already be reasonably up to date.
		{Name: "ciupgrade", Value: 0},
	}

	return ig.applyVMConfig(ctx, vm, "cloud-init user", options...)
}

// configureCloudInitVendorData points the instance's cicustom vendor-data at
// Settings.CloudInitVendorData, if set. A no-op otherwise. Applies
// regardless of UseStaticCredentials - vendor-data provisioning (extra
// groups, files, package installs, disk mounts) is orthogonal to how SSH
// access is set up. The referenced snippet must already exist on Proxmox's
// snippets storage - see the field doc on Settings.CloudInitVendorData.
func (ig *InstanceGroup) configureCloudInitVendorData(ctx context.Context, vm *proxmox.VirtualMachine) error {
	if ig.Settings.CloudInitVendorData == "" {
		return nil
	}

	return ig.applyVMConfig(ctx, vm, "cloud-init vendor-data", proxmox.VirtualMachineOption{
		Name:  "cicustom",
		Value: "vendor=" + ig.Settings.CloudInitVendorData,
	})
}

// proxmoxURLEncode percent-encodes an SSH public key for Proxmox's sshkeys
// config field. url.QueryEscape is almost right (it already escapes "+" and
// "/" from the base64 body to "%2B"/"%2F"), except it encodes a literal
// space as "+" (form-encoding), which Proxmox's own strict RFC 3986-style
// urldecode rejects. Since genuine "+" characters from the key body are
// already the 3-byte "%2B" sequence by this point, a plain string replace
// only touches the space encoding.
func proxmoxURLEncode(key []byte) string {
	trimmed := strings.TrimSpace(string(key))

	return strings.ReplaceAll(url.QueryEscape(trimmed), "+", "%20")
}
