package auth

import (
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const guestSessionPrefix = "trguest1."

// GuestSession is a separate capability, never an email identity. Database
// invitation state remains authoritative after this signed cookie is checked.
type GuestSession struct {
	InvitationID string `json:"invitationId"`
	SessionID    string `json:"sessionId"`
	IssuedAt     int64  `json:"iat"`
	ExpiresAt    int64  `json:"exp"`
}

func (g GuestSession) ActorID() string { return "guest:" + g.InvitationID }

func (v *Validator) IssueGuestSession(invitationID, sessionID string, now time.Time) (string, error) {
	if invitationID == "" || sessionID == "" || len(invitationID) > 128 || len(sessionID) > 128 {
		return "", errors.New("invalid guest session")
	}
	claims := GuestSession{invitationID, sessionID, now.Unix(), now.Add(DefaultServerSessionTTL).Unix()}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return guestSessionPrefix + payload + "." + v.signSessionPayload(guestSessionPrefix+payload), nil
}

func (v *Validator) ValidateGuestSession(token string, now time.Time) (GuestSession, error) {
	var guest GuestSession
	if !strings.HasPrefix(token, guestSessionPrefix) {
		return guest, errors.New("guest session required")
	}
	payload, signature, ok := strings.Cut(strings.TrimPrefix(token, guestSessionPrefix), ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(v.signSessionPayload(guestSessionPrefix+payload))) {
		return guest, errors.New("invalid guest session")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &guest) != nil || guest.InvitationID == "" || guest.SessionID == "" || guest.ExpiresAt <= now.Unix() || guest.IssuedAt > now.Unix()+120 {
		return GuestSession{}, errors.New("invalid or expired guest session")
	}
	return guest, nil
}
