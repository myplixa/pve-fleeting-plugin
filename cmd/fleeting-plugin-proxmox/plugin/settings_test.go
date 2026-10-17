package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

//nolint:gochecknoglobals
var (
	sampleName = "sample_name"
	sampleURL  = "https://example.com"
	//nolint:gosec
	sampleCredentialsPath      = "/tmp/proxmox_credentials.json"
	samplePool                 = "sample_pool"
	sampleStorage              = "sample_storage"
	sampleTemplateID           = 20
	sampleInstanceNameCreating = "proxmox-creating"
	sampleInstanceNameRunning  = "running-prox"
	sampleInstanceNameRemoving = "proxve-removing"
)

func TestSettings_fillWithDefaults(t *testing.T) {
	settings := Settings{}
	settings.FillWithDefaults()

	// Don't use consts here, we want to ensure they are not changed
	require.False(t, settings.InsecureSkipTLSVerify)
	require.Equal(t, "fleeting-creating", settings.InstanceNameCreating)
	require.Equal(t, "fleeting-running", settings.InstanceNameRunning)
	require.Equal(t, "fleeting-removing", settings.InstanceNameRemoving)
	require.Equal(t, "ens18", settings.InstanceNetworkInterface)
	require.Equal(t, "ipv4", settings.InstanceNetworkProtocol)

	settings2 := Settings{
		Name: sampleName,
	}
	settings2.FillWithDefaults()

	require.Equal(t, sampleName+"-creating", settings2.InstanceNameCreating)
	require.Equal(t, sampleName+"-running", settings2.InstanceNameRunning)
	require.Equal(t, sampleName+"-removing", settings2.InstanceNameRemoving)

	settings3 := Settings{
		Name:                 sampleName,
		InstanceNameCreating: sampleInstanceNameCreating,
		InstanceNameRunning:  sampleInstanceNameRunning,
		InstanceNameRemoving: sampleInstanceNameRemoving,
	}
	settings3.FillWithDefaults()

	require.Equal(t, sampleInstanceNameCreating, settings3.InstanceNameCreating)
	require.Equal(t, sampleInstanceNameRunning, settings3.InstanceNameRunning)
	require.Equal(t, sampleInstanceNameRemoving, settings3.InstanceNameRemoving)
}

// validSettings returns a Settings with all fields required by
// CheckRequiredFields populated with valid sample values, then applies
// mutate (if non-nil) so each test case only has to spell out the one
// field it cares about instead of repeating the whole literal.
func validSettings(mutate func(*Settings)) Settings {
	s := Settings{
		Name:                sampleName,
		URL:                 sampleURL,
		CredentialsFilePath: sampleCredentialsPath,
		Pool:                samplePool,
		Storage:             sampleStorage,
		TemplateID:          &sampleTemplateID,
	}

	if mutate != nil {
		mutate(&s)
	}

	return s
}

func TestSettings_checkRequiredFields(t *testing.T) {
	tests := []struct {
		name          string
		settings      Settings
		expectedError error
	}{
		{
			name:          "Missing name",
			settings:      validSettings(func(s *Settings) { s.Name = "" }),
			expectedError: ErrRequiredSettingMissing,
		},
		{
			name:          "Missing URL",
			settings:      validSettings(func(s *Settings) { s.URL = "" }),
			expectedError: ErrRequiredSettingMissing,
		},
		{
			name:          "Missing credentials file path",
			settings:      validSettings(func(s *Settings) { s.CredentialsFilePath = "" }),
			expectedError: ErrRequiredSettingMissing,
		},
		{
			name:          "Missing pool",
			settings:      validSettings(func(s *Settings) { s.Pool = "" }),
			expectedError: ErrRequiredSettingMissing,
		},
		{
			name:          "Missing storage",
			settings:      validSettings(func(s *Settings) { s.Storage = "" }),
			expectedError: nil,
		},
		{
			name:          "Missing template id",
			settings:      validSettings(func(s *Settings) { s.TemplateID = nil }),
			expectedError: ErrRequiredSettingMissing,
		},
		{
			name:          "No missing parameters",
			settings:      validSettings(nil),
			expectedError: nil,
		},
		{
			name:          "Invalid protocol",
			settings:      validSettings(func(s *Settings) { s.InstanceNetworkProtocol = "invalid-protocol" }),
			expectedError: ErrSettingInvalidParameter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.settings.CheckRequiredFields()
			require.ErrorIs(t, err, tt.expectedError)
		})
	}
}
