package pubsubdebug

import "github.com/birdordev/plumego/x/messaging/pubsub"

// PubSubConfig configures the optional pubsub snapshot endpoint.
type PubSubConfig struct {
	Enabled bool          // Whether to enable the debug endpoint
	Path    string        // Path for the debug endpoint
	Pub     pubsub.Broker // PubSub instance for snapshotting
}
