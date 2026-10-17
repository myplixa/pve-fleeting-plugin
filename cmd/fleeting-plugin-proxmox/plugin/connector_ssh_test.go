package plugin

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_proxmoxURLEncode asserts the property that actually matters for
// Proxmox's sshkeys config field: percent-decoding proxmoxURLEncode's
// output with Go's standard url.QueryUnescape must round-trip back to the
// original (trimmed) key string. This is a stronger assertion than
// checking specific %XX sequences, and it's the property that would catch
// an innocent-looking regression like swapping to url.PathEscape or
// dropping the "+" -> "%20" replace - see the doc comment on
// proxmoxURLEncode for why that replace is needed in the first place.
func Test_proxmoxURLEncode(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{
			name: "RSA public key",
			key:  "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC7v0J8f0mZ3n2zqk5f0gk1z9F2f1b6q4X3j7h1y2p3m4n5",
		},
		{
			name: "ed25519 public key",
			key:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJW7QX+1jQZqF2b3n1q5f0gk1z9F2f1b6q4X3j7h1y2p3",
		},
		{
			name: "ed25519 public key with trailing comment",
			key:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJW7QX+1jQZqF2b3n1q5f0gk1z9F2f1b6q4X3j7h1y2p3 user@host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := proxmoxURLEncode([]byte(tt.key))

			decoded, err := url.QueryUnescape(encoded)
			require.NoError(t, err)
			require.Equal(t, strings.TrimSpace(tt.key), decoded)
		})
	}
}
