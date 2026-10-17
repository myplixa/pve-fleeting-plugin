package plugin

import (
	"testing"

	"github.com/luthermonson/go-proxmox"
	"github.com/stretchr/testify/require"
)

// dualStackIfaces returns a single "ens18" interface with two IPv4
// addresses (one global, one private) and two IPv6 addresses (one global,
// one ULA) - the fixture shared by the "Any"/"Forced IPv4"/"Forced IPv6"
// test cases below.
func dualStackIfaces() []*proxmox.AgentNetworkIface {
	return []*proxmox.AgentNetworkIface{
		{
			Name: "ens18",
			IPAddresses: []*proxmox.AgentNetworkIPAddress{
				{
					IPAddressType: "ipv4",
					IPAddress:     "8.8.8.8",
				},
				{
					IPAddressType: "ipv4",
					IPAddress:     "192.168.0.1",
				},
				{
					IPAddressType: "ipv6",
					IPAddress:     "2001:4860:4860::8888",
				},
				{
					IPAddressType: "ipv6",
					IPAddress:     "fd3b:47fc:de09::1",
				},
			},
		},
	}
}

// internalOnlyIfaces returns a single "ens18" interface with only a
// private IPv4 address and a ULA IPv6 address (no globally routable
// address of either family) - the fixture shared by the "...with only
// internal address" test cases below.
func internalOnlyIfaces() []*proxmox.AgentNetworkIface {
	return []*proxmox.AgentNetworkIface{
		{
			Name: "ens18",
			IPAddresses: []*proxmox.AgentNetworkIPAddress{
				{
					IPAddressType: "ipv4",
					IPAddress:     "192.168.0.1",
				},
				{
					IPAddressType: "ipv6",
					IPAddress:     "fd3b:47fc:de09::1",
				},
			},
		},
	}
}

func Test_determineAddresses(t *testing.T) {
	tests := []struct {
		name string

		requestedInterface string
		requestedProtocol  NetworkProtocol
		networkInterfaces  []*proxmox.AgentNetworkIface

		expectedError           error
		expectedInternalAddress string
		expectedExternalAddress string
	}{
		{
			name: "No network interfaces",

			requestedInterface: "ens18",
			requestedProtocol:  NetworkProtocolAny,
			networkInterfaces:  []*proxmox.AgentNetworkIface{},

			expectedError:           ErrNoIPAddress,
			expectedInternalAddress: "",
			expectedExternalAddress: "",
		},
		{
			name: "Any",

			requestedInterface: "ens18",
			requestedProtocol:  NetworkProtocolAny,
			networkInterfaces:  dualStackIfaces(),

			expectedError:           nil,
			expectedInternalAddress: "fd3b:47fc:de09::1",
			expectedExternalAddress: "2001:4860:4860::8888",
		},
		{
			name: "Forced IPv4",

			requestedInterface: "ens18",
			requestedProtocol:  NetworkProtocolIPv4,
			networkInterfaces:  dualStackIfaces(),

			expectedError:           nil,
			expectedInternalAddress: "192.168.0.1",
			expectedExternalAddress: "8.8.8.8",
		},
		{
			name: "Forced IPv6",

			requestedInterface: "ens18",
			requestedProtocol:  NetworkProtocolIPv6,
			networkInterfaces:  dualStackIfaces(),

			expectedError:           nil,
			expectedInternalAddress: "fd3b:47fc:de09::1",
			expectedExternalAddress: "2001:4860:4860::8888",
		},
		{
			name: "Any with only internal address",

			requestedInterface: "ens18",
			requestedProtocol:  NetworkProtocolAny,
			networkInterfaces:  internalOnlyIfaces(),

			expectedError:           nil,
			expectedInternalAddress: "fd3b:47fc:de09::1",
			expectedExternalAddress: "fd3b:47fc:de09::1",
		},
		{
			name: "Forced IPv4 with only internal address",

			requestedInterface: "ens18",
			requestedProtocol:  NetworkProtocolIPv4,
			networkInterfaces:  internalOnlyIfaces(),

			expectedError:           nil,
			expectedInternalAddress: "192.168.0.1",
			expectedExternalAddress: "192.168.0.1",
		},
		{
			name: "Forced IPv6 with only internal address",

			requestedInterface: "ens18",
			requestedProtocol:  NetworkProtocolIPv6,
			networkInterfaces:  internalOnlyIfaces(),

			expectedError:           nil,
			expectedInternalAddress: "fd3b:47fc:de09::1",
			expectedExternalAddress: "fd3b:47fc:de09::1",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			internalAddress, externalAddress, err := determineAddresses(testCase.networkInterfaces, testCase.requestedInterface, testCase.requestedProtocol)

			require.ErrorIs(t, err, testCase.expectedError)
			require.Equal(t, testCase.expectedInternalAddress, internalAddress)
			require.Equal(t, testCase.expectedExternalAddress, externalAddress)
		})
	}
}
