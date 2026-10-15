package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/luthermonson/go-proxmox"
)

var ErrNoIPAddress = errors.New("failed to determine IP address for instance")

const (
	// A freshly booted (or just re-networked, e.g. after a VLAN change)
	// instance can have its qemu-guest-agent respond before DHCP has handed
	// out a lease, so a single AgentGetNetworkIFaces call can transiently
	// come back without a usable address. Retry for a bit instead of
	// failing the whole ConnectInfo call outright.
	networkAddressRetryInterval = 5 * time.Second
	networkAddressRetryTimeout  = 1 * time.Minute
)

// getInstanceAddresses retries fetching and parsing the instance's network
// addresses for up to networkAddressRetryTimeout, to ride out the gap
// between the guest agent coming up and DHCP assigning an address.
func (ig *InstanceGroup) getInstanceAddresses(ctx context.Context, vm *proxmox.VirtualMachine) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, networkAddressRetryTimeout)
	defer cancel()

	var lastErr error

	for {
		if networkInterfaces, err := vm.AgentGetNetworkIFaces(ctx); err != nil {
			lastErr = err
		} else if internalAddress, externalAddress, err := determineAddresses(networkInterfaces, ig.InstanceNetworkInterface, ig.InstanceNetworkProtocol); err != nil {
			lastErr = err
		} else {
			return internalAddress, externalAddress, nil
		}

		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("timed out waiting for instance network address: %w", lastErr)
		case <-time.After(networkAddressRetryInterval):
		}
	}
}

// Determines internal and external address for given interfaces.
func determineAddresses(networkInterfaces []*proxmox.AgentNetworkIface, requestedInterface string, requestedProtocol NetworkProtocol) (string, string, error) {
	internalIPv4, externalIPv4, internalIPv6, externalIPv6 := determinePossibleAddresses(networkInterfaces, requestedInterface)

	// IPv6 (or Any)
	if requestedProtocol == NetworkProtocolIPv6 || requestedProtocol == NetworkProtocolAny {
		// External address is required so use internal if needed
		if externalIPv6 == "" {
			externalIPv6 = internalIPv6
		}

		if externalIPv6 != "" {
			return internalIPv6, externalIPv6, nil
		}
	}

	// IPv4 (or Any)
	if requestedProtocol == NetworkProtocolIPv4 || requestedProtocol == NetworkProtocolAny {
		// External address is required so use internal if needed
		if externalIPv4 == "" {
			externalIPv4 = internalIPv4
		}

		if externalIPv4 != "" {
			return internalIPv4, externalIPv4, nil
		}
	}

	// Did not find the address in requested protocol
	return "", "", ErrNoIPAddress
}

// Finds possible IPv4 and IPv6 addresses for given interfaces.
//
//nolint:nakedret,nonamedreturns
func determinePossibleAddresses(networkInterfaces []*proxmox.AgentNetworkIface, requestedInterface string) (internalIPv4, externalIPv4, internalIPv6, externalIPv6 string) {
	for _, networkInterface := range networkInterfaces {
		if networkInterface.Name != requestedInterface {
			continue
		}

		for _, address := range networkInterface.IPAddresses {
			parsedAddress := net.ParseIP(address.IPAddress)

			if parsedAddress == nil {
				continue
			}

			if parsedAddress.IsLoopback() || parsedAddress.IsUnspecified() {
				continue
			}

			if address.IPAddressType == "ipv4" {
				if parsedAddress.IsPrivate() {
					internalIPv4 = address.IPAddress
				} else if parsedAddress.IsGlobalUnicast() {
					externalIPv4 = address.IPAddress
				}
			}

			if address.IPAddressType == "ipv6" {
				if parsedAddress.IsPrivate() {
					internalIPv6 = address.IPAddress
				} else if parsedAddress.IsGlobalUnicast() {
					externalIPv6 = address.IPAddress
				}
			}
		}

		// We found requested interface so we can break the loop
		return
	}

	return
}
