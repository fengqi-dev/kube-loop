package tunnel

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// SessionToken is an unguessable capability shared by all Gateway
// connections that belong to one desktop session.
type SessionToken [32]byte

type SessionHeader struct {
	Command byte
	Token   SessionToken
}

func NewSessionToken() (SessionToken, error) {
	var token SessionToken
	if _, err := rand.Read(token[:]); err != nil {
		return SessionToken{}, fmt.Errorf("generate Gateway session token: %w", err)
	}
	return token, nil
}

// RelaySessionToken deterministically maps one authenticated Cluster Session
// generation to the protocol tenant key. It is not a credential: authorization
// is provided by the RelayTicket on the enclosing WebSocket connection.
func RelaySessionToken(sessionID string, generation uint64) (SessionToken, error) {
	sessionID = strings.TrimSpace(sessionID)
	parsed, err := uuid.Parse(sessionID)
	if err != nil || parsed.String() != sessionID {
		return SessionToken{}, errors.New("relay session ID must be a canonical UUID")
	}
	if generation == 0 {
		return SessionToken{}, errors.New("relay session generation is required")
	}
	contents := "kubeloop-relay-session-v2\x00" + sessionID + "\x00" + strconv.FormatUint(generation, 10)
	digest := sha256.Sum256([]byte(contents))
	return SessionToken(digest), nil
}

// ReadSessionHeader reads the protocol marker, command, and tenant capability.
func ReadSessionHeader(r io.Reader) (SessionHeader, error) {
	var header [5 + len(SessionToken{})]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return SessionHeader{}, err
	}
	if [4]byte(header[:4]) != magic {
		return SessionHeader{}, errors.New("invalid tunnel protocol magic")
	}
	token := SessionToken(header[5:])
	if token == (SessionToken{}) {
		return SessionHeader{}, errors.New("gateway session token is required")
	}
	return SessionHeader{Command: header[4], Token: token}, nil
}

func appendSessionHeader(value []byte, command byte, token SessionToken) ([]byte, error) {
	if token == (SessionToken{}) {
		return nil, errors.New("gateway session token is required")
	}
	value = append(value, magic[:]...)
	value = append(value, command)
	return append(value, token[:]...), nil
}
