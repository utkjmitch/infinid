package mqtt

import (
	"fmt"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// PahoPublisher adapts an eclipse/paho client to the Publisher seam, with
// LWT ("offline" retained on the availability topic), auto-reconnect, and
// QoS 0 (retained state makes redelivery unnecessary).
type PahoPublisher struct {
	c paho.Client
}

// Connect dials the broker. broker is a URL like tcp://host:1883. The LWT
// makes daemon death indistinguishable from an explicit offline — HA
// degrades to unavailable either way.
func Connect(broker, user, pass, clientID, availabilityTopic string) (*PahoPublisher, error) {
	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID(clientID).
		SetUsername(user).
		SetPassword(pass).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetWill(availabilityTopic, "offline", 0, true)
	c := paho.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(30 * time.Second) {
		return nil, fmt.Errorf("mqtt connect timeout")
	}
	if tok.Error() != nil {
		return nil, tok.Error()
	}
	return &PahoPublisher{c: c}, nil
}

// Publish implements Publisher.
func (p *PahoPublisher) Publish(topic string, payload []byte, retain bool) error {
	tok := p.c.Publish(topic, 0, retain, payload)
	tok.WaitTimeout(5 * time.Second)
	return tok.Error()
}
