package httpauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

type strictJSON[T any] struct{ Value T }

func (body *strictJSON[T]) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body.Value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

type startRequest struct {
	ClientCallback string `json:"clientCallback"`
	State          string `json:"state"`
	Nonce          string `json:"nonce"`
	PKCEChallenge  string `json:"pkceChallenge"`
}
type startResponse struct {
	AuthorizationURL string `json:"authorizationUrl"`
	ExpiresAt        string `json:"expiresAt"`
}
type exchangeRequest struct {
	Code         string `json:"code"`
	PKCEVerifier string `json:"pkceVerifier"`
	DeviceID     string `json:"deviceId"`
}
type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}
type anonymousRequest struct {
	DeviceID string `json:"deviceId"`
}
type tokenResponse struct {
	TokenType        string `json:"tokenType"`
	AccessToken      string `json:"accessToken"`
	AccessExpiresAt  string `json:"accessExpiresAt"`
	RefreshToken     string `json:"refreshToken"`
	RefreshExpiresAt string `json:"refreshExpiresAt"`
}
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
