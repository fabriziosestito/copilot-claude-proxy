package auth

import (
	"errors"
	"time"
)

// NewDeviceAuthorizationForTest builds a DeviceAuthorization with the
// unexported polling fields set, for external tests.
func NewDeviceAuthorizationForTest(
	deviceCode string,
	interval time.Duration,
	expiresAt time.Time,
) *DeviceAuthorization {
	return &DeviceAuthorization{deviceCode: deviceCode, interval: interval, expiresAt: expiresAt}
}

// OAuthErrorCode returns the OAuth error code when err is a definitive OAuth
// error response, for external tests.
func OAuthErrorCode(err error) (string, bool) {
	var oauthErr *oauthError
	if errors.As(err, &oauthErr) {
		return oauthErr.code, true
	}
	return "", false
}
