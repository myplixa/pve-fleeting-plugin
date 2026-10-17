package plugin

import (
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeCredentials writes body to path with the 0600 permissions a
// credentials file is expected to have, failing the test immediately on
// any write error.
func writeCredentials(t *testing.T, path, body string) {
	t.Helper()

	err := os.WriteFile(path, []byte(body), 0o600)
	require.NoError(t, err)
}

func TestInstanceGroup_getProxmoxClient(t *testing.T) {
	tempDir := t.TempDir()
	ig := InstanceGroup{
		Settings: Settings{
			URL:                   "https://example.com/proxmox",
			InsecureSkipTLSVerify: false,
			CredentialsFilePath:   path.Join(tempDir, "prox_credentials.json"),
		},
	}

	writeCredentials(t, ig.Settings.CredentialsFilePath, `{"token_id": "fleeting@pve!fleeting","token_secret": "03Ewl6rENi-rx-N503o"}`)

	_, err := ig.getProxmoxClient()
	require.NoError(t, err)
}

func TestInstanceGroup_getProxmoxAPIToken(t *testing.T) {
	tempDir := t.TempDir()
	ig := InstanceGroup{
		Settings: Settings{
			CredentialsFilePath: path.Join(tempDir, "sample_credentials.json"),
		},
	}

	// Missing credentials file
	_, err := ig.getProxmoxAPIToken()
	require.ErrorIs(t, err, os.ErrNotExist)

	// Malformed credentials file
	writeCredentials(t, ig.Settings.CredentialsFilePath, `{"token_id": 'fleeting@pve!fleeting',`)

	_, err = ig.getProxmoxAPIToken()
	require.Error(t, err)

	// Missing token_secret
	writeCredentials(t, ig.Settings.CredentialsFilePath, `{"token_id": "fleeting@pve!fleeting"}`)

	_, err = ig.getProxmoxAPIToken()
	require.ErrorIs(t, err, ErrSettingInvalidParameter)

	// Correct credentials file
	writeCredentials(t, ig.Settings.CredentialsFilePath, `{"token_id": "oQcW8N246FODI6Qui@pve!fleeting","token_secret": "88u3-kKLJ-gU7AfhWq"}`)

	token, err := ig.getProxmoxAPIToken()
	require.NoError(t, err)
	require.Equal(t, "oQcW8N246FODI6Qui@pve!fleeting", token.TokenID)
	require.Equal(t, "88u3-kKLJ-gU7AfhWq", token.TokenSecret)
}
