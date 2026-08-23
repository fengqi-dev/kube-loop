package storage

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func normalizeIdentity(identity *Identity) error {
	if _, err := uuid.Parse(identity.ID); err != nil {
		return errors.New("identity ID must be a UUID")
	}
	identity.Type = strings.TrimSpace(identity.Type)
	identity.DisplayName = strings.TrimSpace(identity.DisplayName)
	identity.PrimaryEmail = strings.ToLower(
		strings.TrimSpace(identity.PrimaryEmail),
	)
	identity.Status = strings.TrimSpace(identity.Status)
	if identity.Type != identityTypeHuman && identity.Type != "machine" {
		return errors.New("identity type is invalid")
	}
	if identity.DisplayName == "" || len(identity.DisplayName) > 256 ||
		len(identity.PrimaryEmail) > 320 {
		return errors.New("identity profile is invalid")
	}
	if !validIdentityStatus(identity.Status) {
		return errors.New("identity status is invalid")
	}
	now := time.Now().UTC()
	if identity.CreatedAt.IsZero() {
		identity.CreatedAt = now
	}
	if identity.UpdatedAt.IsZero() {
		identity.UpdatedAt = identity.CreatedAt
	}
	identity.CreatedAt = identity.CreatedAt.UTC()
	identity.UpdatedAt = identity.UpdatedAt.UTC()
	if identity.UpdatedAt.Before(identity.CreatedAt) {
		return errors.New("identity timestamps are invalid")
	}
	return nil
}

func validIdentityStatus(status string) bool {
	return status == statusActive || status == "suspended" ||
		status == "disabled"
}

func scanIdentity(row rowScanner) (Identity, error) {
	var identity Identity
	var createdAt, updatedAt string
	if err := row.Scan(
		&identity.ID,
		&identity.Type,
		&identity.DisplayName,
		&identity.PrimaryEmail,
		&identity.Status,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Identity{}, err
	}
	var err error
	identity.CreatedAt, err = parseTime(createdAt, "identity creation time")
	if err == nil {
		identity.UpdatedAt, err = parseTime(updatedAt, "identity update time")
	}
	return identity, err
}
