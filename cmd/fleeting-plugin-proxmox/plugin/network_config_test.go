package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int {
	return &v
}

func Test_buildNetworkOverride(t *testing.T) {
	tests := []struct {
		name string

		current string
		bridge  string
		vlanTag *int

		expectedNet0   string
		expectedChange bool
	}{
		{
			name:           "no overrides configured",
			current:        "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0",
			bridge:         "",
			vlanTag:        nil,
			expectedNet0:   "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0",
			expectedChange: false,
		},
		{
			name:           "plain bridge override",
			current:        "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0",
			bridge:         "vmbr1",
			vlanTag:        nil,
			expectedNet0:   "virtio=FE:A6:49:3A:8E:21,bridge=vmbr1",
			expectedChange: true,
		},
		{
			name:           "add vlan tag to untagged device",
			current:        "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0",
			bridge:         "",
			vlanTag:        intPtr(100),
			expectedNet0:   "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0,tag=100",
			expectedChange: true,
		},
		{
			name:           "bridge and vlan tag together",
			current:        "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0",
			bridge:         "vmbr1",
			vlanTag:        intPtr(300),
			expectedNet0:   "virtio=FE:A6:49:3A:8E:21,bridge=vmbr1,tag=300",
			expectedChange: true,
		},
		{
			name:           "replace existing vlan tag",
			current:        "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0,tag=100",
			bridge:         "",
			vlanTag:        intPtr(200),
			expectedNet0:   "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0,tag=200",
			expectedChange: true,
		},
		{
			name:           "remove vlan tag with explicit zero",
			current:        "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0,tag=100",
			bridge:         "",
			vlanTag:        intPtr(0),
			expectedNet0:   "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0",
			expectedChange: true,
		},
		{
			name:           "no-op when requested value already matches",
			current:        "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0,tag=100",
			bridge:         "vmbr0",
			vlanTag:        intPtr(100),
			expectedNet0:   "virtio=FE:A6:49:3A:8E:21,bridge=vmbr0,tag=100",
			expectedChange: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			net0, changed := buildNetworkOverride(testCase.current, testCase.bridge, testCase.vlanTag)
			require.Equal(t, testCase.expectedNet0, net0)
			require.Equal(t, testCase.expectedChange, changed)
		})
	}
}
