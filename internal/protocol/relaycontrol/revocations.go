package relaycontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maximumRevokedSessions = 1024

func NewRevocationSummary(
	generation uint64,
	sessions []RevokedSession,
	generatedAt, validUntil time.Time,
) (RevocationSummary, error) {
	if generation == 0 {
		if len(sessions) != 0 {
			return RevocationSummary{}, errors.New("empty revocation generation cannot contain Sessions")
		}
		return RevocationSummary{}, nil
	}
	copySessions := append([]RevokedSession(nil), sessions...)
	slices.SortFunc(copySessions, func(left, right RevokedSession) int {
		return strings.Compare(left.SessionSHA256, right.SessionSHA256)
	})
	summary := RevocationSummary{
		Generation: generation, GeneratedAt: generatedAt.UTC(), ValidUntil: validUntil.UTC(),
		Sessions: copySessions,
	}
	summary.SHA256 = summaryDigest(summary)
	if err := summary.Validate(generatedAt.UTC()); err != nil {
		return RevocationSummary{}, err
	}
	return summary, nil
}

func HashSessionID(sessionID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(sessionID)))
	return hex.EncodeToString(digest[:])
}

func (summary RevocationSummary) Validate(now time.Time) error {
	if summary.Generation == 0 {
		hasContents := summary.SHA256 != "" || !summary.GeneratedAt.IsZero() ||
			!summary.ValidUntil.IsZero() || len(summary.Sessions) != 0
		if hasContents {
			return errors.New("empty revocation summary is inconsistent")
		}
		return nil
	}
	if len(summary.Sessions) > maximumRevokedSessions || len(summary.SHA256) != sha256HexLength ||
		summary.GeneratedAt.IsZero() || summary.ValidUntil.IsZero() ||
		!summary.ValidUntil.After(summary.GeneratedAt) || summary.GeneratedAt.After(now.Add(time.Minute)) ||
		!summary.ValidUntil.After(now) {
		return errors.New("revocation summary is invalid")
	}
	if _, err := hex.DecodeString(summary.SHA256); err != nil {
		return errors.New("revocation summary is invalid")
	}
	previous := ""
	for _, entry := range summary.Sessions {
		if len(entry.SessionSHA256) != sha256HexLength || entry.MaximumGeneration == 0 ||
			!entry.ExpiresAt.After(summary.GeneratedAt) || entry.SessionSHA256 <= previous {
			return errors.New("revocation summary Session entry is invalid")
		}
		if _, err := hex.DecodeString(entry.SessionSHA256); err != nil {
			return errors.New("revocation summary Session entry is invalid")
		}
		previous = entry.SessionSHA256
	}
	if summaryDigest(summary) != summary.SHA256 {
		return errors.New("revocation summary digest does not match its entries")
	}
	return nil
}

func (summary RevocationSummary) Revokes(sessionID string, generation uint64, now time.Time) bool {
	if summary.Generation == 0 || generation == 0 || !summary.ValidUntil.After(now) {
		return false
	}
	hash := HashSessionID(sessionID)
	index, found := slices.BinarySearchFunc(summary.Sessions, hash, func(entry RevokedSession, target string) int {
		return strings.Compare(entry.SessionSHA256, target)
	})
	if !found {
		return false
	}
	entry := summary.Sessions[index]
	return entry.ExpiresAt.After(now) && generation <= entry.MaximumGeneration
}

func summaryDigest(summary RevocationSummary) string {
	hash := sha256.New()
	for _, entry := range summary.Sessions {
		hash.Write([]byte(entry.SessionSHA256))
		hash.Write([]byte{0})
		hash.Write([]byte(strconv.FormatUint(entry.MaximumGeneration, 10)))
		hash.Write([]byte{0})
		hash.Write([]byte(strconv.FormatInt(entry.ExpiresAt.UTC().Unix(), 10)))
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
