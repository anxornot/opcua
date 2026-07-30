package monitor

import (
	"context"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"github.com/stretchr/testify/require"
)

// notificationTimeout caps how long a test waits for notifications. The server
// pushes an initial value as soon as a monitored item is created, so a passing
// run needs a small fraction of this. The cap is what keeps a failing run
// affordable: the notifications a regression loses never arrive, so the test
// waits out the whole deadline on every required CI context.
const notificationTimeout = 5 * time.Second

// testNodes are the variable nodes the loopback server exposes. Each one holds a
// different Int32, so the value in a notification says which node produced it
// independently of the NodeID the subscription reports it under — that is what
// lets a test check attribution rather than only how many nodes showed up. Two
// nodes would already go red on the handle defect; three keeps a partial
// collapse — two of the three sharing a handle — distinguishable from a total one.
var testNodes = []struct {
	name  string
	value int32
}{
	{"batch_a", 10},
	{"batch_b", 20},
	{"batch_c", 30},
}

// startTestServer starts a loopback server exposing testNodes as Int32
// variables, and returns its endpoint URL with the NodeIDs of those variables,
// in the same order as testNodes so a caller can pair each NodeID with the value
// behind it.
//
// The server is closed through t.Cleanup rather than by the caller, so that it
// outlives the client and subscription cleanups registered after it. Closing it
// first would leave Unsubscribe waiting out its request timeout against a dead
// listener.
//
// The port comes from binding 127.0.0.1:0 and closing the listener again, which
// leaves a window in which another process — or another test binary picking a
// port the same way — can take it before the server binds it. An attempt that
// loses that race is abandoned and the whole fixture rebuilt on a fresh port:
// server.New fixes the listen address from its first endpoint and Start has no
// setter for it, so retrying Start alone would rebind the same lost port
// forever. Exhausting the attempts calls t.Fatalf rather than log.Fatalf, which
// would take every other test in the run down with it.
func startTestServer(t *testing.T) (string, []*ua.NodeID) {
	t.Helper()

	const attempts = 5
	for range attempts {
		port, err := freePort()
		require.NoError(t, err, "reserve a free port")

		s := server.New(
			server.EndPoint("127.0.0.1", port),
			server.EnableSecurity("None", ua.MessageSecurityModeNone),
			server.EnableAuthMode(ua.UserTokenTypeAnonymous),
		)

		ns := server.NewNodeNameSpace(s, "TestNamespace")
		s.AddNamespace(ns)

		nodeIDs := make([]*ua.NodeID, 0, len(testNodes))
		for _, n := range testNodes {
			nodeIDs = append(nodeIDs, ns.AddNewVariableStringNode(n.name, n.value).ID())
		}

		if err := s.Start(context.Background()); err != nil {
			t.Logf("server did not start on port %d, retrying on a fresh port: %v", port, err)
			_ = s.Close()
			continue
		}
		t.Cleanup(func() { _ = s.Close() })
		return fmt.Sprintf("opc.tcp://127.0.0.1:%d", port), nodeIDs
	}

	t.Fatalf("no free port survived long enough to start the server in %d attempts", attempts)
	return "", nil
}

// freePort reserves an ephemeral port and releases it, returning the number.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	return l.Addr().(*net.TCPAddr).Port, l.Close()
}

// newTestSubscription connects a client to endpoint and returns a channel-based
// subscription with no nodes attached yet, plus the channel it delivers on.
func newTestSubscription(t *testing.T, ctx context.Context, endpoint string) (*Subscription, <-chan *DataChangeMessage) {
	t.Helper()

	c, err := opcua.NewClient(endpoint, opcua.SecurityMode(ua.MessageSecurityModeNone))
	require.NoError(t, err, "NewClient failed")
	require.NoError(t, c.Connect(ctx), "Connect failed")
	t.Cleanup(func() { _ = c.Close(ctx) })

	m, err := NewNodeMonitor(c)
	require.NoError(t, err, "NewNodeMonitor failed")
	m.SetErrorHandler(func(_ *opcua.Client, _ *Subscription, err error) {
		t.Logf("subscription reported an out-of-band error: %v", err)
	})

	ch := make(chan *DataChangeMessage, 16)
	sub, err := m.ChanSubscribe(ctx, &opcua.SubscriptionParameters{Interval: 50 * time.Millisecond}, ch)
	require.NoError(t, err, "ChanSubscribe failed")
	t.Cleanup(func() { _ = sub.Unsubscribe(ctx) })

	return sub, ch
}

// valuesByNodeID reads notifications until want NodeIDs have delivered a value
// or notificationTimeout expires, and returns the Int32 values each NodeID was
// seen carrying, in arrival order and without repeats. A node re-reporting a
// value it already delivered is therefore indistinguishable from one reporting
// it once, while a NodeID that delivers two different values keeps both — which
// is what several nodes' notifications arriving under one identity looks like
// from here.
//
// A short or mispaired result is not failed here: the caller compares the whole
// mapping, so a regression reports which node received which value instead of
// only that the deadline passed.
func valuesByNodeID(t *testing.T, ch <-chan *DataChangeMessage, want int) map[string][]int32 {
	t.Helper()

	got := make(map[string][]int32, want)
	deadline := time.After(notificationTimeout)
	for len(got) < want {
		select {
		case msg := <-ch:
			require.NoError(t, msg.Error, "notification carried an error")
			require.NotNil(t, msg.NodeID, "notification carried no NodeID")
			require.NotNil(t, msg.DataValue, "notification for %s carried no DataValue", msg.NodeID)
			require.NotNil(t, msg.Value, "notification for %s carried no Value", msg.NodeID)

			v, ok := msg.Value.Value().(int32)
			require.True(t, ok, "notification for %s carried %T, want int32", msg.NodeID, msg.Value.Value())

			id := msg.NodeID.String()
			if !slices.Contains(got[id], v) {
				got[id] = append(got[id], v)
			}
		case <-deadline:
			return got
		}
	}
	return got
}

// wantValues is the mapping valuesByNodeID should return for the first n nodes
// the fixture exposes: each NodeID paired with the single value written into it.
func wantValues(nodeIDs []*ua.NodeID, n int) map[string][]int32 {
	want := make(map[string][]int32, n)
	for i := range n {
		want[nodeIDs[i].String()] = []int32{testNodes[i].value}
	}
	return want
}

// TestBatchedAddMonitorItemsNotifiesEachNode monitors three nodes in a single
// AddMonitorItems call with all three Requests sharing one
// *ua.MonitoringParameters value, and requires each node's own value to arrive
// under that node's NodeID.
//
// Pairing is what the assertion is for. Counting how many distinct nodes showed
// up would pass on any permutation of the handle-to-NodeID mapping, which
// delivers every value under some other node's identity and is the same failure
// as delivering them all under one — the caller is misinformed either way. The
// nodes hold distinct values, so comparing the whole mapping pins each value to
// the node it was written into.
//
// Sharing one parameters value across Requests is the input reported in issue
// #880, and the reason it matters is that the ClientHandle is all a client has
// to attribute a value to a node. Part 4 §7.21 defines the field as the
// "Client-supplied id of the MonitoredItem. This id is used in Notifications
// generated for the list Node", and the MonitoredItemNotification carried in a
// DataChangeNotification (Part 4 §7.25.2, Table 161) holds only that handle and
// a Value — no NodeID, no MonitoredItemID, no index back into the create
// request. So an AddMonitorItems that stamps each handle through the caller's
// shared pointer instead of onto a per-item copy sends all three items carrying
// the last handle allocated: every notification then resolves to the last node,
// and the caller silently receives correct values attributed to the wrong nodes.
// Nothing reports it: no status code exists for a duplicate handle, and
// §5.13.2.1 leaves the server free to accept the parameters and echo them back.
//
// SamplingInterval and QueueSize are non-default so that the shared struct is
// the one issue #880 describes rather than an empty one. They are not asserted
// on, and cannot be from here: the in-tree server hardcodes RevisedQueueSize and
// revises the sampling interval to the subscription's publishing interval, so
// nothing that comes back reports whether the caller's parameters survived the
// copy. What this test pins is attribution.
func TestBatchedAddMonitorItemsNotifiesEachNode(t *testing.T) {
	ctx := context.Background()

	endpoint, nodeIDs := startTestServer(t)
	sub, ch := newTestSubscription(t, ctx, endpoint)

	shared := &ua.MonitoringParameters{
		SamplingInterval: 250,
		QueueSize:        5,
		DiscardOldest:    true,
	}
	reqs := make([]Request, 0, len(nodeIDs))
	for _, nid := range nodeIDs {
		reqs = append(reqs, Request{
			NodeID:               nid,
			MonitoringMode:       ua.MonitoringModeReporting,
			MonitoringParameters: shared,
		})
	}

	items, err := sub.AddMonitorItems(ctx, reqs...)
	require.NoError(t, err, "AddMonitorItems failed")
	require.Len(t, items, len(nodeIDs), "one item per request")

	got := valuesByNodeID(t, ch, len(nodeIDs))
	require.Equal(t, wantValues(nodeIDs, len(nodeIDs)), got,
		"each node's value must arrive under its own NodeID; got %v", got)
}

// TestAddNodeIDsWithNilParametersNotifies monitors a node through AddNodeIDs,
// which builds its Requests with MonitoringParameters left nil, and requires
// that node's value to arrive under its own NodeID.
//
// AddMonitorItems copies the caller's parameters only when there are any; with
// nil parameters the request keeps the defaults that
// opcua.NewMonitoredItemCreateRequestWithDefaults supplies, handle included.
// AddNodeIDs, AddNodes and NodeMonitor.Subscribe all depend on that branch and
// no other test here enters it, so this is the only guard against a change that
// assumes parameters are always present: invert the guard and this test dies on
// a nil dereference inside AddMonitorItems, while
// TestBatchedAddMonitorItemsNotifiesEachNode above still passes.
func TestAddNodeIDsWithNilParametersNotifies(t *testing.T) {
	ctx := context.Background()

	endpoint, nodeIDs := startTestServer(t)
	sub, ch := newTestSubscription(t, ctx, endpoint)

	require.NoError(t, sub.AddNodeIDs(ctx, nodeIDs[0]), "AddNodeIDs failed")

	got := valuesByNodeID(t, ch, 1)
	require.Equal(t, wantValues(nodeIDs, 1), got,
		"the nil-parameters node's value must arrive under its own NodeID; got %v", got)
}
