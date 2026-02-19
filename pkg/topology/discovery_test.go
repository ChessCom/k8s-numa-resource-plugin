package topology

import (
	"reflect"
	"testing"
)

func TestParseRange(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []int
		wantErr bool
	}{
		{
			name:    "single node",
			input:   "0",
			want:    []int{0},
			wantErr: false,
		},
		{
			name:    "simple range",
			input:   "0-3",
			want:    []int{0, 1, 2, 3},
			wantErr: false,
		},
		{
			name:    "comma separated",
			input:   "0,2,4",
			want:    []int{0, 2, 4},
			wantErr: false,
		},
		{
			name:    "mixed range and comma",
			input:   "0-2,4,6-8",
			want:    []int{0, 1, 2, 4, 6, 7, 8},
			wantErr: false,
		},
		{
			name:    "with spaces",
			input:   "0 - 2, 4",
			want:    []int{0, 1, 2, 4},
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "typical 2-node NUMA",
			input:   "0-1",
			want:    []int{0, 1},
			wantErr: false,
		},
		{
			name:    "typical 4-node NUMA",
			input:   "0-3",
			want:    []int{0, 1, 2, 3},
			wantErr: false,
		},
		{
			name:    "non-contiguous nodes",
			input:   "0,2",
			want:    []int{0, 2},
			wantErr: false,
		},
		{
			name:    "invalid - reversed range",
			input:   "3-0",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid - not a number",
			input:   "abc",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRange(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseRange() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNUMATopologyGetNodeIDs(t *testing.T) {
	topo := &NUMATopology{
		Nodes: []NUMANode{
			{ID: 0},
			{ID: 1},
			{ID: 3},
		},
	}

	got := topo.GetNodeIDs()
	want := []int{0, 1, 3}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetNodeIDs() = %v, want %v", got, want)
	}
}
