package trafficstream

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/flynn/noise"
	"github.com/gorilla/websocket"
)

const noiseHandshakeName = "kubeloop.traffic.noise.v1"

const NoisePublicKeyBytes = 32

// NoiseStaticKeypair is the process-scoped Gateway identity used by Noise XX.
// The public key is distributed through the Control Plane's signed RelayTicket.
type NoiseStaticKeypair struct {
	Private []byte
	Public  []byte
}

func GenerateNoiseStaticKeypair() (NoiseStaticKeypair, error) {
	suite := noiseSuite()
	key, err := suite.GenerateKeypair(nil)
	if err != nil {
		return NoiseStaticKeypair{}, fmt.Errorf("generate Noise static key: %w", err)
	}
	return NoiseStaticKeypair{
		Private: append([]byte(nil), key.Private...),
		Public:  append([]byte(nil), key.Public...),
	}, nil
}

func EncodeNoisePublicKey(key []byte) (string, error) {
	if len(key) != NoisePublicKeyBytes {
		return "", errors.New("Noise public key is invalid")
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

func DecodeNoisePublicKey(encoded string) ([]byte, error) {
	key, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != NoisePublicKeyBytes || base64.RawURLEncoding.EncodeToString(key) != encoded {
		return nil, errors.New("Noise public key is invalid")
	}
	return key, nil
}

func noiseSuite() noise.CipherSuite {
	return noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
}

func noiseHandshake(
	ctx context.Context,
	connection *websocket.Conn,
	initiator bool,
	staticKey *NoiseStaticKeypair,
	expectedPeerStatic []byte,
	prologue []byte,
) (*noise.CipherState, *noise.CipherState, error) {
	if ctx == nil {
		return nil, nil, errors.New("traffic stream context is required")
	}
	suite := noiseSuite()
	localKey := noise.DHKey{}
	if staticKey == nil {
		generated, err := GenerateNoiseStaticKeypair()
		if err != nil {
			return nil, nil, err
		}
		staticKey = &generated
	}
	if len(staticKey.Private) != NoisePublicKeyBytes || len(staticKey.Public) != NoisePublicKeyBytes {
		return nil, nil, errors.New("Noise static keypair is invalid")
	}
	localKey.Private = append([]byte(nil), staticKey.Private...)
	localKey.Public = append([]byte(nil), staticKey.Public...)
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   suite,
		Pattern:       noise.HandshakeXX,
		Initiator:     initiator,
		Prologue:      append([]byte(noiseHandshakeName+"/"), prologue...),
		StaticKeypair: localKey,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create Noise handshake: %w", err)
	}

	write := func(message []byte) error {
		clearDeadline, deadlineErr := bindContext(ctx, connection.SetWriteDeadline, "write Noise handshake")
		if deadlineErr != nil {
			return deadlineErr
		}
		defer clearDeadline()
		if len(message) == 0 || len(message) > noise.MaxMsgLen {
			return errors.New("Noise handshake message size is invalid")
		}
		return connection.WriteMessage(websocket.BinaryMessage, message)
	}
	read := func() ([]byte, error) {
		clearDeadline, deadlineErr := bindContext(ctx, connection.SetReadDeadline, "read Noise handshake")
		if deadlineErr != nil {
			return nil, deadlineErr
		}
		defer clearDeadline()
		messageType, message, readErr := connection.ReadMessage()
		if readErr != nil {
			return nil, readErr
		}
		if messageType != websocket.BinaryMessage || len(message) == 0 || len(message) > noise.MaxMsgLen {
			return nil, errors.New("Noise handshake message is invalid")
		}
		return message, nil
	}

	if initiator {
		message, _, _, err := handshake.WriteMessage(nil, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("write Noise handshake message 1: %w", err)
		}
		if err := write(message); err != nil {
			return nil, nil, err
		}
		message, err = read()
		if err != nil {
			return nil, nil, fmt.Errorf("read Noise handshake message 2: %w", err)
		}
		if _, _, _, err := handshake.ReadMessage(nil, message); err != nil {
			return nil, nil, fmt.Errorf("process Noise handshake message 2: %w", err)
		}
		if expectedPeerStatic != nil && (len(expectedPeerStatic) != NoisePublicKeyBytes ||
			subtle.ConstantTimeCompare(handshake.PeerStatic(), expectedPeerStatic) != 1) {
			return nil, nil, errors.New("Gateway Noise static key does not match RelayTicket")
		}
		message, writeState, readState, err := handshake.WriteMessage(nil, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("write Noise handshake message 3: %w", err)
		}
		if err := write(message); err != nil {
			return nil, nil, err
		}
		return writeState, readState, nil
	}

	message, err := read()
	if err != nil {
		return nil, nil, fmt.Errorf("read Noise handshake message 1: %w", err)
	}
	if _, _, _, err := handshake.ReadMessage(nil, message); err != nil {
		return nil, nil, fmt.Errorf("process Noise handshake message 1: %w", err)
	}
	message, _, _, err = handshake.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("write Noise handshake message 2: %w", err)
	}
	if err := write(message); err != nil {
		return nil, nil, err
	}
	message, err = read()
	if err != nil {
		return nil, nil, fmt.Errorf("read Noise handshake message 3: %w", err)
	}
	_, readState, writeState, err := handshake.ReadMessage(nil, message)
	if err != nil {
		return nil, nil, fmt.Errorf("process Noise handshake message 3: %w", err)
	}
	return writeState, readState, nil
}
