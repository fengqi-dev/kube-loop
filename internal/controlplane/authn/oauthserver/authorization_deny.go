package oauthserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ory/fosite"
)

func (endpoints *Endpoints) CancelAuthorization(
	rw http.ResponseWriter,
	request *http.Request,
	transaction, csrf string,
) error {
	return endpoints.DenyAuthorization(
		rw,
		request,
		transaction,
		csrf,
		"access_denied",
	)
}

func (endpoints *Endpoints) DenyAuthorization(
	rw http.ResponseWriter,
	request *http.Request,
	transaction, csrf, code string,
) error {
	stored, err := endpoints.repositories.OAuthAuthorizationRequests().
		Consume(request.Context(), signatureHash(transaction), time.Now().UTC())
	if err != nil ||
		subtle.ConstantTimeCompare(stored.CSRFHash, signatureHash(csrf)) != 1 {
		return fosite.ErrInvalidRequest
	}
	var dto authorizationRequestDTO
	if json.Unmarshal(stored.RequestJSON, &dto) != nil {
		return fosite.ErrInvalidRequest
	}
	original, err := http.NewRequestWithContext(
		request.Context(),
		http.MethodGet,
		dto.URL,
		nil,
	)
	if err != nil {
		return err
	}
	authorizeRequest, err := endpoints.provider.NewAuthorizeRequest(
		request.Context(),
		original,
	)
	if err != nil {
		return err
	}
	denial := fosite.ErrAccessDenied
	switch code {
	case "login_required":
		denial = fosite.ErrLoginRequired
	case "consent_required":
		denial = fosite.ErrConsentRequired
	}
	endpoints.provider.WriteAuthorizeError(
		request.Context(),
		rw,
		authorizeRequest,
		denial,
	)
	return nil
}

func randomAuthorizationValue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate OAuth authorization value")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func exactScopeHash(scopes []string) []byte {
	scopes = append([]string(nil), scopes...)
	slices.Sort(scopes)
	sum := sha256.Sum256([]byte(strings.Join(slices.Compact(scopes), "\x00")))
	return sum[:]
}
