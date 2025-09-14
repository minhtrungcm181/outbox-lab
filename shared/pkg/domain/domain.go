package domain

import "time"

// Channel defines the type of notification
type Channel string

const (
	ChannelSMS   Channel = "SMS"
	ChannelEmail Channel = "EMAIL"
	ChannelPush  Channel = "PUSH"
)

// Notification is the business record stored in DB
type Notification struct {
	ID         string
	Channel    Channel
	Tenant     string
	Recipients []string
	Title      string
	Content    string
	Metadata   map[string]any
	CreatedAt  time.Time
}

// DispatchEvent is what gets published to Kafka from the outbox
type DispatchEvent struct {
	EventID        string
	NotificationID string
	Channel        Channel
	Tenant         string
	To             []string
	Title          string
	Content        string
	Metadata       map[string]any
	CreatedAt      time.Time
	Attempt        int
}

// DeliveryStatus is the result of sending to a provider
type DeliveryStatus string

const (
	DeliveryQueued DeliveryStatus = "QUEUED"
	DeliverySent   DeliveryStatus = "SENT"
	DeliveryFailed DeliveryStatus = "FAILED"
	DeliveryRetry  DeliveryStatus = "RETRY"
)
