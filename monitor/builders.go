package monitor

import (
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

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
