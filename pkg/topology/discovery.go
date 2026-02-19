package topology

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// NUMATopology represents the discovered NUMA topology of the system
type NUMATopology struct {
	Nodes []NUMANode
}

// NUMANode represents a single NUMA node
type NUMANode struct {
	ID int // NUMA node ID (0, 1, 2, etc.)
}

// DiscoverNUMATopology reads NUMA topology from /sys filesystem
func DiscoverNUMATopology() (*NUMATopology, error) {
	topology := &NUMATopology{Nodes: []NUMANode{}}

	// Check if NUMA is available
	numaPath := "/sys/devices/system/node"
	if _, err := os.Stat(numaPath); os.IsNotExist(err) {
		// No NUMA support - treat as single node system
		topology.Nodes = append(topology.Nodes, NUMANode{ID: 0})
		return topology, nil
	}

	// Read online nodes from /sys/devices/system/node/online
	onlinePath := "/sys/devices/system/node/online"
	onlineData, err := os.ReadFile(onlinePath)
	if err != nil {
		// Fallback to single node if we can't read online nodes
		topology.Nodes = append(topology.Nodes, NUMANode{ID: 0})
		return topology, nil
	}

	// Parse range (e.g., "0-1" or "0,2-3" or "0")
	nodeIDs, err := ParseRange(strings.TrimSpace(string(onlineData)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse NUMA node range: %w", err)
	}

	if len(nodeIDs) == 0 {
		// No nodes found - fallback to single node
		topology.Nodes = append(topology.Nodes, NUMANode{ID: 0})
		return topology, nil
	}

	for _, nodeID := range nodeIDs {
		topology.Nodes = append(topology.Nodes, NUMANode{ID: nodeID})
	}

	return topology, nil
}

// ParseRange parses a range string like "0-3" or "0,2,4-7" into a slice of integers
func ParseRange(rangeStr string) ([]int, error) {
	if rangeStr == "" {
		return nil, nil
	}

	var result []int

	// Split by comma first
	parts := strings.Split(rangeStr, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Check if this part is a range (contains hyphen)
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range: %s", part)
			}

			start, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid range start: %s", rangeParts[0])
			}

			end, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid range end: %s", rangeParts[1])
			}

			if start > end {
				return nil, fmt.Errorf("invalid range: start %d > end %d", start, end)
			}

			for i := start; i <= end; i++ {
				result = append(result, i)
			}
		} else {
			// Single number
			num, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid number: %s", part)
			}
			result = append(result, num)
		}
	}

	return result, nil
}

// GetNodeIDs returns a slice of all NUMA node IDs
func (t *NUMATopology) GetNodeIDs() []int {
	ids := make([]int, len(t.Nodes))
	for i, node := range t.Nodes {
		ids[i] = node.ID
	}
	return ids
}
