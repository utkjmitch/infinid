package mqtt

import (
	"fmt"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// PahoPublisher adapts an eclipse/paho client to the Publisher seam, with
// LWT ("offline" retained on the availability topic), auto-reconnect, and
// QoS 0 (retained state makes redelivery unnecessary).
type PahoPublisher struct {
	c          paho.Client
	reconnects uint64 // atomic; incremented by the OnConnect handler
	seen       uint64 // atomic; last value of reconnects consumed by Reconnected
}

// Connect dials the broker. broker is a URL like tcp://host:1883. The LWT
// makes daemon death indistinguishable from an explicit offline — HA
// degrades to unavailable either way.
//
// With ConnectRetry enabled, paho keeps dialing the broker in the
// background even when the initial attempt hasn't completed by the time we
// stop waiting — so a WaitTimeout expiry here is not treated as fatal: it
// means "still trying," not "failed." Returning an error in that case would
// abandon this client (whose retry loop paho leaves running) while the
// caller starts a second one, and the abandoned client — not the one the
// caller holds — would eventually win the clientID and claim the LWT.
// Instead Connect returns the client with a nil error, and Publish (below)
// reports an honest error until the connection actually opens; the
// exporter's own retry/heartbeat publishing self-heals once it does. Only a
// token that completes with an actual error (as opposed to timing out) is
// surfaced here.
func Connect(broker, user, pass, clientID, availabilityTopic string) (*PahoPublisher, error) {
	pp := &PahoPublisher{}
	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID(clientID).
		SetUsername(user).
		SetPassword(pass).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetWill(availabilityTopic, "offline", 0, true).
		SetOnConnectHandler(func(paho.Client) {
			// Runs on a paho-internal goroutine, not the caller's single
			// publish-loop goroutine — touch only the atomic counter here.
			atomic.AddUint64(&pp.reconnects, 1)
		})
	pp.c = paho.NewClient(opts)
	tok := pp.c.Connect()
	if tok.WaitTimeout(30 * time.Second) {
		if err := tok.Error(); err != nil {
			return nil, err
		}
	}
	return pp, nil
}

// Publish implements Publisher. It reports an honest error rather than a
// false success in two cases paho's own API makes easy to get wrong:
//
//   - paho silently drops QoS-0 publishes while reconnecting: it calls
//     token.flowComplete() (success, no error) without ever sending the
//     message, and Client.IsConnected() returns true for the whole
//     reconnecting window. Only IsConnectionOpen() reflects a live,
//     fully-connected session, so that's what we gate on.
//   - Token.WaitTimeout's bool return must be checked: on a timeout the
//     token's Error() is nil (paho leaves it unset so a caller may choose
//     to wait again), which would otherwise read as success.
func (p *PahoPublisher) Publish(topic string, payload []byte, retain bool) error {
	if !p.c.IsConnectionOpen() {
		return fmt.Errorf("mqtt: connection not open")
	}
	tok := p.c.Publish(topic, 0, retain, payload)
	if !tok.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("mqtt: publish timeout")
	}
	return tok.Error()
}

// Reconnected reports whether the client has (re)connected since the last
// call, consuming the signal (a second call returns false until another
// reconnect happens). Intended to be polled once per cycle from the single
// goroutine that drives Exporter, to trigger Exporter.Reassert after a
// reconnect — wiring happens in the Task 11 publish loop. Safe to call from
// that goroutine even though the underlying counter is only ever written
// from paho's own connection goroutine, via the OnConnectHandler installed
// in Connect: sync/atomic is what makes that handoff safe.
func (p *PahoPublisher) Reconnected() bool {
	cur := atomic.LoadUint64(&p.reconnects)
	prev := atomic.SwapUint64(&p.seen, cur)
	return cur != prev
}
