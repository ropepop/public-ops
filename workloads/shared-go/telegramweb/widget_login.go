package telegramweb

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ValidateLoginWidget verifies the legacy Telegram Login Widget payload.
// See https://core.telegram.org/widgets/login.
func ValidateLoginWidget(values url.Values, botToken string, maxAge time.Duration, now time.Time) (Auth, error) {
	hashHex := strings.TrimSpace(values.Get("hash"))
	if hashHex == "" {
		return Auth{}, errors.New("missing hash")
	}
	botToken = strings.TrimSpace(botToken)
	if botToken == "" {
		return Auth{}, errors.New("missing bot token")
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.EqualFold(strings.TrimSpace(key), "hash") {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", key, values.Get(key)))
	}
	dataCheckString := strings.Join(lines, "\n")
	secretDigest := sha256.Sum256([]byte(botToken))
	expected := hmacSHA256(secretDigest[:], []byte(dataCheckString))
	actual, err := hex.DecodeString(hashHex)
	if err != nil {
		return Auth{}, fmt.Errorf("decode hash: %w", err)
	}
	if len(actual) != len(expected) || subtle.ConstantTimeCompare(actual, expected) != 1 {
		return Auth{}, errors.New("invalid Telegram login signature")
	}

	authRaw := strings.TrimSpace(values.Get("auth_date"))
	if authRaw == "" {
		return Auth{}, errors.New("missing auth_date")
	}
	authUnix, err := strconv.ParseInt(authRaw, 10, 64)
	if err != nil {
		return Auth{}, fmt.Errorf("invalid auth_date: %w", err)
	}
	authAt := time.Unix(authUnix, 0).UTC()
	if maxAge > 0 && now.UTC().Sub(authAt) > maxAge {
		return Auth{}, errors.New("Telegram login expired")
	}

	idRaw := strings.TrimSpace(values.Get("id"))
	if idRaw == "" {
		return Auth{}, errors.New("missing id")
	}
	userID, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil {
		return Auth{}, fmt.Errorf("invalid id: %w", err)
	}
	if userID <= 0 {
		return Auth{}, errors.New("invalid Telegram user id")
	}

	firstName := strings.TrimSpace(values.Get("first_name"))
	if firstName == "" {
		return Auth{}, errors.New("missing first_name")
	}

	return Auth{
		AuthDate: authAt,
		User: User{
			ID:        userID,
			FirstName: firstName,
			LastName:  strings.TrimSpace(values.Get("last_name")),
			Username:  strings.TrimSpace(values.Get("username")),
			PhotoURL:  strings.TrimSpace(values.Get("photo_url")),
		},
	}, nil
}
