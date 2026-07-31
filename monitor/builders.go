package monitor

import (
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

// buildCreateRequest returns the create request for one node, with handle as
// its ClientHandle. node.MonitoringParameters is copied, never written to.
// The copy is shallow, so Filter stays shared with the caller; node.NodeID is
// likewise retained by reference in the returned request.
func buildCreateRequest(node Request, handle uint32) *ua.MonitoredItemCreateRequest {
	request := opcua.NewMonitoredItemCreateRequestWithDefaults(node.NodeID, ua.AttributeIDValue, handle)
	request.MonitoringMode = node.MonitoringMode

	if node.MonitoringParameters != nil {
		params := *node.MonitoringParameters
		params.ClientHandle = handle
		request.RequestedParameters = &params
	}

	return request
}

// buildModifyRequests returns the modify requests for the given nodes, resolving
// each node to its already-monitored item by NodeID. Nodes with nil
// MonitoringParameters, and nodes matching no item, are skipped. A node matching
// more than one item yields one request, naming the first match in items.
func buildModifyRequests(nodes []Request, items []Item) []*ua.MonitoredItemModifyRequest {
	requests := make([]*ua.MonitoredItemModifyRequest, 0)

	for _, node := range nodes {
		for _, item := range items {
			if item.nodeID.String() != node.NodeID.String() {
				continue
			}

			if node.MonitoringParameters == nil {
				break
			}

			params := *node.MonitoringParameters
			params.ClientHandle = item.handle
			requests = append(requests, &ua.MonitoredItemModifyRequest{
				MonitoredItemID:     item.id,
				RequestedParameters: &params,
			})
			break
		}
	}

	return requests
}
