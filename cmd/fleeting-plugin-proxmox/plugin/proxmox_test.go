package plugin

import (
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstanceGroup_getProxmoxClient(t *testing.T) {
	tempDir := t.TempDir()
	ig := InstanceGroup{
		Settings: Settings{
			URL:                   "https://example.com/proxmox",
			InsecureSkipTLSVerify: false,
			CredentialsFilePath:   path.Join(tempDir, "prox_credentials.json"),
		},
	}

	err := os.WriteFile(
		ig.Settings.CredentialsFilePath,
		[]byte(`{"token_id": "fleeting@pve!fleeting","token_secret": "03Ewl6rENi-rx-N503o"}`),
		0o600,
	)
	require.NoError(t, err)

	_, err = ig.getProxmoxClient()
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
	err = os.WriteFile(
		ig.Settings.CredentialsFilePath,
		[]byte(`{"token_id": 'fleeting@pve!fleeting',`),
		0o600,
	)
	require.NoError(t, err)

	_, err = ig.getProxmoxAPIToken()
	require.Error(t, err)

	// Missing token_secret
	err = os.WriteFile(
		ig.Settings.CredentialsFilePath,
		[]byte(`{"token_id": "fleeting@pve!fleeting"}`),
		0o600,
	)
	require.NoError(t, err)

	_, err = ig.getProxmoxAPIToken()
	require.ErrorIs(t, err, ErrSettingInvalidParameter)

	// Correct credentials file
	err = os.WriteFile(
		ig.Settings.CredentialsFilePath,
		[]byte(`{"token_id": "oQcW8N246FODI6Qui@pve!fleeting","token_secret": "88u3-kKLJ-gU7AfhWq"}`),
		0o600,
	)
	require.NoError(t, err)

	token, err := ig.getProxmoxAPIToken()
	require.NoError(t, err)
	require.Equal(t, "oQcW8N246FODI6Qui@pve!fleeting", token.TokenID)
	require.Equal(t, "88u3-kKLJ-gU7AfhWq", token.TokenSecret)
}
