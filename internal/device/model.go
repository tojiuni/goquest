// Package device manages push-notification token registrations for mobile
// clients (iOS APNs, Android FCM). The dispatcher that consumes this registry
// to send pushes lives outside this package — see docs/ios-apns-dispatcher-todo.md.
package device

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

type Token struct {
	ID         uuid.UUID `json:"id"`
	UserID     string    `json:"user_id"`
	Platform   Platform  `json:"platform"`
	APNSToken  string    `json:"apns_token"` // platform-specific token (FCM/APNs)
	AppVersion string    `json:"app_version,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type RegisterReq struct {
	Platform   Platform `json:"platform"`
	APNSToken  string   `json:"apns_token"`
	AppVersion string   `json:"app_version,omitempty"`
}

var ErrNotFound = errors.New("device: not found")
