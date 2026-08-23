package storage

import (
	"database/sql"
	"errors"
	"strings"
)

func normalizeAdminSession(session *AdminSession) error {
	session.IdentityID = strings.TrimSpace(session.IdentityID)
	session.AuthorizationID = strings.TrimSpace(session.AuthorizationID)
	session.AuthenticationType = strings.TrimSpace(session.AuthenticationType)
	if len(session.IDHash) != sha256Size ||
		len(session.CSRFTokenHash) != sha256Size {
		return errors.New(
			"management Session and CSRF hashes must be SHA-256 values",
		)
	}
	switch session.AuthenticationType {
	case sessionKindNormal, "bootstrap":
		if validateUUID(
			session.IdentityID,
			"management session identity ID",
		) != nil ||
			validateUUID(session.AuthorizationID, "management session authorization ID") != nil {
			return errors.New(
				"authenticated management session identity is invalid",
			)
		}
	default:
		return errors.New("management session authentication type is invalid")
	}
	if session.AuthenticatedAt.IsZero() {
		session.AuthenticatedAt = session.CreatedAt
	}
	if session.CreatedAt.IsZero() || session.LastSeenAt.IsZero() ||
		session.IdleExpiresAt.IsZero() || session.AbsoluteExpiresAt.IsZero() ||
		session.LastSeenAt.Before(session.CreatedAt) ||
		!session.IdleExpiresAt.After(
			session.LastSeenAt,
		) || session.IdleExpiresAt.After(session.AbsoluteExpiresAt) {
		return errors.New("management session schema or lifetime is invalid")
	}
	session.IDHash = append([]byte(nil), session.IDHash...)
	session.CSRFTokenHash = append([]byte(nil), session.CSRFTokenHash...)
	session.CreatedAt = session.CreatedAt.UTC()
	session.AuthenticatedAt = session.AuthenticatedAt.UTC()
	session.LastSeenAt = session.LastSeenAt.UTC()
	session.IdleExpiresAt = session.IdleExpiresAt.UTC()
	session.AbsoluteExpiresAt = session.AbsoluteExpiresAt.UTC()
	if session.RevokedAt != nil {
		value := session.RevokedAt.UTC()
		session.RevokedAt = &value
	}
	return nil
}

func scanAdminSession(row rowScanner) (AdminSession, error) {
	var session AdminSession
	var identityID, authorizationID, revokedAt sql.NullString
	var authenticatedAt, createdAt, lastSeenAt, idleExpiresAt, absoluteExpiresAt string
	if err := row.Scan(
		&session.IDHash, &identityID, &authorizationID, &session.AuthenticationType,
		&session.CSRFTokenHash, &authenticatedAt, &createdAt, &lastSeenAt,
		&idleExpiresAt, &absoluteExpiresAt, &revokedAt,
	); err != nil {
		return AdminSession{}, err
	}
	session.IdentityID = identityID.String
	session.AuthorizationID = authorizationID.String
	var err error
	if session.AuthenticatedAt, err = parseTime(authenticatedAt, "management session authentication time"); err != nil {
		return AdminSession{}, err
	}
	if session.CreatedAt, err = parseTime(createdAt, "management session creation time"); err != nil {
		return AdminSession{}, err
	}
	if session.LastSeenAt, err = parseTime(lastSeenAt, "management session last-seen time"); err != nil {
		return AdminSession{}, err
	}
	if session.IdleExpiresAt, err = parseTime(idleExpiresAt, "management session idle expiry"); err != nil {
		return AdminSession{}, err
	}
	if session.AbsoluteExpiresAt, err = parseTime(absoluteExpiresAt, "management session absolute expiry"); err != nil {
		return AdminSession{}, err
	}
	if session.RevokedAt, err = parseNullableTime(revokedAt, "management session revocation time"); err != nil {
		return AdminSession{}, err
	}
	return session, nil
}
