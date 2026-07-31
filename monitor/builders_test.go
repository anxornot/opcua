package monitor

import (
	"testing"

	"github.com/gopcua/opcua/ua"
)

func TestBuildCreateRequestKeepsCallerParameters(t *testing.T) {
	filter := &ua.ExtensionObject{}
	// Every value differs from the constructor's defaults (SamplingInterval 0,
	// QueueSize 10, DiscardOldest true, Filter nil), so a request that ignored
	// the caller cannot pass by coincidence.
	caller := &ua.MonitoringParameters{
		SamplingInterval: 250,
		QueueSize:        42,
		DiscardOldest:    false,
		Filter:           filter,
	}
	node := Request{
		NodeID:               ua.NewNumericNodeID(0, 2258),
		MonitoringParameters: caller,
	}

	got := buildCreateRequest(node, 7).RequestedParameters

	if got.SamplingInterval != 250 {
		t.Errorf("SamplingInterval = %v, want 250", got.SamplingInterval)
	}
	if got.QueueSize != 42 {
		t.Errorf("QueueSize = %d, want 42", got.QueueSize)
	}
	if got.DiscardOldest {
		t.Error("DiscardOldest = true, want false")
	}
	if got.Filter != filter {
		t.Errorf("Filter = %v, want the caller's filter", got.Filter)
	}
	if got.ClientHandle != 7 {
		t.Errorf("ClientHandle = %d, want 7", got.ClientHandle)
	}
}

// This test is a regression guard for issue #880, not a driver: it passes
// against the first stub of buildCreateRequest and must keep passing.
func TestBuildCreateRequestDoesNotWriteCallerParameters(t *testing.T) {
	shared := &ua.MonitoringParameters{SamplingInterval: 100, QueueSize: 5}
	before := *shared
	node := Request{
		NodeID:               ua.NewNumericNodeID(0, 2258),
		MonitoringParameters: shared,
	}

	first := buildCreateRequest(node, 11)
	second := buildCreateRequest(node, 12)

	if *shared != before {
		t.Errorf("caller's parameters = %+v, want them untouched at %+v", *shared, before)
	}
	if first.RequestedParameters == shared || second.RequestedParameters == shared {
		t.Error("request aliases the caller's parameters, want a copy")
	}
	if first.RequestedParameters == second.RequestedParameters {
		t.Error("both requests share one parameters struct, want one per call")
	}
	if first.RequestedParameters.ClientHandle != 11 {
		t.Errorf("first ClientHandle = %d, want 11", first.RequestedParameters.ClientHandle)
	}
	if second.RequestedParameters.ClientHandle != 12 {
		t.Errorf("second ClientHandle = %d, want 12", second.RequestedParameters.ClientHandle)
	}
}
